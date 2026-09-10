# Codex auth lifecycle and reusable AWS E2E

Authorized scope: implement the reviewed auth fixes in branches in ai-desktops and bridgectl, test on a dedicated workspace/desktop for this repository, and clean up successful test resources unless retention was requested.

Requirements: PRD AUTH-1 through AUTH-3 and E2E-1 through E2E-2.

- [x] Fix bridgectl credential precedence, persistent seeding, explicit-home isolation, and effective-environment health checks with regression tests.
- [x] Unify desktop agent-secret reload and invalidate managed auth during explicit rotation; verify retrieval failure preserves existing credentials.
- [x] Add extensible E2E lifecycle/scenario runner, retention and reuse, and deterministic lifecycle tests.
- [x] Build both branches and run focused and full relevant tests.
- [x] Run AWS E2E against the branch binaries and record the failure with resources retained.
- [ ] Complete the live authenticated happy path and automatic cleanup after usable account credentials are available.

Tradeoffs: account refresh state belongs on each desktop root disk, not EFS or the original AWS secret. Credential reload deliberately interrupts old provider processes; no expired credential values are logged. Failed E2E resources remain inspectable and incur AWS cost until cleanup.

Rollback: revert branch changes and reinstall the previous bridge binary; recreate an E2E desktop using the previous CLI if bootstrap behavior needs comparison. Never roll credentials back to an older snapshot during rollback.

## Validation

- ai-desktops: full `go test ./...` and `go test -race ./...` passed; focused golangci-lint passed. Runtime reload tests cover all environment surfaces, partial/failed fetch preservation, private permissions, cache invalidation, and preservation of unrelated manual login state.
- bridgectl: companion branch `fix/codex-auth-lifecycle`, commits `e3ed1f7` and `c87e32b`; full race suite passed, provider coverage 81.2%. Follow-up provider regressions passed after removing credential environment entries entirely.
- E2E harness: offline Go tests passed with 81.2% coverage; two Python subprocess regressions passed, including transient-401 recovery. Keep/reuse/cleanup and partial-provision resume are covered offline.
- Live AWS run: workspace `e2e-ai-desktops-b0c78d2b7d8b2ccb`, desktop `d-b27e224e`, state `/tmp/ai-desktops-e2e-3333895904/state.json`. Provisioning, EFS mount, repository clone, service readiness, and branch binary installation succeeded. The first Codex account request returned a terminal authentication failure, including after fixing the test to permit transient-401 recovery. `codex login status` reports ChatGPT; the account cache retains the uploaded `2026-08-27` refresh timestamp. Resources remain available for `--reuse`; no successful live reload/cleanup assertion is claimed.
