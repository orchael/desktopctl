#!/usr/bin/env python3
"""Desktop-side Codex scenario. Never write credentials or raw output to artifacts.

The bridge still starts the Codex provider and supplies its auth environment.
A small wrapper holds the provider's process group open after Codex exec replies,
so secrets reload can be checked with two live sessions without timing a model.
"""

import json
import os
from pathlib import Path
import re
import selectors
import stat
import subprocess
import sys
import time
import uuid

import yaml

BASE = Path.home() / ".local/share/ai-desktops-e2e"
CONFIG = Path.home() / ".config/bridgectl/config.yaml"
BACKUP = BASE / "original-config.yaml"
META = BASE / "codex-scenario.json"
MARKER = "ai_desktops_e2e_refresh_marker"


class ScenarioFailure(Exception):
    """Only controlled, non-secret diagnostic categories cross the SSH boundary."""


def error_category(text):
    lower = text.lower()
    if "refresh_token_reused" in lower or "refresh token has already been used" in lower:
        return "refresh_token_reused"
    if "token_expired" in lower or "token has expired" in lower or "account auth is expired" in lower:
        return "token_expired"
    if "401 unauthorized" in lower or "authentication failed" in lower or "invalid authentication" in lower:
        return "authentication_failed"
    if "provider unavailable" in lower or "provider not found" in lower:
        return "provider_unavailable"
    return None


def save(path, data):
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(data), encoding="utf-8")
    temporary.chmod(0o600)
    temporary.replace(path)


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


def service(action):
    subprocess.run(["systemctl", "--user", action, "bridgectl"], check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def process_start(pid):
    try:
        # Field 22, after the parenthesized process name (which may contain spaces).
        return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
    except FileNotFoundError:
        return None


def daemon_environment():
    pid = subprocess.check_output(["systemctl", "--user", "show", "bridgectl",
                                   "--property=MainPID", "--value"], text=True).strip()
    assert pid.isdigit() and pid != "0", "bridge service has no live process"
    return dict(item.split("=", 1) for item in Path(f"/proc/{pid}/environ").read_bytes()
                .decode().split("\0") if "=" in item)


def mount_filesystem(path, mountinfo=None):
    """Identify the innermost mount, including NFS mounted below a local home."""
    if mountinfo is None:
        mountinfo = Path("/proc/self/mountinfo").read_text()
    matches = []
    for line in mountinfo.splitlines():
        fields = line.split()
        separator = fields.index("-")
        mount = Path(re.sub(r"\\([0-7]{3})", lambda m: chr(int(m.group(1), 8)), fields[4]))
        if path == mount or mount in path.parents:
            matches.append((len(mount.parts), fields[separator + 1]))
    assert matches, "cannot identify auth storage filesystem"
    return max(matches)[1]


def private_auth_path(auth_home, require_file=True):
    """Validate storage before reading credentials or adding a refresh marker."""
    assert auth_home.is_absolute(), "Codex auth home must be absolute"
    real_home = Path.home().resolve(strict=True)
    resolved = auth_home.resolve(strict=True)
    assert real_home in resolved.parents, "Codex auth must remain beneath the desktop user's home"
    workspace = Path("/workspace").resolve()
    assert resolved != workspace and workspace not in resolved.parents, "Codex auth must not use shared workspace storage"
    assert mount_filesystem(resolved) not in ("nfs", "nfs4", "efs"), "Codex auth must not use EFS or NFS storage"
    info = resolved.stat()
    assert stat.S_ISDIR(info.st_mode) and info.st_uid == os.getuid() and info.st_mode & 0o077 == 0, "Codex auth directory must be private and owned by this user"
    auth = resolved / "auth.json"
    assert not auth.is_symlink(), "Codex auth file must not be a symlink"
    if require_file or auth.exists():
        assert mount_filesystem(auth) not in ("nfs", "nfs4", "efs"), "Codex auth file must not be bind-mounted from EFS or NFS storage"
        info = auth.stat()
        assert stat.S_ISREG(info.st_mode) and info.st_uid == os.getuid() and info.st_mode & 0o077 == 0 and info.st_nlink == 1, "Codex auth file must be private and owned by this user"
    return auth


def prepare():
    BASE.mkdir(mode=0o700, parents=True, exist_ok=True)
    if BACKUP.exists():
        raise RuntimeError("previous scenario config needs restore before retry")
    env = daemon_environment()
    seed = json.loads(env.get("CODEX_AUTH", "null"))
    assert isinstance(seed, dict) and isinstance(seed.get("tokens"), dict), "CODEX_AUTH account seed is missing"
    assert seed["tokens"].get("access_token") and seed["tokens"].get("refresh_token"), "account seed incomplete"
    original_config = CONFIG.read_bytes()
    config = yaml.safe_load(original_config)
    provider = config["providers"]["codex"]
    assert "binary" in provider, "codex provider missing"
    metadata = {"sentinel": "CODEX_E2E_" + uuid.uuid4().hex,
                "sessions": []}
    save(META, metadata)
    # Original command arguments may contain credentials. Keep them only in the
    # protected restoration copy, never in JSON scenario metadata.
    with os.fdopen(os.open(BACKUP, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as backup:
        backup.write(original_config)
    provider["binary"] = sys.executable
    provider["args"] = [str(Path(__file__).resolve()), "provider"]
    provider["startup_probe"] = "none"
    provider["required_env"] = []
    provider["prompt_pattern"] = ""
    # Explicitly remove fallback to another provider from this assertion.
    provider["fallbacks"] = []
    CONFIG.write_text(yaml.safe_dump(config), encoding="utf-8")
    service("restart")


def provider():
    metadata = read(META)
    auth_home = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex")))
    auth_file = private_auth_path(auth_home)
    # Only booleans, paths, and process identity leave this process; no values.
    record = {"pid": os.getpid(), "start": process_start(os.getpid()),
              "auth_path": str(auth_file),
              "marker_present": read(auth_file).get(MARKER) == metadata["sentinel"],
              "api_key_present": "OPENAI_API_KEY" in os.environ or "CODEX_API_KEY" in os.environ,
              "seed_env_present": "CODEX_AUTH" in os.environ}
    save(BASE / f"provider-{os.getpid()}.json", record)
    prompt = ('Do not use tools or change files. Reply only with the concatenation '
              'of "CODEX_" and "' + metadata["sentinel"][6:] + '".')
    original_provider = yaml.safe_load(BACKUP.read_text())["providers"]["codex"]
    command = [original_provider["binary"]] + original_provider.get("args", []) + ["exec", "--json", "--sandbox", "read-only", prompt]
    completed = subprocess.run(command, check=False)
    record["native_complete"] = True
    save(BASE / f"provider-{os.getpid()}.json", record)
    if completed.returncode:
        return completed.returncode
    # Keep the real bridge-owned provider group alive until explicit reload.
    time.sleep(600)
    return 0


def driver(label):
    metadata = read(META)
    proc = subprocess.Popen(["bridgectl", "run", "--provider", "codex", "--no-tty",
                             os.environ.get("E2E_WORKSPACE_PATH", "/workspace/desktopctl")], stdin=subprocess.PIPE,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    selector = selectors.DefaultSelector()
    selector.register(proc.stdout, selectors.EVENT_READ)
    buffer = b""
    answered = False
    last_error_category = None
    output_closed = False
    answer_deadline = time.monotonic() + 175
    deadline = time.monotonic() + 600
    try:
        # Drain stdout through EOF even if the child has exited: its final
        # assistant message may still be buffered in the pipe.
        while not output_closed and time.monotonic() < deadline:
            if not answered and time.monotonic() >= answer_deadline:
                break
            for key, _ in selector.select(timeout=1):
                chunk = os.read(key.fileobj.fileno(), 65536)
                if not chunk:
                    output_closed = True
                    break
                buffer += chunk
                category = error_category(buffer.decode(errors="replace"))
                if category:
                    # Codex can recover an initial 401 by refreshing its token.
                    # Classify diagnostics without publishing a terminal result
                    # until the process exits or the answer deadline expires.
                    last_error_category = category
                while b"\n" in buffer:
                    raw, buffer = buffer.split(b"\n", 1)
                    line = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", raw.decode(errors="replace")).strip()
                    try:
                        event = json.loads(line[line.index("{"):])
                    except (ValueError, json.JSONDecodeError):
                        continue
                    item = event.get("item", {})
                    if (event.get("type") == "item.completed" and
                            item.get("type") == "agent_message" and
                            item.get("text", "").strip() == metadata["sentinel"]):
                        answered = True
                        save(BASE / f"{label}-result.json", {"answered": True})
            # Bound memory while withholding provider output, including errors.
            buffer = buffer[-131072:]
    finally:
        if not answered and not (BASE / f"{label}-result.json").exists():
            # Publish the terminal outcome before bounded process cleanup so
            # session() can report its category within its own 180s deadline.
            save(BASE / f"{label}-result.json", {"answered": False,
                                               "category": last_error_category or "response_timeout"})
        proc.stdin.close()  # --no-tty EOF asks the bridge to stop this session.
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        selector.close()
        proc.stdout.close()


def session(label):
    metadata = read(META)
    previous = {p.name for p in BASE.glob("provider-*.json")}
    result_path = BASE / f"{label}-result.json"
    result_path.unlink(missing_ok=True)
    subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "driver", label],
                     start_new_session=True, stdin=subprocess.DEVNULL,
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        if result_path.exists():
            result = read(result_path)
            if not result["answered"]:
                raise ScenarioFailure(result.get("category", "authentication_failed"))
            break
        time.sleep(1)
    else:
        raise ScenarioFailure("response_timeout")
    new = [p for p in BASE.glob("provider-*.json") if p.name not in previous]
    assert len(new) == 1, "expected exactly one new bridge provider process"
    record = read(new[0])
    for _ in range(30):
        if record.get("native_complete"):
            break
        time.sleep(1)
        record = read(new[0])
    else:
        raise ScenarioFailure("response_timeout")
    assert not record["api_key_present"], "API keys leaked into account-authenticated provider environment"
    assert not record["seed_env_present"], "account seed leaked into provider environment after materializing auth.json"
    auth = private_auth_path(Path(record["auth_path"]).parent)
    account = read(auth)
    assert account.get("tokens", {}).get("access_token"), "provider did not persist account auth"
    assert auth.stat().st_mode & 0o077 == 0, "auth file permissions are too broad"
    assert process_start(record["pid"]) == record["start"], "provider exited before reload check"
    metadata["sessions"].append(record)
    save(META, metadata)
    if label == "first":
        account[MARKER] = metadata["sentinel"]
        save(auth, account)
    if label == "second":
        # Inspect the bridge's selection before native Codex can refresh and
        # serialize auth.json, which may legitimately omit unknown metadata.
        assert record["marker_present"], "second session overwrote refreshed desktop auth"
        # Re-establish the refresh marker after native serialization so the
        # reload assertion always starts with a distinguishable old snapshot.
        account[MARKER] = metadata["sentinel"]
        save(auth, account)
    if label == "third":
        assert not record["marker_present"], "reload failed to replace previous desktop credentials"


def reloaded():
    metadata = read(META)
    assert len(metadata["sessions"]) == 2, "missing live sessions for reload assertion"
    for record in metadata["sessions"]:
        assert process_start(record["pid"]) != record["start"], "old provider process survived secrets reload"
        auth = private_auth_path(Path(record["auth_path"]).parent, require_file=False)
        assert not auth.exists() or MARKER not in read(auth), "old auth cache survived reload"
    # Re-reading actual daemon environment confirms service is back with a seed.
    assert json.loads(daemon_environment()["CODEX_AUTH"])["tokens"]["access_token"], "reload lost account seed"


def restore():
    if BACKUP.exists():
        CONFIG.write_bytes(BACKUP.read_bytes())
        service("restart")
        BACKUP.unlink()


def main():
    action = sys.argv[1]
    if action == "provider":
        return provider()
    if action == "driver":
        driver(sys.argv[2])
        return 0
    if action in ("first", "second", "third"):
        session(action)
    else:
        {"prepare": prepare, "reloaded": reloaded, "restore": restore}[action]()
    print("PASS " + action)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ScenarioFailure as error:
        print("FAIL " + str(error))
        sys.exit(1)
    except AssertionError:
        print("FAIL assertion_failed")
        sys.exit(1)
    except Exception:
        # Tracebacks and provider errors could include credential-bearing values.
        print("FAIL configuration_failed")
        sys.exit(1)
