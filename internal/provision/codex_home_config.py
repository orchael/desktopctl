#!/usr/bin/env python3
"""Disable Codex's in-TUI updater in bridgectl's Codex home without losing settings."""

import os
import pathlib
import re
import sys
import tempfile
import tomllib


def main() -> None:
    path = pathlib.Path(sys.argv[1])
    directory = path.parent
    if path.is_symlink() or any(parent.is_symlink() for parent in (directory, *directory.parents)):
        raise RuntimeError(f"refusing symlinked Codex config path: {path}")
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory.chmod(0o700)

    original = path.read_text(encoding="utf-8") if path.exists() else ""
    parsed = tomllib.loads(original)
    current = parsed.get("check_for_update_on_startup")
    if current is False:
        return
    if current is not None and current is not True:
        raise ValueError("check_for_update_on_startup must be a boolean")

    lines = original.splitlines(keepends=True)
    first_table = next((i for i, line in enumerate(lines) if line.lstrip().startswith("[")), len(lines))
    option = re.compile(r'^\s*(?:check_for_update_on_startup|"check_for_update_on_startup"|\'check_for_update_on_startup\')\s*=')
    if current is True:
        for index in range(first_table):
            if option.match(lines[index]):
                lines[index] = "check_for_update_on_startup = false\n"
                break
        else:
            raise RuntimeError("could not locate root Codex update setting")
    else:
        if first_table == len(lines) and lines and not lines[-1].endswith("\n"):
            lines[-1] += "\n"
        lines.insert(first_table, "check_for_update_on_startup = false\n")
    updated = "".join(lines)
    tomllib.loads(updated)

    fd, temporary = tempfile.mkstemp(prefix=".config.toml.", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as output:
            output.write(updated)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
        print("updated")
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


if __name__ == "__main__":
    main()
