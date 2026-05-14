# Task 10 - Repository Checkout

## Objective

Implement secure repository checkout into `/workspace`.

## Depends On

- Task 02
- Task 08
- Task 09

## Scope

Implement host-side and CLI-side repository handling:

- normalize repo inputs
- validate owner boundary before infrastructure changes
- pass repo list to bootstrap
- retrieve fine-scoped GitHub PAT from AWS secret storage
- clone requested repos under `/workspace`
- verify checked-out repo ownership during readiness

## Requirements

- v1 uses a fine-scoped GitHub PAT stored in AWS SSM Parameter Store or Secrets Manager.
- PAT value must not be logged.
- Mixed-owner repo inputs must fail before Pulumi update.
- Host-side clone script must revalidate the owner boundary.
- Re-running bootstrap must not clobber existing local work.

## Acceptance Criteria

- Unit tests cover repo normalization and mixed-owner rejection.
- A desktop can be created with multiple repos from the same owner.
- A mixed-owner request fails before cloud resources are changed.
- `doctor` reports missing or owner-mismatched repos.

## Verification

```bash
go test ./internal/repo ./internal/health
go test ./...
```
