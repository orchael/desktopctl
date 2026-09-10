"""Remote credential rotation. Embedded in the CLI; only metadata is in argv."""
import base64
import json
import os
import pathlib
import re
import shlex
import subprocess
import sys
import tempfile


def run(*args, check=True):
    result = subprocess.run(args, capture_output=True, text=True)
    if check and result.returncode:
        # Child diagnostics may include secret values. Never forward them.
        raise RuntimeError(f"{args[0]} {args[1]} failed (exit {result.returncode})")
    return result


def normalize(value):
    if isinstance(value, (dict, list)):
        return json.dumps(value, separators=(',', ':'))
    value = str(value)
    if "\x00" in value:
        raise ValueError("secret value contains NUL")
    if "\n" in value or "\r" in value:
        value = json.dumps(json.loads(value), separators=(',', ':'))
    return value


def read_env(path):
    values = {}
    if path.exists():
        for line in path.read_text().splitlines():
            parts = shlex.split(line, comments=True)
            if parts and parts[0] == "export":
                parts = parts[1:]
            if len(parts) == 1 and "=" in parts[0]:
                key, value = parts[0].split("=", 1)
                values[key] = value
    return values


def quote(value):
    # Compatible with shell exports and systemd EnvironmentFile double quotes.
    return '"' + re.sub(r'([\\"$`])', r'\\\1', value) + '"'


def stage(path, text):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".credentials-", dir=path.parent)
    with os.fdopen(fd, "w") as handle:
        handle.write(text)
    return pathlib.Path(name)


def main():
    request = json.loads(base64.b64decode(sys.argv[1]))
    home = pathlib.Path.home().resolve()
    agents = home / ".config/bridgectl/agents.env"
    desktop_env = home / ".config/environment.d/desktop-secrets.conf"
    shell = home / ".desktop-secrets"
    values = {}
    for secret in request["paths"]:
        try:
            response = run("aws", "secretsmanager", "get-secret-value", "--region",
                           request["region"], "--secret-id", secret,
                           "--query", "SecretString", "--output", "text")
            data = json.loads(response.stdout)
            if not isinstance(data, dict):
                raise ValueError("expected an object")
            for key, value in data.items():
                if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
                    raise ValueError("invalid environment key")
                if value is not None:
                    values[key] = normalize(value)
        except (RuntimeError, ValueError, TypeError):
            raise RuntimeError(f"could not retrieve/validate secret {secret}; files not updated") from None
    if request["paths"] and not any(values.values()):
        raise RuntimeError("no secret values retrieved; files not updated")

    previous = read_env(agents) | read_env(desktop_env)
    credential_keys = {"CODEX_AUTH", "CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY"}
    rotate_codex = bool(credential_keys & (set(previous) | set(values)))
    auth_dirs = {home / ".config/bridgectl/codex-home", home / ".codex"} if rotate_codex else set()
    for source in (previous, values) if rotate_codex else ():
        if source.get("CODEX_HOME"):
            directory = pathlib.Path(source["CODEX_HOME"]).expanduser()
            if not directory.is_absolute() or not directory.resolve().is_relative_to(home):
                raise RuntimeError("CODEX_HOME must be a private absolute directory under the desktop user's home")
            auth_dirs.add(directory)
    for directory in auth_dirs:
        if not directory.resolve().is_relative_to(home):
            raise RuntimeError("refusing to rotate Codex auth in a shared or external directory")

    # Prepare every replacement before touching credentials or running sessions.
    text = "".join(f"{key}={quote(value)}\n" for key, value in sorted(values.items()))
    shell_text = "".join("export " + line + "\n" for line in text.splitlines())
    staged = []
    try:
        for target, content in ((agents, text), (desktop_env, text), (shell, shell_text)):
            staged.append((target, stage(target, content)))
        bashrc = home / ".bashrc"
        bashrc_text = bashrc.read_text() if bashrc.exists() else ""
        if ".desktop-secrets" not in bashrc_text:
            staged.append((bashrc, stage(bashrc, bashrc_text +
                '\n[ -f ~/.desktop-secrets ] && . ~/.desktop-secrets\n')))
        service_status = run("systemctl", "--user", "is-active", "--quiet", "bridgectl", check=False).returncode
        if service_status not in (0, 3, 4):
            raise RuntimeError("could not determine bridge service state; files not updated")
        active = service_status == 0
        if active:
            # The systemd service's control group owns every provider process.
            run("systemctl", "--user", "stop", "bridgectl")
        for target, temporary in staged:
            os.replace(temporary, target)
        for directory in auth_dirs:
            (directory / "auth.json").unlink(missing_ok=True)
        # environment.d generators do not reliably remove prior manager values.
        removed_keys = set(previous) | (credential_keys if rotate_codex else set())
        old_keys = sorted(key for key in removed_keys if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key))
        if old_keys:
            run("systemctl", "--user", "unset-environment", *old_keys)
        run("systemctl", "--user", "daemon-reload")
        if active:
            run("systemctl", "--user", "start", "bridgectl")
            run("systemctl", "--user", "is-active", "--quiet", "bridgectl")
        print("Secrets replaced." + (" Previous Codex auth caches cleared." if rotate_codex else "") +
              (" Bridge restarted; start or resume sessions to use the new credentials." if active else ""))
    finally:
        for _, temporary in staged:
            temporary.unlink(missing_ok=True)


try:
    main()
except (RuntimeError, OSError, ValueError) as error:
    # Do not include exception values from parsers, file contents or subprocesses.
    if isinstance(error, RuntimeError):
        print(f"ERROR: {error}", file=sys.stderr)
    else:
        print("ERROR: credential rotation failed; inspect file permissions and service state", file=sys.stderr)
    sys.exit(1)
