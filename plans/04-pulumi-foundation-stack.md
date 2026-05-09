# Task 04 - Pulumi Foundation Stack

## Objective

Create the Pulumi foundation program for shared AWS resources.

## Depends On

- Task 03

## Scope

Create:

- `infra/pulumi/foundation/Pulumi.yaml`
- `infra/pulumi/foundation/main.go`
- Go types for foundation stack config
- CLI integration for `init-foundation`

Foundation stack owns:

- VPC or existing VPC reference
- public subnet selection
- common security group baseline
- IAM role and instance profile for desktop hosts
- SSM permissions needed for Session Manager tunnel support
- Route53 hosted zone lookup for `desktops.orchael.com` or `desktops.orchael.dev`
- SSM/Secrets Manager namespace references
- DynamoDB fleet table if not already created by bootstrap

## Requirements

- Use Pulumi Go.
- Use Pulumi Automation API from the CLI.
- Fail clearly if the selected Route53 hosted zone does not exist.
- Export subnet IDs, security group IDs, instance profile name, hosted zone ID, and fleet table name.
- Do not create per-desktop resources in the foundation stack.

## Acceptance Criteria

- `init-foundation` can preview the stack.
- Stack outputs are parseable by the CLI.
- Unit tests cover stack config validation.
- Docs mention that hosted zones must exist before running foundation init.

## Verification

```bash
go test ./...
go run ./cmd/ai-desktops init-foundation --env dev --preview
```
