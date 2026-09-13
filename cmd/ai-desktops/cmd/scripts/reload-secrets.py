"""Remote credential rotation. Embedded in the CLI; only metadata is in argv."""
import base64
import json
import os
import pathlib
import re
import shlex
import stat
import subprocess
import sys
import tempfile


class CategorizedError(RuntimeError):
    def __init__(self, code, message):
        super().__init__(message)
        self.code = code


def run(*args, check=True, error_code=1):
    try:
        result = subprocess.run(args, capture_output=True, text=True)
    except OSError:
        raise CategorizedError(error_code, f"{args[0]} could not be started") from None
    if check and result.returncode:
        # Child diagnostics may include secret values. Never forward them.
        raise CategorizedError(error_code, f"{args[0]} {args[1]} failed (exit {result.returncode})")
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
    try:
        if path.exists():
            for line in path.read_text().splitlines():
                parts = shlex.split(line, comments=True)
                if parts and parts[0] == "export":
                    parts = parts[1:]
                if len(parts) == 1 and "=" in parts[0]:
                    key, value = parts[0].split("=", 1)
                    values[key] = value
    except (OSError, ValueError):
        raise CategorizedError(22, "could not safely read an existing credential output; files not updated") from None
    return values


def quote(value):
    # Compatible with shell exports and systemd EnvironmentFile double quotes.
    return '"' + re.sub(r'([\\"$`])', r'\\\1', value) + '"'


def surface_directories(path, home):
    return reversed([directory for directory in (path.parent, *path.parent.parents)
                     if directory.is_relative_to(home)])


def stage(path, text, home, mode=None):
    # All outputs were preflighted before staging begins. Create each missing
    # component explicitly: mkdir(parents=True) only applies mode to the leaf.
    for directory in surface_directories(path, home):
        directory.mkdir(mode=0o700, exist_ok=True)
    fd, name = tempfile.mkstemp(prefix=".credentials-", dir=path.parent)
    with os.fdopen(fd, "w") as handle:
        handle.write(text)
        if mode is not None:
            os.fchmod(handle.fileno(), mode)
    return pathlib.Path(name)


def validate_codex_seed(value):
    try:
        auth = json.loads(value)
        if not isinstance(auth, dict):
            raise ValueError("expected object")
        mode = auth.get("auth_mode")
        api_key = auth.get("OPENAI_API_KEY")
        tokens = auth.get("tokens")
        # Match bridgectl's native JSON field types and supported auth shapes.
        if mode is not None and not isinstance(mode, str):
            raise ValueError("invalid mode")
        if api_key is not None and not isinstance(api_key, str):
            raise ValueError("invalid key")
        if tokens is not None and not isinstance(tokens, dict):
            raise ValueError("invalid tokens")
        tokens = tokens or {}
        for key in ("access_token", "refresh_token"):
            if tokens.get(key) is not None and not isinstance(tokens[key], str):
                raise ValueError("invalid token")
        account = (mode in (None, "", "chatgpt") and
                   bool((tokens.get("access_token") or "").strip()) and
                   bool((tokens.get("refresh_token") or "").strip()))
        api = mode in (None, "", "apikey") and bool((api_key or "").strip())
        if not (account or api):
            raise ValueError("unsupported credentials")
    except (ValueError, TypeError):
        raise CategorizedError(20, "invalid CODEX_AUTH credentials; files not updated") from None


def validate_private_auth_path(path, directory=False):
    try:
        info = path.lstat()
    except FileNotFoundError:
        return
    expected_type = stat.S_ISDIR if directory else stat.S_ISREG
    if (not expected_type(info.st_mode) or info.st_uid != os.getuid() or
            (not directory and info.st_nlink != 1) or
            stat.S_IMODE(info.st_mode) & 0o077):
        # lstat rejects symlinks as well as incorrect file types. Do not print
        # supplied path strings or credential contents in diagnostics.
        raise CategorizedError(21, "Codex auth paths must be private, user-owned directories and regular files; files not updated")


def auth_filesystem(path, mountinfo):
    # A local home may contain a nested NFS mount or an NFS file bind-mount.
    # Match the innermost mount rather than only checking the home filesystem.
    matches = []
    try:
        for index, line in enumerate(mountinfo.splitlines()):
            fields = line.split()
            separator = fields.index("-")
            mount = pathlib.Path(re.sub(r"\\([0-7]{3})", lambda match: chr(int(match.group(1), 8)), fields[4]))
            if path == mount or mount in path.parents:
                matches.append((len(mount.parts), index, fields[separator + 1]))
    except (IndexError, ValueError):
        raise CategorizedError(23, "could not verify Codex auth filesystem; files not updated") from None
    if not matches:
        raise CategorizedError(23, "could not verify Codex auth filesystem; files not updated")
    return max(matches)[2]


def validate_surface_path(path, home, mountinfo, credential=True):
    if not home.is_absolute() or not path.is_relative_to(home):
        raise CategorizedError(22, "credential output must be under an absolute desktop home; files not updated")
    # Do not resolve first: doing so would hide symlinks in the home or parents.
    for directory in surface_directories(path, home):
        try:
            info = directory.lstat()
        except FileNotFoundError:
            continue
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or
                info.st_mode & 0o022):
            raise CategorizedError(22, "credential output directories must be real, user-owned and not group/other writable; files not updated")
    try:
        info = path.lstat()
    except FileNotFoundError:
        info = None
    if info is not None and (not stat.S_ISREG(info.st_mode) or
            info.st_uid != os.getuid() or info.st_nlink != 1 or
            info.st_mode & (0o077 if credential else 0o022)):
        raise CategorizedError(22, "credential output files must be safe, user-owned regular files; files not updated")
    # Include the file itself: a local directory can hold an NFS file bind-mount.
    for candidate in (*surface_directories(path, home), path):
        if auth_filesystem(candidate.resolve(), mountinfo) in ("nfs", "nfs4", "efs"):
            raise CategorizedError(23, "refusing credential output on EFS or NFS storage; files not updated")


def main():
    request = json.loads(base64.b64decode(sys.argv[1]))
    home = pathlib.Path.home()
    agents = home / ".config/bridgectl/agents.env"
    desktop_env = home / ".config/environment.d/desktop-secrets.conf"
    shell = home / ".desktop-secrets"
    bashrc = home / ".bashrc"
    try:
        mountinfo = pathlib.Path("/proc/self/mountinfo").read_text()
    except OSError:
        raise CategorizedError(23, "could not verify credential filesystems; files not updated") from None
    for target in (agents, desktop_env, shell):
        validate_surface_path(target, home, mountinfo)
    validate_surface_path(bashrc, home, mountinfo, credential=False)
    agent_values = {}
    desktop_values = {}
    sources = []
    if request["agent_path"]:
        sources.append((request["agent_path"], agent_values))
    sources.extend((secret, desktop_values) for secret in request["paths"])
    for secret, destination in sources:
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
                    destination[key] = normalize(value)
        except (RuntimeError, ValueError, TypeError):
            raise CategorizedError(25, "could not retrieve or validate a configured secret; files not updated") from None
    if sources and not any((agent_values | desktop_values).values()):
        raise CategorizedError(25, "no secret values retrieved; files not updated")
    values = agent_values | desktop_values
    # Missing means unconfigured; a present blank seed must not authorize
    # deleting working account credentials or silently falling back to an API key.
    if "CODEX_AUTH" in values:
        validate_codex_seed(values["CODEX_AUTH"])

    previous_sources = (read_env(agents), read_env(desktop_env), read_env(shell))
    previous = previous_sources[0] | previous_sources[1] | previous_sources[2]
    credential_keys = {"CODEX_AUTH", "CODEX_HOME", "CODEX_API_KEY", "OPENAI_API_KEY"}
    rotate_codex = bool(credential_keys & (set(previous) | set(values)))
    auth_dirs = {home / ".config/bridgectl/codex-home", home / ".codex"} if rotate_codex else set()
    for source in (*previous_sources, values) if rotate_codex else ():
        if source.get("CODEX_HOME"):
            # Keep validation identical to the value written to EnvironmentFile:
            # systemd and bridgectl do not expand shell-style ~ paths.
            directory = pathlib.Path(source["CODEX_HOME"])
            if not directory.is_absolute() or not directory.resolve().is_relative_to(home):
                raise CategorizedError(26, "CODEX_HOME must be a private absolute directory under the desktop user's home")
            auth_dirs.add(directory)
    for directory in auth_dirs:
        if not directory.resolve().is_relative_to(home):
            raise CategorizedError(26, "refusing to rotate Codex auth in a shared or external directory")
        validate_private_auth_path(directory, directory=True)
        validate_private_auth_path(directory / "auth.json")
        for path in (directory.resolve(), (directory / "auth.json").resolve()):
            if auth_filesystem(path, mountinfo) in ("nfs", "nfs4", "efs"):
                raise CategorizedError(23, "refusing to rotate Codex auth on EFS or NFS storage; files not updated")

    load_state = run("systemctl", "--user", "show", "bridgectl",
                     "--property=LoadState", "--value", error_code=24).stdout.strip()
    if load_state != "loaded":
        raise CategorizedError(24, "bridge service is not loaded; files not updated")
    service_status = run("systemctl", "--user", "is-active", "--quiet", "bridgectl", check=False).returncode
    if service_status not in (0, 3):
        raise CategorizedError(24, "could not determine bridge service state; files not updated")
    active = service_status == 0

    # Prepare every replacement before touching credentials or running sessions.
    agent_text = "".join(f"{key}={quote(value)}\n" for key, value in sorted(agent_values.items()))
    desktop_text = "".join(f"{key}={quote(value)}\n" for key, value in sorted(desktop_values.items()))
    shell_text = "".join("export " + line + "\n" for line in desktop_text.splitlines())
    staged = []
    try:
        for target, content in ((agents, agent_text), (desktop_env, desktop_text), (shell, shell_text)):
            staged.append((target, stage(target, content, home)))
        bashrc_text = bashrc.read_text() if bashrc.exists() else ""
        if ".desktop-secrets" not in bashrc_text:
            # Preserve existing safe shell-config permissions, not the default
            # private mode used for credential snapshots. New files stay 0600.
            bashrc_mode = stat.S_IMODE(bashrc.lstat().st_mode) if bashrc.exists() else None
            staged.append((bashrc, stage(bashrc, bashrc_text +
                '\n[ -f ~/.desktop-secrets ] && . ~/.desktop-secrets\n', home, mode=bashrc_mode)))
        if active:
            # The systemd service's control group owns every provider process.
            run("systemctl", "--user", "stop", "bridgectl", error_code=24)
        for target, temporary in staged:
            os.replace(temporary, target)
        for directory in auth_dirs:
            (directory / "auth.json").unlink(missing_ok=True)
        # environment.d generators do not reliably remove prior manager values.
        removed_keys = set(previous) | (credential_keys if rotate_codex else set())
        old_keys = sorted(key for key in removed_keys if re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key))
        if old_keys:
            run("systemctl", "--user", "unset-environment", *old_keys, error_code=24)
        run("systemctl", "--user", "daemon-reload", error_code=24)
        if active:
            run("systemctl", "--user", "start", "bridgectl", error_code=24)
            run("systemctl", "--user", "is-active", "--quiet", "bridgectl", error_code=24)
        print("Secrets replaced." + (" Previous Codex auth caches cleared." if rotate_codex else "") +
              (" Bridge restarted; start or resume sessions to use the new credentials." if active else
               " Bridge was inactive and remains stopped; start bridgectl before starting sessions."))
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
    if isinstance(error, CategorizedError):
        code = error.code
    else:
        code = 1
    sys.exit(code)
