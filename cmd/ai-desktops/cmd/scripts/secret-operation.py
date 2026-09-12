"""Keep a desktop-wide lock through rotation AND the client's fleet commit.

Only protocol messages reach stdout. The persistent lock inode also records an
unfinished rotation; never unlink it. Children inherit the flock so a lost SSH
connection cannot admit another rotation while the old child is still running.
"""
import fcntl
import json
import os
from pathlib import Path
import stat
import subprocess
import sys


def reply(status, **fields):
    print(json.dumps(dict(status=status, **fields)), flush=True)


def private(info, directory=False):
    correct_type = stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)
    if not correct_type or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise RuntimeError("unsafe coordination path")
    if not directory and info.st_nlink != 1:
        raise RuntimeError("unsafe coordination path")


def main():
    directory = Path.home() / ".ai-desktops-secret-operation"
    directory.mkdir(mode=0o700, exist_ok=True)
    private(directory.lstat(), directory=True)
    with os.fdopen(os.open(directory / "lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600), "r+b", buffering=0) as lock:
        private(os.fstat(lock.fileno()))
        # Persist the lock inode and directory entry before any rotation. The
        # pending state must also survive a host reboot, not just an SSH loss.
        for parent in (directory, directory.parent):
            fd = os.open(parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(fd)
            finally:
                os.close(fd)
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            reply("busy")
            return
        reply("ready", pending=os.fstat(lock.fileno()).st_size != 0)
        rotated = False
        for line in sys.stdin:
            request = json.loads(line)
            if request["action"] == "run" and not rotated:
                lock.seek(0)
                lock.write(b"pending\n")
                lock.truncate()
                os.fsync(lock.fileno())
                result = subprocess.run(
                    ["bash", "-c", request["script"]], stdin=subprocess.DEVNULL,
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                    pass_fds=(lock.fileno(),), check=False,
                )
                if result.returncode:
                    reasons = {
                        20: "invalid_credentials",
                        21: "codex_auth_permissions",
                        22: "credential_output_permissions",
                        23: "shared_credential_storage",
                        24: "bridge_service",
                        25: "secret_retrieval",
                        26: "codex_home",
                    }
                    reply("rotation_failed", reason=reasons.get(result.returncode, "unknown"))
                    return
                rotated = True
                reply("rotated")
            elif request["action"] == "commit" and rotated:
                lock.truncate(0)
                os.fsync(lock.fileno())
                reply("committed")
                return
            else:
                reply("invalid_request")
                return


try:
    main()
except Exception:
    # Never expose child diagnostics, script content, or credential values.
    reply("coordination_failed")
    sys.exit(1)
