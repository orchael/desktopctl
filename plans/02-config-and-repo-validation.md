# Task 02 - Config and Repository Validation

## Objective

Implement local operator config loading and GitHub repository owner validation.

## Depends On

- Task 01

## Scope

Create:

- `internal/config`
- `internal/repo`

Config should support:

- AWS region
- AWS profile
- Pulumi backend bucket
- DynamoDB fleet table name
- environment: `prod` or `dev`
- Route53 zone name
- default desktop profile
- secret references for GitHub PAT and agent provider keys

Repository validation should support:

- `github.com/<owner>/<repo>`
- `https://github.com/<owner>/<repo>`
- `git@github.com:<owner>/<repo>.git`
- optional `.git` suffix

## Requirements

- `prod` maps to `desktops.orchael.com`.
- `dev` maps to `desktops.orchael.dev`.
- A desktop create request must specify exactly one GitHub owner boundary.
- Mixed-owner repository input must be rejected before any infrastructure operation.
- Validation errors must be actionable and include the offending repo.

## Acceptance Criteria

- Unit tests cover supported repo URL formats.
- Unit tests reject mixed GitHub owners.
- Unit tests reject non-GitHub repository inputs.
- Config loading supports defaults plus explicit overrides from flags.

## Verification

```bash
go test ./internal/config ./internal/repo
go test ./...
```
