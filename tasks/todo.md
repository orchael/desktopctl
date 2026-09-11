# Codex auth lifecycle and reusable AWS E2E

Authorized scope: implement the reviewed auth fixes in branches in ai-desktops and bridgectl, test on a dedicated workspace/desktop for this repository, and clean up successful test resources unless retention was requested.

Requirements: PRD AUTH-1 through AUTH-4 and E2E-1 through E2E-2.

## Approved review follow-up

### Blank Codex seed preflight (AUTH-4c)

Mode: approval-required; user approved rejecting a present blank seed while preserving absent-key support. Scope: validate the merged replacement snapshot, without changing secret precedence, null-value handling, or systemd configuration. Risk: operators currently blanking a seed must remove that key instead. Rollback: revert this validation guard and its tests; do not restore old credentials or alter remote secrets.

- [x] Reproduce empty/whitespace seeds with another nonempty secret/API key; assert failure preserves the full runtime snapshot and makes no service calls. Cover absent-seed API-key rotation positively.
- [x] Implement the minimum guard, update operator docs, and run targeted/full Go tests, race/coverage, build, lint, and Python regressions.
- [ ] Push, reply/resolve the three reviewed threads, check CI, and request one fresh Copilot review. Blank seed: score 2 after approval (`PRRT_kwDOSYxwmc6hpEJE`); Python 3.8 compatibility and repeated systemd directives: score 0 (`PRRT_kwDOSYxwmc6hpEIw`, `PRRT_kwDOSYxwmc6hpEJY`).

### Snapshot and shell-mode follow-up (AUTH-4b)

Mode: autonomous, localized correctness fixes within the ongoing review cycle. Fix desktop slice aliasing consistently and preserve existing safe `.bashrc` modes; keep all credential files private. The second bashrc comment is a duplicate; the anonymous strings-only JSON request cannot fail to marshal, so no error-path redesign is needed. Rollback: revert these localized fixes without changing credential sources.

- [x] Reproduce caller slice aliasing across Create/Get/List/Update and `.bashrc` 0644-to-0600 replacement through failing regression tests.
- [x] Implement isolated snapshots and preserved shell mode; run full Go/race tests, focused lint, and existing Python regressions.
- [x] Push, reply/resolve all four comments with evidence, verify CI, and request the next bounded review. All seven checks passed on `7395a72`; the next three findings are tracked above.

### Credential-output preflight (AUTH-4a)

User approved fixing review comment discussion_r3992199828. Scope: output-path validation only; leave `rand.Text()` and unrelated slice cloning unchanged. Preserve safe `0755` ancestors, fail on unsafe ownership/write access, symlinks, or shared mounts; no automatic chmod. Rollback is reverting this guard, not restoring old credentials.

- [x] Reproduce output-directory/symlink/mount failures and prove preflight preserves the entire snapshot; test safe readable and missing directories.
- [x] Validate all output paths before any credential read/staging or service change, then run focused/full tests and lint.
- [x] Push the fix, reply/resolve its review thread, and verify CI. All seven checks passed on `47a12c9`; follow-up review arrived with the findings tracked above.

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

- AUTH-4c: empty and space/tab-only seeds reproduced destructive success before the fix; multiline whitespace was already rejected during normalization. All three preservation regressions and absent-seed API-key rotation now pass. `go test ./cmd/ai-desktops/cmd -run TestSecrets -count=1`, `go test ./...`, full Go race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python regressions passed. Whole-repository Go coverage remains 44.2% (pre-existing gap; no gate lowered). Tests use dummy secrets and local service stubs; no cloud resources or real credentials were changed.

- AUTH-4b: regression tests reproduced aliasing in Create/Get/List/Update and 0644 shell-mode loss before the fixes. All five snapshot-boundary cases and all 31 output-preflight scenarios now pass. Full Go tests, full race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python regressions passed. Store coverage is 81.8%; whole-repository coverage remains 44.2% (the pre-existing broader coverage gap is unchanged).

- AUTH-4a: all 30 output-preflight scenarios pass, alongside existing auth-preservation/coordinator regressions. Before the fix, 26 of the initial 27 scenarios failed (unsafe paths accepted and missing parent directories not private). Full Go tests, full race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python E2E regressions pass. No live cloud operation or credential change was needed for this regression fix.

- PR #223 approved follow-up: full `go test ./...` and full race/coverage suite pass; focused lint reports 0 issues; all 7 Python E2E regressions pass. Scoped internal coverage is 76.5%, store and offline E2E coverage are both 82.0%. Whole-repository coverage is 44.2% (up from 42.9%, still below the global 75% guideline; no gate lowered). Offline tests cover all 19 preflight-preservation cases, cross-process lock overlap/death, explicit recovery, metadata failure, and stale ordinary fleet updates. Live AWS account-auth happy path remains blocked as recorded below.

- ai-desktops: full `go test ./...` and `go test -race ./...` passed; focused golangci-lint passed. Runtime reload tests cover all environment surfaces, partial/failed fetch preservation, private permissions, cache invalidation, and preservation of unrelated manual login state.
- bridgectl: companion branch `fix/codex-auth-lifecycle`, commits `e3ed1f7` and `c87e32b`; full race suite passed, provider coverage 81.2%. Follow-up provider regressions passed after removing credential environment entries entirely.
- E2E harness: offline Go tests passed with 81.2% coverage; two Python subprocess regressions passed, including transient-401 recovery. Keep/reuse/cleanup and partial-provision resume are covered offline.
- Live AWS run: workspace `e2e-ai-desktops-b0c78d2b7d8b2ccb`, desktop `d-b27e224e`, state `/tmp/ai-desktops-e2e-3333895904/state.json`. Provisioning, EFS mount, repository clone, service readiness, and branch binary installation succeeded. The first Codex account request returned a terminal authentication failure, including after fixing the test to permit transient-401 recovery. `codex login status` reports ChatGPT; the account cache retains the uploaded `2026-08-27` refresh timestamp. Resources remain available for `--reuse`; no successful live reload/cleanup assertion is claimed.
- Isolation check: a direct native Codex request with the same auth file and all API/seed environment overrides removed also exited 1 with HTTP 401, refresh failure, and the ChatGPT backend (not the API backend). The remaining live blocker is the current account credential snapshot; the check did not print tokens or replace the shared AWS secret.
