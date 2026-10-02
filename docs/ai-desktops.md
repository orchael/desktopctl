# ai-desktops Agent-Host Guide

This guide covers provisioning, operating, and troubleshooting `bridgectl` on an **ai-desktops** Ubuntu 24.04 host — a machine where the bridge runs as a user-level systemd service and spawns AI agent CLIs against repositories under `/workspace`.

---

## Architecture and Security Boundary

```
ai-desktops host (Ubuntu 24.04)
┌─────────────────────────────────────────────────────┐
│                                                     │
│  /workspace/<repo>       ← agent working directories │
│                                                     │
│  bridgectl agent server  127.0.0.1:9445             │
│    ↕ PTY                                            │
│  claude / codex / opencode / gemini                 │
│    (from /opt/bridgectl/node_modules/)              │
│                                                     │
│  /home/ubuntu/.config/bridgectl/                    │
│    config.yaml           ← operator-supplied config │
│    agents.env            ← ubuntu:ubuntu 0600 keys  │
│                                                     │
│  /opt/bridgectl/         ← provider CLIs, root:root │
│  /var/lib/bridge/sessions.db  ← persistence │
│                                                     │
└─────────────────────────────────────────────────────┘
         ↑ localhost only — no external exposure
```

**Security constraints:**

- The bridge listens on `127.0.0.1:9445` only. Do not expose it publicly without adding mTLS and JWT (see [service.md](service.md)).
- Provider API keys live in `/home/ubuntu/.config/bridgectl/agents.env` (`ubuntu:ubuntu 0600`). They are injected into the user service environment at startup and never written to disk by the bridge.
- The `ubuntu` login user can read provider CLIs from `/opt/bridgectl`. Only root can update the root-controlled provider runtime.
- Agent subprocesses inherit the systemd sandbox and can write to `/workspace`, `/var/lib/bridge`, `/tmp`, and `/var/tmp` only.

---

## Package Install

Install `bridgectl` from the apt repository:

```bash
curl -fsSL https://orchael.github.io/bridgectl/install.sh | sudo bash
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user enable --now bridgectl
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user status bridgectl
```

The base package installs a provider-neutral daemon. It starts and passes a health check, but no AI providers are configured yet.

**What the package installs:**

```
/usr/bin/bridgectl
/usr/bin/bridge-ca
/etc/bridgectl/bridge.yaml                 ← default (no providers)
/usr/lib/systemd/user/bridge.service
/usr/lib/bridgectl/install-provider-runtime
/usr/share/bridgectl/provider-runtime/.nvmrc
/usr/share/bridgectl/provider-runtime/package.json
/usr/share/bridgectl/provider-runtime/pnpm-lock.yaml
/usr/share/doc/bridgectl/examples/bridge-example.yaml
```

---

## Provisioning Flow

Run these steps once after installing the package, or re-run them on upgrade.

### 1. Install the Provider Runtime

```bash
sudo env INSTALL_DIR=/opt/bridgectl /usr/lib/bridgectl/install-provider-runtime
```

This script:

1. Installs or verifies Node.js 24 via NodeSource (if not already present).
2. Copies the packaged runtime manifest to `/opt/bridgectl`.
3. Runs `pnpm install --frozen-lockfile --prod` in a staging directory and swaps `node_modules` into place only on success — a failed install leaves the existing runtime working.
4. Verifies each installed CLI binary is present and prints its version.
5. Sets ownership of `/opt/bridgectl` to `root:root`.

To verify an existing installation without making changes:

```bash
sudo env INSTALL_DIR=/opt/bridgectl /usr/lib/bridgectl/install-provider-runtime --verify
```

### 2. Apply the ai-desktops Config

Copy and customize the example config:

```bash
sudo install -d -o ubuntu -g ubuntu -m 0700 /home/ubuntu/.config/bridgectl
sudo -u ubuntu cp /usr/share/doc/bridgectl/examples/bridge-example.yaml \
  /home/ubuntu/.config/bridgectl/config.yaml
sudo -u ubuntu $EDITOR /home/ubuntu/.config/bridgectl/config.yaml
```

Uncomment one or more provider blocks in the config. See [Provider Configuration](#provider-configuration) below.

### 3. Install the systemd User Service

```bash
sudo install -d -o ubuntu -g ubuntu -m 0755 /home/ubuntu/.config/systemd/user
sudo install -o ubuntu -g ubuntu -m 0644 /usr/lib/systemd/user/bridge.service \
  /home/ubuntu/.config/systemd/user/bridgectl.service
```

The ai-desktops AMI overrides the upstream unit name with `bridgectl.service` and adds:
- `ExecStart=bridgectl server start --config %h/.config/bridgectl/config.yaml`
- `EnvironmentFile=-%h/.config/bridgectl/agents.env`
- `EnvironmentFile=-%h/.config/bridgectl/display.env`

### 4. Create the Credentials File

Create the credentials file before starting the service:

```bash
sudo install -m 0600 -o ubuntu -g ubuntu /dev/null /home/ubuntu/.config/bridgectl/agents.env
```

Add required API key variables for the providers you enabled:

```bash
# For Claude Code:
echo "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-..." | sudo tee -a /home/ubuntu/.config/bridgectl/agents.env >/dev/null

# For Codex:
echo "OPENAI_API_KEY=sk-..." | sudo tee -a /home/ubuntu/.config/bridgectl/agents.env >/dev/null

# For Gemini CLI:
echo "GEMINI_API_KEY=AIza..." | sudo tee -a /home/ubuntu/.config/bridgectl/agents.env >/dev/null
```

The leading `-` in `EnvironmentFile=-/path` means the service starts even if the file is absent. The bridge itself fails at startup if a configured provider's `required_env` variable is missing from the process environment.

### 5. Reload and Restart

```bash
sudo systemctl daemon-reload
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user restart bridgectl
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user status bridgectl
```

### 6. Verify Health

```bash
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) journalctl --user -u bridgectl --no-pager -n 30
```

Look for `bridge daemon starting` and `registered provider` log lines.

---

## Provider Runtime Layout

After running `install-provider-runtime`, the runtime is at:

```
/opt/bridgectl/
  .nvmrc                         ← required Node.js major version
  package.json                   ← pinned provider CLI versions
  pnpm-lock.yaml                 ← reproducible install manifest
  node_modules/
    @anthropic-ai/claude-code/   ← Claude Code CLI
    @openai/codex/               ← Codex CLI
    opencode-ai/                 ← OpenCode (native binary)
    @google/gemini-cli/          ← Gemini CLI
    .bin/
      claude
      codex
      opencode
      gemini
```

---

## Provider Configuration

Edit `/home/ubuntu/.config/bridgectl/config.yaml`. Uncomment the relevant block and supply credentials via `/home/ubuntu/.config/bridgectl/agents.env`.

### Claude Code

```yaml
providers:
  claude:
    binary: "/opt/bridgectl/node_modules/@anthropic-ai/claude-code/bin/claude.exe"
    args: []
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["CLAUDE_CODE_OAUTH_TOKEN"]
    prompt_pattern: '(?m)(❯|>\s*$)'
```

Requires `CLAUDE_CODE_OAUTH_TOKEN` in `/home/ubuntu/.config/bridgectl/agents.env`.

### Codex

```yaml
providers:
  codex:
    binary: "/usr/bin/node"
    args: ["/opt/bridgectl/node_modules/@openai/codex/bin/codex.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["OPENAI_API_KEY"]
    prompt_pattern: '(?m)(>\s*$|›)'
```

Requires `OPENAI_API_KEY` in `/home/ubuntu/.config/bridgectl/agents.env`.

### OpenCode

OpenCode ships as a native binary — no Node invocation needed.

```yaml
providers:
  opencode:
    binary: "/opt/bridgectl/node_modules/.bin/opencode"
    args: []
    startup_timeout: "45s"
    startup_probe: "output"
```

Requires `OPENAI_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (depending on which model is configured in OpenCode).

### Gemini CLI

```yaml
providers:
  gemini:
    binary: "/usr/bin/node"
    args: ["/opt/bridgectl/node_modules/@google/gemini-cli/dist/index.js"]
    startup_timeout: "60s"
    startup_probe: "output"
    required_env: ["GEMINI_API_KEY"]
```

Requires `GEMINI_API_KEY` in `/home/ubuntu/.config/bridgectl/agents.env`.

---

## Environment File Contract

The credentials file is the only supported mechanism for injecting secrets into the bridge service:

| Path | Owner | Mode | Purpose |
|---|---|---|---|
| `/home/ubuntu/.config/bridgectl/agents.env` | `ubuntu:ubuntu` | `0600` | Provider API keys and secrets |

Each line is a `KEY=value` pair. The bridge daemon inherits these variables at startup and passes them to provider subprocesses. The bridge never logs, redacts-and-logs, or writes secret values.

**Do not:**
- Write secrets into `bridge.yaml` or any other world-readable file.
- Store secrets in the repository, apt package, AMI, or instance metadata.
- Set permissions above `0600` on `agents.env`.

---

## Upgrade Workflow

When a new `bridgectl` package is published:

```bash
sudo apt-get update
sudo apt-get install -y --allow-downgrades bridgectl=1.4.3
sudo env INSTALL_DIR=/opt/bridgectl /usr/lib/bridgectl/install-provider-runtime
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user daemon-reload
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user restart bridgectl
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user status bridgectl
```

The `install-provider-runtime` step re-copies the updated manifest and reinstalls pinned provider CLIs into a staging directory. Existing workspaces and session persistence are unaffected.

---

## Doctor Check

Run the included readiness check to confirm the host is correctly configured as an agent-host before or after provisioning:

```bash
sudo /usr/local/bin/ai-desktops-doctor
```

### Managed terminal workflow

New pre-baked AMIs configure the `ubuntu` user with a ready-to-use terminal environment:

- NvChad starts non-interactively and uses the Catppuccin theme.
- GitHub CLI uses `vim` as its editor.
- tmux uses `C-a` as its prefix, `|` and `-` for pane splits, `r` to reload, vi copy-mode keys, login shells, clipboard integration, and automatic window renumbering.
- TPM loads pinned `tmux-sensible`, Catppuccin, CPU, kubectx, resurrect, and continuum plugins. Continuum restores sessions automatically.
- `gitmux` is installed from a checksum-verified release and is invoked only when the command is present.

The AMI owns configurations marked with `.ai-desktops-managed`. The build role preserves an existing unmarked `~/.config/nvim` or `~/.tmux.conf` instead of replacing it. This workflow is an AMI capability; fallback cloud-init installs the base `nvim` and `tmux` binaries but does not install these configurations.

New AMIs also include a pinned stable Visual Studio Code package from Microsoft's signed apt repository. Launch **Visual Studio Code** from the desktop application menu or run `code` in a terminal. The Packer build verifies the package version, CLI, and packaged `com.microsoft.VSCode.desktop` launcher; graphical launch is checked on a desktop created from the built AMI.

The script checks twelve items and prints `[OK]`, `[FAIL]`, or `[WARN]` for each:

| Check | What it verifies |
|---|---|
| Package version | `bridgectl` apt package is installed |
| Service state | `bridgectl.service` user service is active |
| Port 9445 | Daemon is listening on `127.0.0.1:9445` |
| Node.js | Node.js v24.x.x is on `PATH` |
| Provider runtime | `/opt/bridgectl/node_modules/` is present |
| Configured providers | One or more providers are enabled in `bridge.yaml` |
| Credentials | Required env variables are present in `agents.env` (names only, not values) |
| Native Codex home | `/home/ubuntu/.codex` is a real, non-symlinked directory owned by `ubuntu:ubuntu` with mode `0700` |
| Codex update prompt | Bridgectl's `/home/ubuntu/.config/bridgectl/codex-home/config.toml` sets `check_for_update_on_startup = false` |
| `/workspace` policy | Bridge `allowed_paths` includes `/workspace` |
| systemd user service | `bridgectl.service` is installed for the `ubuntu` user |
| Bridge health | Bridge responds on `127.0.0.1:9445` |

The script exits `0` if all checks pass and `1` if any `[FAIL]` item is found. `[WARN]` items are informational and do not cause a non-zero exit.

Bridgectl sessions run the Codex copy under `/opt/bridgectl/node_modules`. The AMI and fallback cloud-init disable Codex's startup update prompt in bridgectl's separate `codex-home/config.toml`, preserving other valid TOML keys. Upgrade Codex by rebuilding the pinned bridgectl provider runtime. If doctor reports the update prompt setting as missing, run `sudo -u ubuntu python3 /usr/local/bin/ai-desktops-codex-home-config /home/ubuntu/.config/bridgectl/codex-home/config.toml` on an AMI desktop, or use the same updater at `/opt/ai-desktops/codex_home_config.py` on a fallback desktop.

```
ai-desktops bridge doctor
=========================

[OK]   Package version: bridgectl 1.4.3
[OK]   Service state: active
[OK]   Port 9445: bound to 127.0.0.1
[OK]   Node.js: v24.2.0
[OK]   Provider runtime: /opt/bridgectl (node_modules present)
[OK]   Configured providers: claude
       CLAUDE_CODE_OAUTH_TOKEN: set
[OK]   Credentials: all required variables present
[OK]   Codex home: /home/ubuntu/.codex is private
[OK]   Codex update prompt: disabled in /home/ubuntu/.config/bridgectl/codex-home/config.toml
[OK]   /workspace: listed in bridge allowed_paths
[OK]   systemd user service: /home/ubuntu/.config/systemd/user/bridgectl.service
[OK]   Bridge health: healthy (127.0.0.1:9445)

Result: 12 OK, 0 FAIL
```

---

## Troubleshooting

### Service fails to start

```bash
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) journalctl --user -u bridgectl --no-pager -n 50
```

Common causes:

| Log message | Fix |
|---|---|
| `node runtime validation failed` | Node.js not installed or wrong version. Run `install-provider-runtime`. |
| `provider environment validation failed` | A required env var is missing. Check `agents.env`. |
| `open session store` | The configured persistence path is not writable. Keep it under `/home/ubuntu/.config/bridgectl` or fix ownership. |
| `listen ... bind: address already in use` | Port 9445 in use. Check `ss -tlnp` and resolve the conflict. |

### Check Node.js version

```bash
node --version           # should print v24.x.x
INSTALL_DIR=/opt/bridgectl /usr/lib/bridgectl/install-provider-runtime --verify
```

### Check provider CLI binaries

```bash
/opt/bridgectl/node_modules/.bin/claude --version
/opt/bridgectl/node_modules/.bin/codex --version
/opt/bridgectl/node_modules/.bin/opencode --version
/opt/bridgectl/node_modules/.bin/gemini --version
```

### Check runtime.provider_root is correct

If you see `node runtime validation failed: read .nvmrc: ...`, the bridge cannot find the `.nvmrc` at the configured `runtime.provider_root`. Verify:

```bash
grep provider_root /home/ubuntu/.config/bridgectl/config.yaml
ls /opt/bridgectl/.nvmrc
```

### Check /workspace access

```bash
# Verify the user service is installed:
cat /home/ubuntu/.config/systemd/user/bridgectl.service

# Verify the bridge policy allows /workspace:
grep workspace /home/ubuntu/.config/bridgectl/config.yaml

# Verify the directory exists:
ls -la /workspace
```

### Check bridge health

```bash
# Using the bridge-ca healthcheck binary (if built):
/usr/local/bin/plain-healthcheck -target 127.0.0.1:9445

# Using grpc-health-probe if installed:
grpc-health-probe -addr 127.0.0.1:9445
```

### View live logs

```bash
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) journalctl --user -u bridgectl -f
```

---

## Localhost-Only Default and Remote Exposure Warning

By default the bridge binds to `127.0.0.1:9445`. All connections originate from the same host. There is no encryption or authentication required for localhost-only use.

For ai-desktops created with both Tailscale and step-ca enabled, cloud-init changes the bridgectl listener to the desktop's Tailscale IPv4 address on the configured bridge port and adds the desktop's Tailscale DNS name to `server.san`. This enables direct `bridgectl` access over the tailnet while avoiding exposure on the public EC2 interface. Tailscale-only desktops remain localhost-only.

Known remote `bridgectl` clients can be preloaded at provisioning time. Configure `pki.step_ca_clients` or repeat `--step-ca-client issuer=<name>,public-key-path=<path>,required=true`; the CLI copies each JWT public key to `/home/ubuntu/.config/bridgectl/certs/jwt-clients/<issuer>.pub` and renders matching `step_ca.clients` entries into `/home/ubuntu/.config/bridgectl/config.yaml`.

**If you expose the bridge over the network** (by changing `server.listen` to `0.0.0.0:9445` or forwarding port 9445), you must also configure:

- `tls.ca_bundle`, `tls.cert`, `tls.key` — mTLS to authenticate clients
- `step_ca.clients` or `auth.jwt_public_keys` — JWT (Ed25519) to authorize RPCs

See [service.md — Security](service.md) for the full mTLS + JWT configuration reference.
