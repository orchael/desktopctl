# Task 01 - Go Module and CLI Skeleton

## Objective

Create the initial Go module and `ai-desktops` CLI skeleton.

## Depends On

- `PRD.md`
- `ARCHITECTURE.md`

## Scope

Create:

- `go.mod`
- `cmd/ai-desktops/main.go`
- `internal/version`
- basic CLI command wiring
- global flags for `--config`, `--profile`, `--region`, and `--json`

Initial commands should exist but may return "not implemented":

- `bootstrap`
- `init-foundation`
- `create`
- `list`
- `status`
- `url`
- `ssh`
- `agent`
- `stop`
- `start`
- `terminate`
- `doctor`

## Requirements

- Use Go.
- Prefer Cobra unless there is a clear reason not to.
- Keep command implementation thin; put reusable behavior in `internal/`.
- `ai-desktops --help` must show all MVP commands.
- `ai-desktops version` must print a version string.

## Acceptance Criteria

- `go test ./...` passes.
- `go run ./cmd/ai-desktops --help` prints the root help.
- `go run ./cmd/ai-desktops version` prints the current version.
- Every MVP command is present in help output.

## Verification

```bash
go test ./...
go run ./cmd/ai-desktops --help
go run ./cmd/ai-desktops version
```
