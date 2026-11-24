"""Run Bridge machine login with only its dedicated Secrets Manager values.

The JSON secret arrives on stdin, never in argv or a persistent environment
file. This process runs as the desktop user after bridgectl.service is active.
"""

import json
import os
import subprocess
import sys


ALLOWED = {
    "BRIDGECTL_ENROLL_KEY",
    "BRIDGECTL_CLIENT_ID",
    "BRIDGECTL_CLIENT_SECRET",
    "BRIDGECTL_OIDC_ISSUER",
    "BRIDGECTL_OIDC_PROJECT_ID",
    "BRIDGECTL_BRIDGE_URL",
}


def main():
    values = json.load(sys.stdin)
    if not isinstance(values, dict) or not values:
        raise ValueError("Bridge enrollment secret must be a nonempty JSON object")
    if any(key not in ALLOWED or not isinstance(value, str) for key, value in values.items()):
        raise ValueError("Bridge enrollment secret has unsupported keys or non-string values")

    env = {key: value for key, value in os.environ.items() if key not in ALLOWED}
    env.pop("BRIDGECTL_STATE_DIR", None)
    env["HOME"] = "/home/ubuntu"
    env.update(values)
    subprocess.run(["/usr/bin/bridgectl", "login", "--non-interactive"], env=env, check=True)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, json.JSONDecodeError) as exc:
        print(f"Bridge enrollment configuration error: {exc}", file=sys.stderr)
        sys.exit(1)
