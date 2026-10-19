# Task 01 - Go Module and CLI Skeleton

## Objective

Create the initial Go module and `desktopctl` CLI skeleton.

## Depends On

- `PRD.md`
- `ARCHITECTURE.md`

## Scope

Create:

- `go.mod`
- `cmd/desktopctl/main.go`
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
- `desktopctl --help` must show all MVP commands.
- `desktopctl version` must print a version string.

## Acceptance Criteria

- `go test ./...` passes.
- `go run ./cmd/desktopctl --help` prints the root help.
- `go run ./cmd/desktopctl version` prints the current version.
- Every MVP command is present in help output.

## Verification

```bash
go test ./...
go run ./cmd/desktopctl --help
go run ./cmd/desktopctl version
```
