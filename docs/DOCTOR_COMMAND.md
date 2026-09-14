# Doctor Command — Desktop Health Checks

The `doctor` command runs a comprehensive suite of health checks against a deployed desktop to verify it's running correctly. Checks can pass, fail, warn, or be skipped. Warnings identify degraded optional behavior and do not produce a non-zero exit by themselves.

## Usage

```bash
ai-desktops doctor <desktop-id>
```

Example:
```bash
ai-desktops doctor d-001
```

## What It Checks

The doctor command performs health checks organized into categories. Optional groups are added only when the desktop was created with matching features such as `--secret`, Tailscale, step-ca, `--nested-virtualization`, `--mobile`, or `--avd`.

The System group includes two provisioning and monitoring signals:

- `bootstrap-state` reads `/var/lib/ai-desktops/bootstrap-status.json`. New desktops report `running`, `succeeded`, or `failed`; older desktops without the artifact receive a compatibility warning. Failed and malformed states point to `/var/log/cloud-init-output.log`.
- `cloudwatch-agent-active` checks the optional Amazon CloudWatch agent. An inactive agent is a warning rather than a hard failure and includes its systemd `LoadState`, `ActiveState`, `SubState`, `Result`, and `ExecMainStatus`, plus the journal command to run.

The lifecycle artifact contains only state, timestamps, an exit code, and the log path. It does not contain bootstrap command output or secret values.

### Network Connectivity (Always Run)

These checks verify the desktop is reachable and responding:

- **ssh-port** — SSH port 22 is reachable via TCP
- **novnc-https** — noVNC HTTPS endpoint responding on port 8443

### Essential Services (SSH-Based)

Require SSH key configured in `config.yaml`; skipped otherwise.

- **docker-active** — Docker daemon is active and running
- **nvim-installed** — Neovim is on the ubuntu user's PATH
- **tmux-installed** — Tmux is on the ubuntu user's PATH
- **bridgectl-installed** — bridgectl CLI is on PATH
- **bridgectl-config-exists** — bridgectl config exists
- **bridgectl-credentials-env** — provider credentials env file exists
- **codex-home-private** — `/home/ubuntu/.codex` is a real, non-symlinked directory owned by `ubuntu:ubuntu` with mode `0700`
- **bridgectl-service-active** — bridgectl user service is active

### System Resources (SSH-Based)

Monitor available system resources:

- **disk-space** — `/workspace` partition has >1GB free space
- **memory-available** — System has >512MB free memory
- **novnc-running** — novnc-desktop process is actively running

### Certificate & TLS (SSH-Based)

Verify TLS certificate setup and auto-renewal:

- **certbot-cert-valid** — TLS certificate is valid and won't expire within 7 days
- **certbot-timer-enabled** — Certbot auto-renewal timer (systemd) is enabled

### Workspace Integrity (SSH-Based)

Verify workspace and repository configuration:

- **workspace-mounted** — `/workspace` is mounted and writable
- **repo-<name>** — Each configured repository is a valid git clone

### Tailscale (Optional)

Only runs when the desktop was created with Tailscale enabled by `--tailscale` or `--tailscale-network`.

- **tailscale-installed** — Tailscale CLI is installed
- **tailscaled-active** — tailscaled systemd service is active
- **tailscale-running** — Tailscale backend state is Running
- **tailscale-network-metadata** — requested network name was recorded on the desktop
- **bridgectl-tailscale-listener** — when step-ca is also configured, bridgectl is bound on the desktop's Tailscale IPv4 address and bridge port; skipped for Tailscale-only desktops
- **bridgectl-tailscale-san** — when step-ca is also configured, the desktop's Tailscale DNS name is present in both bridgectl `server.san` and the issued bridge TLS certificate; skipped for Tailscale-only desktops

### step-ca (Optional)

Only runs when the desktop was created with step-ca enabled by `--step-ca`, or by `--tailscale` with `pki.step_ca_server` configured.

- **step-cli-installed** — Smallstep CLI is installed
- **step-ca-resolves** — configured step-ca DNS name resolves
- **step-ca-health** — `step ca health` succeeds
- **bridgectl-step-ca-env** — bridgectl step-ca environment file exists
- **bridgectl-step-ca-cert** — bridge TLS certificate and key were issued

## Output Formats

### Human-Readable (Default)

```
Desktop : d-001
Summary : all checks passed

  ✓ ssh-port
  ✓ novnc-https
  ✓ docker-active
  ✓ nvim-installed
  ✓ tmux-installed
  ✓ bridgectl-installed
  ✓ bridgectl-service-active
  ✓ disk-space
  ✓ memory-available
  ✓ novnc-running
  ✓ certbot-cert-valid
  ✓ certbot-timer-enabled
  ✓ workspace-mounted
  ✓ repo-my-repo
```

### JSON Output

Use `--json` flag for machine-readable output:

```bash
ai-desktops doctor d-001 --json
```

JSON reports warnings both on their individual checks and in the top-level `warnings` array. A report can therefore have `"passed": true` and a summary such as `passed with warnings: bootstrap-state`.

Output:
```json
{
  "desktop_id": "d-001",
  "checks": [
    {
      "name": "ssh-port",
      "status": "pass"
    },
    {
      "name": "disk-space",
      "status": "fail",
      "message": "exit status 1"
    }
  ],
  "passed": false,
  "summary": "failed: disk-space"
}
```

### CloudWatch startup timing

The CloudWatch package is installed in the Packer image, but its desktop-specific configuration is applied during runtime cloud-init. On a live September 2026 validation desktop, cloud-init began around instance creation and the agent became active roughly three minutes later. Running doctor during that interval can legitimately observe the agent as inactive even while required desktop services are becoming ready. The `bootstrap-state` check now makes that ordering visible, and `doctor --wait` remains available when the operator wants to wait for required checks to settle.

If CloudWatch remains inactive after bootstrap succeeds, inspect it without printing its configuration contents:

```bash
systemctl show amazon-cloudwatch-agent --no-pager \
  --property=LoadState --property=ActiveState --property=SubState \
  --property=Result --property=ExecMainStatus
journalctl -u amazon-cloudwatch-agent --no-pager -n 200
/opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl -a status
```

## Exit Codes

- **0** — No required check failed; warnings may be present
- **1** — One or more checks failed

Use exit codes for automation:

```bash
if ai-desktops doctor $DESKTOP_ID; then
  echo "Desktop is healthy"
else
  echo "Desktop has issues"
  exit 1
fi
```

## Understanding Failures

### Network Checks Fail

The desktop may not be reachable. Verify:
- EC2 instance is running
- Security group allows SSH (port 22)
- Desktop hostname resolves correctly
- SSM tunnel is open (for bridge check)

### SSH-Based Checks Skipped

If all SSH-based checks show `–` (skipped):
- SSH key not configured in `config.yaml` under `desktop.ssh_key_path`
- Key doesn't have permission to access the desktop

### Disk Space Fails

Less than 1GB free on `/workspace`:
```bash
# SSH into desktop and check
df -h /workspace

# Clean up if needed
docker system prune
```

### Certificate Expires Soon

Certificate will expire within 7 days:
```bash
# SSH into desktop and check expiration
sudo openssl x509 -in /etc/letsencrypt/live/*/fullchain.pem -noout -dates

# Force renewal
sudo certbot renew --force-renewal
```

### Git Repository Invalid

Repository clone is corrupted or missing:
```bash
# SSH into desktop and check
ls -la /workspace/repo-name/.git

# Re-clone if needed
cd /workspace
rm -rf repo-name
git clone <repo-url> repo-name
```

## Common Workflows

### Quick Health Check

```bash
ai-desktops doctor my-desktop
```

### Automated Monitoring

```bash
#!/bin/bash
DESKTOP="d-001"
if ! ai-desktops doctor "$DESKTOP" >/dev/null 2>&1; then
  echo "Alert: Desktop $DESKTOP health check failed"
  ai-desktops doctor "$DESKTOP"  # Show details
  exit 1
fi
```

### Daily Health Report

```bash
for desktop in d-001 d-002 d-003; do
  echo "=== $desktop ==="
  ai-desktops doctor "$desktop" || true
  echo
done
```

### JSON for Metrics Collection

```bash
# Collect metrics for monitoring system
ai-desktops doctor my-desktop --json | jq '.checks[] | select(.status == "fail")'
```

## Timeout

The doctor command has a 120-second timeout for all checks. If some SSH checks are slow (e.g., due to high latency), they may time out. Increase the timeout by running checks individually with `--timeout`:

Currently, the timeout is hardcoded at 120 seconds. For faster feedback, you can:
- Run only network checks (don't provide SSH key)
- Check specific services manually via SSH

## See Also

- [Certbot Setup](./CERTBOT_SETUP.md) — TLS certificate configuration
- [Getting Started](./GETTING_STARTED.md) — Desktop deployment
