# Task: Customer worker dependency security

## Context
User requested a reviewed Console release candidate for Biokeytic DEV.

## Scope
Update vulnerable dependencies in all three separate Pulumi Go modules. Keep the CLI root module and application behavior unchanged. Add CI compilation/tests for these modules, since root `go test ./...` does not cover nested modules.

## Acceptance Criteria
Patched SSH crypto, Git transport/storage, compression and gRPC versions in the worker source; module test results recorded in the PR.

## Test Strategy
Run each Pulumi module's `go test ./...`, root tests, and CI workflow syntax validation. Dependency updates do not change provisioning contracts.

## Rollback Strategy
Revert the dependency/CI commit and restore the previously pinned Console worker source revision; retain customer state and encryption keys.

## Outcome
Root Go tests and all three nested Pulumi module checks passed locally. The desktop module currently has no test files, so its check compiles the program. CI now checks each module explicitly.
