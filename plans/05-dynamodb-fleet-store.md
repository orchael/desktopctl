# Task 05 - DynamoDB Fleet Store

## Objective

Implement the DynamoDB-backed fleet metadata store.

## Depends On

- Task 03

## Scope

Create:

- `internal/store`
- desktop record model
- create, get, list, update, and mark-terminated operations
- optimistic update support where practical

Desktop record fields:

- `desktop_id`
- `stack_name`
- `github_owner`
- `lifecycle_state`
- `instance_id`
- `hostname`
- `novnc_url`
- `ssh_target`
- `readiness`
- `created_at`
- `updated_at`
- last failure phase/message

## Requirements

- Use DynamoDB as an operational index, not as the infrastructure source of truth.
- `status` and `doctor` may later reconcile records with Pulumi outputs and AWS state.
- Store timestamps in RFC3339 format.
- Support `--json` output for list/status consumers.

## Acceptance Criteria

- Unit tests cover record marshaling and lifecycle transitions.
- `list` command reads from DynamoDB.
- `status <id>` reads one record and prints useful output.
- Missing records produce a clear not-found error.

## Verification

```bash
go test ./internal/store
go test ./...
```
