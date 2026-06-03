#!/usr/bin/env python3
"""
ai-desktops desktop-web API server.

Reads /opt/ai-desktops/desktop.env for runtime state and serves
GET /api/desktop on 127.0.0.1:3001.

Uses only Python stdlib — no external dependencies required.
"""
import json
import os
import socket
import subprocess
from http.server import BaseHTTPRequestHandler, HTTPServer

ENV_FILE = "/opt/ai-desktops/desktop.env"
LISTEN_HOST = "127.0.0.1"
LISTEN_PORT = 3001


def read_env_file(path: str) -> dict:
    env = {}
    try:
        with open(path) as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if "=" in line:
                    key, _, value = line.partition("=")
                    env[key.strip()] = value.strip().strip('"')
    except OSError:
        pass
    return env


def service_active(name: str) -> bool:
    try:
        result = subprocess.run(
            ["systemctl", "is-active", name],
            capture_output=True,
            text=True,
            timeout=5,
        )
        return result.stdout.strip() == "active"
    except Exception:
        return False


def scan_repos(workspace: str) -> list:
    repos = []
    if not workspace or not os.path.isdir(workspace):
        return repos
    try:
        for entry in os.scandir(workspace):
            if entry.is_dir() and os.path.isdir(os.path.join(entry.path, ".git")):
                repos.append(entry.name)
    except OSError:
        pass
    return sorted(repos)


def build_desktop_info() -> dict:
    env = read_env_file(ENV_FILE)

    desktop_id = env.get("DESKTOP_ID", "unknown")
    github_owner = env.get("GITHUB_OWNER", "")
    workspace = env.get("WORKSPACE", "/workspace")
    environment = env.get("ENVIRONMENT", "dev")
    bridge_port = int(env.get("BRIDGE_PORT", "9445"))

    hostname = os.environ.get("HOSTNAME") or socket.gethostname()
    novnc_url = f"https://{hostname}:8443/novnc/vnc.html"

    services = [
        {"name": "docker", "active": service_active("docker")},
        {"name": "ai-agent-bridge", "active": service_active("ai-agent-bridge")},
        {"name": "novnc-desktop", "active": service_active("novnc-desktop")},
    ]

    return {
        "desktop_id": desktop_id,
        "hostname": hostname,
        "github_owner": github_owner,
        "environment": environment,
        "bridge_port": bridge_port,
        "workspace": workspace,
        "repos": scan_repos(workspace),
        "services": services,
        "novnc_url": novnc_url,
    }


class Handler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):  # type: ignore[override]  # noqa: N802
        pass

    def do_GET(self):  # noqa: N802
        if self.path == "/api/desktop":
            try:
                data = build_desktop_info()
                body = json.dumps(data).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except Exception as exc:
                error = json.dumps({"error": str(exc)}).encode()
                self.send_response(500)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(error)))
                self.end_headers()
                self.wfile.write(error)
        else:
            self.send_response(404)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"not found"}')


if __name__ == "__main__":
    server = HTTPServer((LISTEN_HOST, LISTEN_PORT), Handler)
    print(f"ai-desktops API server listening on http://{LISTEN_HOST}:{LISTEN_PORT}", flush=True)
    server.serve_forever()
