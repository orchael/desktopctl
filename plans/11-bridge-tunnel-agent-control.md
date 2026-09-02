# Task 11 - Bridge Tunnel and Agent Control

## Objective

Implement remote agent control through a CLI-managed tunnel to the desktop-local `bridgectl`.

## Depends On

- Task 08
- Task 09

## Scope

Create:

- `internal/tunnel`
- `internal/agent`
- `ai-desktops agent <id> ...` command group

Minimum agent commands:

- `agent <id> status`
- `agent <id> providers`
- `agent <id> start --provider <name> --repo <repo>`
- `agent <id> attach <session_id>`
- `agent <id> stop <session_id>`

Tunnel modes:

- SSM Session Manager port forwarding first
- SSH local port forwarding fallback

## Requirements

- `bridgectl` remains bound to `127.0.0.1:9445` on the desktop.
- The bridge port is not opened in the public security group.
- Tunnel lifetime is tied to the CLI command.
- Local forwarded port should be ephemeral.
- Command output should stream bridge session events when applicable.
- If SSM is unavailable, error should clearly say whether SSH fallback was attempted.

## Acceptance Criteria

- Unit tests cover tunnel command construction without invoking real SSH/SSM.
- `agent status` can reach the bridge through the tunnel.
- Bridge public exposure is not required.
- Failure modes identify SSM, SSH, or bridge connectivity separately.

## Verification

```bash
go test ./internal/tunnel ./internal/agent
go test ./...
go run ./cmd/ai-desktops agent <desktop_id> status
```
