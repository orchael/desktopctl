# Task 07 - Lifecycle Commands

## Objective

Implement desktop lifecycle commands around Pulumi, AWS, and DynamoDB.

## Depends On

- Task 05
- Task 06

## Scope

Implement:

- `create`
- `stop`
- `start`
- `terminate`
- lifecycle state updates in DynamoDB
- local run directory logging under `~/.ai-desktops/runs/<run_id>/`

## Requirements

- `create` runs Pulumi preview by default before update.
- `create` creates a DynamoDB record in `creating` state before Pulumi up.
- `stop` uses AWS API to stop the EC2 instance and preserves Pulumi state.
- `start` uses AWS API to start the EC2 instance and refreshes DynamoDB.
- `terminate` runs Pulumi destroy and marks the record `terminated` only after success.
- Failed create leaves the EC2 instance running by default for debugging.
- All commands support `--json` where useful.

## Acceptance Criteria

- Unit tests cover lifecycle state transitions.
- Create failure records last failure phase/message.
- Stop/start do not destroy Pulumi state.
- Terminate does not delete or mark terminated before destroy succeeds.

## Verification

```bash
go test ./internal/desktop ./internal/store ./internal/pulumi
go test ./...
```
