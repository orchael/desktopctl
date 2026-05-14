# Task 09 - Readiness and Doctor

## Objective

Implement readiness checks and the `doctor` command.

## Depends On

- Task 07
- Task 08

## Scope

Create:

- `internal/health`
- readiness check runner
- `doctor <id>` command output
- DynamoDB readiness summary updates

Checks:

- EC2 instance state is running.
- SSH accepts the configured operator key.
- noVNC HTTPS endpoint responds.
- `novnc-desktop-url` can mint a valid browser URL.
- Docker service is active.
- `ai-agent-bridge.service` is active.
- required tools exist on `PATH`.
- requested repositories exist under `/workspace`.
- requested repositories match the configured GitHub owner.

## Requirements

- `create` must not mark a desktop `ready` until readiness passes.
- `doctor` must work against partially provisioned desktops.
- Check failures must include the failed phase and actionable detail.
- `doctor --json` must produce machine-readable results.

## Acceptance Criteria

- Unit tests cover result aggregation and status mapping.
- `doctor` exits non-zero when required checks fail.
- DynamoDB is updated with last readiness result.
- Partial failures are visible without destroying the desktop.

## Verification

```bash
go test ./internal/health ./internal/desktop
go test ./...
go run ./cmd/ai-desktops doctor <desktop_id>
go run ./cmd/ai-desktops doctor <desktop_id> --json
```
