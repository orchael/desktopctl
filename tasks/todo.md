# Codex auth lifecycle and reusable AWS E2E

Authorized scope: implement the reviewed auth fixes in branches in ai-desktops and bridgectl, test on a dedicated workspace/desktop for this repository, and clean up successful test resources unless retention was requested.

Requirements: PRD AUTH-1 through AUTH-4 and E2E-1 through E2E-2.

## Approved review follow-up

### Credential-output preflight (AUTH-4a)

User approved fixing review comment discussion_r3992199828. Scope: output-path validation only; leave `rand.Text()` and unrelated slice cloning unchanged. Preserve safe `0755` ancestors, fail on unsafe ownership/write access, symlinks, or shared mounts; no automatic chmod. Rollback is reverting this guard, not restoring old credentials.

- [x] Reproduce output-directory/symlink/mount failures and prove preflight preserves the entire snapshot; test safe readable and missing directories.
- [x] Validate all output paths before any credential read/staging or service change, then run focused/full tests and lint.
- [ ] Push the fix, reply/resolve its review thread, and verify CI. Track follow-up review status on the PR.

Mode: approval-required; operator approved fail-fast cross-machine secret coordination. Keep changes on PR #223.

- [x] Add a desktop-held nonblocking lock spanning remote rotation and fleet commit, with a fenced metadata update and explicit reload recovery after interruption. Test overlap, interrupted clients, stale commits, and path-list preservation offline.
- [x] Preflight cache ownership/modes/types, account seed structure, and systemd unit load state before mutation; prove failures preserve files and sessions.
- [x] Check file-level NFS mounts, keep provider arguments out of E2E artifacts, and persist cleanup failures accurately; add regression tests.
- [ ] Run relevant/full tests, push, reply/resolve review threads, check CI, and complete the bounded Copilot cycle.

Risk/rollout: upgrade all CLI and control-plane fleet writers; older versions bypass coordination and metadata guards. Remote locks are kernel-held, not time-expiring leases. A private pending marker survives interrupted writes; reload reconciles against a fresh fenced fleet snapshot. Ordinary stale fleet updates fail safely if a secret operation changed their snapshot. Rollback must not delete pending markers or restore old credentials.

- [x] Fix bridgectl credential precedence, persistent seeding, explicit-home isolation, and effective-environment health checks with regression tests.
- [x] Unify desktop agent-secret reload and invalidate managed auth during explicit rotation; verify retrieval failure preserves existing credentials.
- [x] Add extensible E2E lifecycle/scenario runner, retention and reuse, and deterministic lifecycle tests.
- [x] Build both branches and run focused and full relevant tests.
- [x] Run AWS E2E against the branch binaries and record the failure with resources retained.
- [ ] Complete the live authenticated happy path and automatic cleanup after usable account credentials are available.
- [x] Open companion PRs with Copilot requested: ai-desktops #223 and orchael/bridgectl #219.
- [x] Reproduce and fix Copilot's inline tilde-path and legacy secret-precedence findings; also invalidate overridden auth homes from every environment surface and report preserved inactive services explicitly.
- [x] Harden E2E recovery after partial workspace/desktop creation, reject mismatched SSH keys before provisioning, and validate private non-EFS auth storage. Full Go race tests and five Python regressions pass after review fixes.

Tradeoffs: account refresh state belongs on each desktop root disk, not EFS or the original AWS secret. Credential reload deliberately interrupts old provider processes; no expired credential values are logged. Failed E2E resources remain inspectable and incur AWS cost until cleanup.

Rollback: revert branch changes and reinstall the previous bridge binary; recreate an E2E desktop using the previous CLI if bootstrap behavior needs comparison. Never roll credentials back to an older snapshot during rollback.

## Validation

- AUTH-4a: all 30 output-preflight scenarios pass, alongside existing auth-preservation/coordinator regressions. Before the fix, 26 of the initial 27 scenarios failed (unsafe paths accepted and missing parent directories not private). Full Go tests, full race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python E2E regressions pass. No live cloud operation or credential change was needed for this regression fix.

- PR #223 approved follow-up: full `go test ./...` and full race/coverage suite pass; focused lint reports 0 issues; all 7 Python E2E regressions pass. Scoped internal coverage is 76.5%, store and offline E2E coverage are both 82.0%. Whole-repository coverage is 44.2% (up from 42.9%, still below the global 75% guideline; no gate lowered). Offline tests cover all 19 preflight-preservation cases, cross-process lock overlap/death, explicit recovery, metadata failure, and stale ordinary fleet updates. Live AWS account-auth happy path remains blocked as recorded below.

- ai-desktops: full `go test ./...` and `go test -race ./...` passed; focused golangci-lint passed. Runtime reload tests cover all environment surfaces, partial/failed fetch preservation, private permissions, cache invalidation, and preservation of unrelated manual login state.
- bridgectl: companion branch `fix/codex-auth-lifecycle`, commits `e3ed1f7` and `c87e32b`; full race suite passed, provider coverage 81.2%. Follow-up provider regressions passed after removing credential environment entries entirely.
- E2E harness: offline Go tests passed with 81.2% coverage; two Python subprocess regressions passed, including transient-401 recovery. Keep/reuse/cleanup and partial-provision resume are covered offline.
- Live AWS run: workspace `e2e-ai-desktops-b0c78d2b7d8b2ccb`, desktop `d-b27e224e`, state `/tmp/ai-desktops-e2e-3333895904/state.json`. Provisioning, EFS mount, repository clone, service readiness, and branch binary installation succeeded. The first Codex account request returned a terminal authentication failure, including after fixing the test to permit transient-401 recovery. `codex login status` reports ChatGPT; the account cache retains the uploaded `2026-08-27` refresh timestamp. Resources remain available for `--reuse`; no successful live reload/cleanup assertion is claimed.
- Isolation check: a direct native Codex request with the same auth file and all API/seed environment overrides removed also exited 1 with HTTP 401, refresh failure, and the ChatGPT backend (not the API backend). The remaining live blocker is the current account credential snapshot; the check did not print tokens or replace the shared AWS secret.
