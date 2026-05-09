# Task 03 - Pulumi Backend Bootstrap

## Objective

Implement `ai-desktops bootstrap` to create or verify the S3 Pulumi backend bucket and DynamoDB fleet table.

## Depends On

- Task 01
- Task 02

## Scope

Create:

- `internal/backend`
- AWS SDK helpers needed for S3 and DynamoDB bootstrap
- `bootstrap` command implementation

The command should:

- create or verify the S3 backend bucket
- enable bucket versioning
- enable server-side encryption
- block public access
- create or verify the DynamoDB fleet table
- print the Pulumi backend URL

## Requirements

- Use AWS SDK for Go for bootstrap because the Pulumi S3 backend bucket must exist before Pulumi can use it.
- Do not store AWS credentials in repo files.
- DynamoDB table key is `desktop_id` string.
- The command must be idempotent.
- The command must support `--json`.

## Acceptance Criteria

- Unit tests cover backend config rendering and table schema construction.
- Command can run safely when resources already exist.
- Failure messages identify which resource failed.

## Verification

```bash
go test ./internal/backend ./internal/awsx
go test ./...
```

If AWS credentials are available:

```bash
go run ./cmd/ai-desktops bootstrap --env dev
```
