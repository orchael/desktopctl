# Task 08 - Desktop Cloud-Init Bootstrap

## Objective

Build the first-boot desktop bootstrap that installs the required runtime on the EC2 host.

## Depends On

- Task 06

## Scope

Create cloud-init or rendered bootstrap assets for:

- base OS package setup
- `git`
- `docker`
- `nvim`
- `tmux`
- SSM agent validation or installation
- `novnc-desktop` installation with `desktop_type=elementary`
- `bridgectl` installation and user systemd service
- `/workspace` setup
- `/opt/ai-desktops` setup

## Requirements

- `bridgectl` must bind to `127.0.0.1`.
- Raw VNC must not be publicly reachable.
- Secrets are retrieved from AWS SSM Parameter Store or Secrets Manager using the instance role.
- Rendered secret files must have restrictive permissions.
- Bootstrap logs must be available over SSH.
- Failure must leave enough logs for `doctor` to diagnose.

## Acceptance Criteria

- New desktop reaches a state where SSH is available.
- Required tools are installed and on `PATH`.
- Docker service is active.
- `novnc-desktop` services are active.
- `bridgectl.service` is active and localhost-bound.

## Verification

```bash
go test ./...
go run ./cmd/ai-desktops doctor <desktop_id>
```
