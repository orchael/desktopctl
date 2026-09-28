# Default root disk size: 100 GiB

Mode: user-authorized configuration change. Governing requirements: FR-7.5 and AC-7.5.

- [x] Set normal desktop defaults to 100 GiB in CLI configuration and the Pulumi fallback; preserve mobile/AVD defaults and explicit overrides.
- [x] Update the example configuration, requirements, and regression expectations.
- [x] Update the local CLI's explicit `desktop.volume_size` override from 64 to 100 GiB.
- [x] Verify the default regression fails at 64 GiB, then passes after the change.

Validation: `go test ./internal/config ./internal/pulumi ./cmd/ai-desktops/cmd -cover` passed (88.4%, 70.2%, and 25.6% respectively); `go test ./...` in `infra/pulumi/desktop` compiled successfully (no test files); `git diff --check` passed. Existing desktops are not resized. Rollback: restore the previous defaults and local configuration value; newly provisioned volumes retain their chosen size.

# Desktop terminal workflow and doctor lifecycle reliability (#225, #146, #149)

Mode: approval-required; the user explicitly approved implementing the three selected issues. Governing requirements: FR-3.4 through FR-3.6, AC-3.6 through AC-3.9, FR-9.9, and AC-9.7 through AC-9.11. Scope: configure the approved terminal workflow in the pre-baked AMI only, add a persistent secret-free cloud-init lifecycle artifact, and make doctor interpret bootstrap and optional CloudWatch state with actionable diagnostics. Preserve existing user-managed terminal configuration and the fallback cloud-init package path. Rollout: build and activate a new AMI, launch a dedicated validation desktop, then verify the live terminal, bootstrap, and CloudWatch paths. Rollback: reactivate the previous AMI and revert the doctor/lifecycle changes; existing lifecycle artifacts are harmless and may remain.

- [x] Add failing static/unit/integration tests for pinned terminal configuration, lifecycle states, warning semantics, and CloudWatch diagnostics.
- [x] Add a reusable, pinned, idempotent Packer Ansible role for NvChad, tmux/TPM plugins, gitmux, and the GitHub editor.
- [x] Extend post-boot and integration verification for ownership and non-interactive Neovim/tmux startup.
- [x] Persist bootstrap lifecycle state from all cloud-init terminal paths and interpret it in doctor.
- [x] Report inactive optional CloudWatch monitoring as a diagnostic warning and document troubleshooting.
- [x] Run focused tests, full Go/race/coverage validation, Ansible/Packer checks, and diff hygiene.
- [x] Build and activate a new AMI, launch a dedicated desktop, and collect live evidence for #225, #146, and #149.
- [x] Prepare an isolated commit and PR handoff that closes all three issues and enters the bounded Copilot/CI cycle.

### PR #240 Copilot cycle 1

- [x] Score 2: preserve SSH exit 255 as a hard failure for the optional CloudWatch diagnostic and distinguish a missing lifecycle artifact from remote read errors.
- [x] Score 2: shell-quote the tmux pane path, write managed markers before terminal configuration mutations, start lifecycle observation before package modules, and gate postboot verification on the corresponding managed markers.
- [x] Re-run full Go/race/coverage, lint, Ansible syntax/role lint, Packer format/syntax, pre-commit, and a malicious-directory tmux smoke check.

### PR #240 Copilot cycle 2

- [x] Score 2: keep SSH context timeouts as hard failures and reject terminal lifecycle artifacts missing required timestamps or a nonzero failure code.
- [x] Score 2: separate image capability, Neovim, and tmux markers so editor and ownership checks run only for managed state; wait for TPM and verify its binding and gitmux status before integration teardown.

### PR #240 Copilot cycle 3

- [x] Score 2: reject contradictory succeeded lifecycle artifacts with nonzero exit codes.
- [x] Score 2: preserve dangling user-managed Neovim and tmux symlinks by excluding links before treating a path as absent.
- [x] End the bounded Copilot loop after this third cycle; no fourth review request.

Validation: the new focused tests failed against the prior behavior, then passed after implementation. `go test ./...`, `go test -race ./...`, internal-package coverage (76.0%), integration compile-only validation, `golangci-lint run ./...`, the production-profile terminal-role lint, Ansible syntax, Packer formatting/syntax, a locked NvChad restore/headless launch, and isolated tmux startup all pass. Full desktop-setup lint retains 30 pre-existing findings outside the new clean role/tasks. Live Packer validation exposed and fixed gitmux output, GitHub CLI ordering, and current Homebrew tap-trust ordering; the final bake completed 187 tasks with zero failures and activated `ami-00c40d089d33d9b7c`. Disposable desktop `d-a0cc4a75` recorded bootstrap `running` at 21:52:07Z and atomically transitioned to `succeeded` at 21:53:14Z; the terminal workflow passed live, inactive CloudWatch produced a warning with actionable systemd fields, and the restored agent returned active. The disposable instance, DNS record, root disk, and stack resources were then deleted. Existing desktop timing showed the reported CloudWatch failure came from a roughly three-minute startup window rather than a persistent agent fault.

# Upgrade the repository to Go 1.26

Mode: approval-required; the user explicitly requested this repository-wide toolchain and CI update. Governing requirement: FR-5.6/AC-5.8. Scope: update all first-party Go modules, the control-plane and AMI builder versions, developer prerequisites, and verify that GitHub Actions continues to derive Go from the root module. Do not update historical review evidence or unrelated example snippets in generated agent-rule files. Rollout: merge the PR so CI, release builds, and subsequent container/AMI builds adopt Go 1.26. Rollback: revert the version-only commits to restore the prior Go pins.

- [x] Update the PRD before implementation.
- [x] Update every authoritative Go version reference to Go 1.26.
- [x] Run module, build, test, coverage, lint, and static version validation.
- [x] Create the PR, request Copilot, monitor CI, and resolve review feedback.

### PR #227 Copilot cycle 1

- [x] Score 2: update the Packer AMI Go pin and its active documentation to 1.26.0; validate with Go tests, Packer formatting/syntax checks, and Ansible syntax check.
- [x] Score 1: rename the new toolchain criterion to the unused AC-5.8 identifier.

### PR #227 Copilot cycle 2

- [x] Score 1: add an automated AC-5.8 test covering all Go modules, GitHub Actions version derivation, the control-plane builder, and Packer pin propagation.
- [x] Score 2: replace any preinstalled Go tree during AMI provisioning and fail the build unless the installed compiler exactly matches the configured version.

### PR #227 Copilot cycle 3

- [x] Copilot recommended approval with no remaining code issues; final CI passed all seven jobs.

# Harden agent-auth rotation and Codex-home provisioning

Mode: approval-required; the user explicitly requested credential workflow, AMI, and cloud-init changes after the live `d-effdf24d` rotation exposed the gaps. Governing requirements: FR-9.8/AC-9.6 and AUTH-2a/AUTH-4d. Scope: fail closed on ambiguous AWS errors, add an optional update-and-reload path, propagate only allowlisted reload failure categories, and pre-create the native Codex home privately in both image and fallback provisioning. Preserve the strict reload validator and all unrelated branch edits. Rollout: bake and activate a new AMI for future desktops; cloud-init covers non-prebaked/fallback creates. Existing desktops require an explicit permission correction before reload. Rollback: restore the previous script/coordinator behavior and prior AMI, without rolling credentials back or weakening existing path validation.

- [x] Update the governing PRD before implementation.
- [x] Add failing tests for AWS read failures, optional reload propagation, safe coordinator errors, and both provisioning paths.
- [x] Implement the minimum script, coordinator, AMI, cloud-init, doctor, and documentation changes.
- [x] Run focused tests, full Go/race/coverage validation, script regressions, Ansible/Packer checks, and diff hygiene.
- [x] Build and activate the updated AMI if the live build prerequisites are available; otherwise record the exact blocker and rollout command.

Validation: all new focused regressions failed against the prior behavior, then passed after implementation. `go test ./...`, `go test -race ./...`, scoped internal coverage (76.4%), CLI build, `golangci-lint run ./...` (0 issues), Bash syntax, focused ShellCheck, Ansible syntax, Packer formatting/syntax, and `git diff --check` pass. Full `ansible-lint packer/playbook.yml` retains the same 26 pre-existing failures and one task-count warning; neither the private Codex-home task nor the existing Helm tasks add a violation. The live us-east-2 Packer build completed with `failed=0`, executed the private Codex-home task, verified Helm v4.3.0, created `ami-012e6494e8afc836b` backed by `snap-0bf69c6c4cc227080`, activated it in operator config, and cleaned up its temporary instance, keypair, security group, and volumes. Build log: `packer/build-logs/packer-us-east-2-20260912-043057.log`.

### PR #224 Copilot cycle 1

- [x] Reject symlinked native Codex homes in both Go health checks and the AMI doctor.
- [x] Pin Helm `v4.3.0`, propagate the pin through Packer/Ansible metadata, and fail provisioning on version drift.
- [x] Skip Helm AMI validation for adopted integration fixtures.
- [x] Reject whitespace-only existing `SecretString` values and recognize not-found only from the structured AWS `DescribeSecret` error prefix.
- [x] Run full local validation for cycle 1.
- [x] Push, reply to and resolve all cycle-1 threads, and check CI.

Cycle-1 validation: the five focused regressions failed against commit `6d62125` for the expected reasons, then passed after the fixes. `go test ./...`, `go test -race ./...`, `golangci-lint run ./...` (0 issues), ShellCheck, all seven Python regressions, Ansible syntax, Packer formatting/syntax, integration-suite compilation, and `git diff --check` pass. Full `ansible-lint packer/playbook.yml` retains the same 26 pre-existing failures and one task-count warning, with no finding on the changed tasks. The previously built AMI already verified Helm v4.3.0; no additional cloud build was needed for the validation-only pin and review corrections.

### PR #224 Copilot cycle 2

- [x] Make fallback cloud-init reject a symlinked native Codex home and exit before later provisioning commands.
- [x] Carry an explicit `secret_retrieval` exit category without printing or classifying on an untrusted secret path.
- [x] Compare the live AMI's Helm version to the configured pin and correct stale FR-9 acceptance references.
- [x] Update both doctor guides and the README diagnostic summary for the private Codex-home check.
- [x] Run full local validation for cycle 2.
- [x] Push, reply to and resolve the cycle-2 thread, and check CI.

Cycle-2 validation: the cloud-init symlink/fatality regression and secret-path diagnostic regression failed first for the intended reasons, then passed. `go test ./...`, `go test -race ./...`, `golangci-lint run ./...` (0 issues), ShellCheck, all seven Python regressions, Ansible syntax, Packer formatting/syntax, integration-suite compilation, and `git diff --check` pass.

### PR #224 Copilot cycle 3

- [x] Require `update-agent-auth.sh --reload-desktop` to validate the exact updated secret path and resolved AWS region against the desktop before reload.
- [x] Carry every known reload failure as a typed category and leave unexpected exceptions uncategorized.
- [x] Make fallback cloud-init exit explicitly when native Codex-home creation fails.
- [x] Correct the doctor help and FR-9 acceptance-test mapping.
- [x] Run full local validation, push, reply to and resolve cycle-3 threads, and verify CI.

Cycle-3 validation: the exact-target, unknown-error classification, and fatal Codex-home provisioning regressions failed against `3328072`, then passed after implementation. `go test ./...`, `go test -race ./...`, scoped internal coverage (76.4%), CLI build, `golangci-lint run ./...` (0 issues), ShellCheck, all seven Python regressions, Ansible syntax, Packer formatting/syntax, integration-suite binary compilation, and `git diff --check` pass. Full `ansible-lint packer/playbook.yml` retains the same 26 pre-existing failures and one task-count warning, with no finding on the changed cloud-init or credential files.

# Install Helm in the AMI with Homebrew

Mode: approval-required; the user explicitly requested this AMI/runtime change. Governing requirement: FR-9.5 and AC-9.5. Scope: install Helm through the existing Linuxbrew installation during Packer provisioning, verify it in the playbook and AMI integration suite, and document the toolchain. Do not build an AMI or alter existing desktops. Rollout: rebuild and activate an AMI containing this change. Rollback: reactivate the prior AMI or revert the Helm provisioning tasks and rebuild.

- [x] Update FR-9.5 and add AC-9.5 before implementation.
- [x] Add a failing regression for Linuxbrew-based Helm installation and verification.
- [x] Implement the minimum playbook, integration-test, and operator-doc changes.
- [x] Run focused tests, Go tests, Ansible lint, and Packer syntax validation; record evidence.

Validation: `TestAMIPlaybookInstallsHelmWithHomebrew` failed first because both Helm tasks were absent, then passed after implementation. `go test ./...`, integration-suite compilation with the `integration` build tag, Ansible syntax check, Packer format check, Packer syntax-only validation, and `git diff --check` pass. The live `TestFR9_HelmInstalled` requires a built AMI and SSH fixture and was not executed. `ansible-lint packer/playbook.yml` reports 26 existing failures and one existing task-count warning at unchanged lines; it reports no violation on either new Helm task. No AMI was built and no running desktop was changed.

# Codex auth lifecycle and reusable AWS E2E

Authorized scope: implement the reviewed auth fixes in branches in ai-desktops and bridgectl, test on a dedicated workspace/desktop for this repository, and clean up successful test resources unless retention was requested.

Requirements: PRD AUTH-1 through AUTH-4 and E2E-1 through E2E-2.

## Approved review follow-up

### Release pin and retained fixture cleanup (AUTH-5, E2E-2)

Mode: approval-required; user explicitly approved deleting desktop `d-b27e224e` and workspace `e2e-ai-desktops-b0c78d2b7d8b2ccb`, then pinning `bridgectl` to `v1.1.1` and pushing. Use the saved manifest and identity-validating cleanup runner; leave underlying EFS directory contents intact. Update the CLI/Packer pins and current build documentation, without changing branch-binary E2E overrides. Rollout requires a matching rebuilt AMI; no image build or existing-fleet upgrade is authorized here. Rollback: revert both pins together and use the matching prior image; deleted compute/access points cannot be restored by reverting code.

- [x] Clean up the exact recorded desktop/workspace and verify the manifest and live state. EC2 `i-0b1919f534f344fc6` is terminated; workspace state is `deleted` (audit record retained); AWS confirms access point `fsap-05c1e35a55cfa07b6` no longer exists. Manifest records both cleanup flags true. Underlying EFS directory contents were not erased.
- [x] Add a failing regression for release `v1.1.1` and CLI/Packer pin consistency, then update both pins and docs.
- [ ] Run targeted/full tests, race/coverage, build, lint and Packer validation; push and check CI/Copilot status.

### Blank Codex seed preflight (AUTH-4c)

Mode: approval-required; user approved rejecting a present blank seed while preserving absent-key support. Scope: validate the merged replacement snapshot, without changing secret precedence, null-value handling, or systemd configuration. Risk: operators currently blanking a seed must remove that key instead. Rollback: revert this validation guard and its tests; do not restore old credentials or alter remote secrets.

- [x] Reproduce empty/whitespace seeds with another nonempty secret/API key; assert failure preserves the full runtime snapshot and makes no service calls. Cover absent-seed API-key rotation positively.
- [x] Implement the minimum guard, update operator docs, and run targeted/full Go tests, race/coverage, build, lint, and Python regressions.
- [x] Push, reply/resolve the three reviewed threads, check CI, and request one fresh Copilot review. All seven checks passed on `8a2271d`. Blank seed: score 2 after approval (`PRRT_kwDOSYxwmc6hpEJE`); Python 3.8 compatibility and repeated systemd directives: score 0 (`PRRT_kwDOSYxwmc6hpEIw`, `PRRT_kwDOSYxwmc6hpEJY`).

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

- AUTH-5 / explicit E2E cleanup: the new release-pin test failed against both `v1.0.1` pins and both cloud-init modes before the update; the full provisioning tests now pass with `v1.1.1`. Full Go race/coverage suite, CLI build, focused lint (0 issues), all 7 Python regressions, Packer formatting and syntax-only validation passed. Provision coverage remains 83.8%; the existing whole-repository coverage gap is unchanged. The identity-validating cleanup command succeeded and AWS confirmed compute termination/access-point deletion. No new AMI was built and no running desktop was upgraded.

- AUTH-4c: empty and space/tab-only seeds reproduced destructive success before the fix; multiline whitespace was already rejected during normalization. All three preservation regressions and absent-seed API-key rotation now pass. `go test ./cmd/ai-desktops/cmd -run TestSecrets -count=1`, `go test ./...`, full Go race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python regressions passed. Whole-repository Go coverage remains 44.2% (pre-existing gap; no gate lowered). Tests use dummy secrets and local service stubs; no cloud resources or real credentials were changed.

- AUTH-4b: regression tests reproduced aliasing in Create/Get/List/Update and 0644 shell-mode loss before the fixes. All five snapshot-boundary cases and all 31 output-preflight scenarios now pass. Full Go tests, full race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python regressions passed. Store coverage is 81.8%; whole-repository coverage remains 44.2% (the pre-existing broader coverage gap is unchanged).

- AUTH-4a: all 30 output-preflight scenarios pass, alongside existing auth-preservation/coordinator regressions. Before the fix, 26 of the initial 27 scenarios failed (unsafe paths accepted and missing parent directories not private). Full Go tests, full race/coverage suite, CLI build, focused lint (0 issues), and all 7 Python E2E regressions pass. No live cloud operation or credential change was needed for this regression fix.

- PR #223 approved follow-up: full `go test ./...` and full race/coverage suite pass; focused lint reports 0 issues; all 7 Python E2E regressions pass. Scoped internal coverage is 76.5%, store and offline E2E coverage are both 82.0%. Whole-repository coverage is 44.2% (up from 42.9%, still below the global 75% guideline; no gate lowered). Offline tests cover all 19 preflight-preservation cases, cross-process lock overlap/death, explicit recovery, metadata failure, and stale ordinary fleet updates. Live AWS account-auth happy path remains blocked as recorded below.

- ai-desktops: full `go test ./...` and `go test -race ./...` passed; focused golangci-lint passed. Runtime reload tests cover all environment surfaces, partial/failed fetch preservation, private permissions, cache invalidation, and preservation of unrelated manual login state.
- bridgectl: companion branch `fix/codex-auth-lifecycle`, commits `e3ed1f7` and `c87e32b`; full race suite passed, provider coverage 81.2%. Follow-up provider regressions passed after removing credential environment entries entirely.
- E2E harness: offline Go tests passed with 81.2% coverage; two Python subprocess regressions passed, including transient-401 recovery. Keep/reuse/cleanup and partial-provision resume are covered offline.
- Live AWS run: workspace `e2e-ai-desktops-b0c78d2b7d8b2ccb`, desktop `d-b27e224e`, state `/tmp/ai-desktops-e2e-3333895904/state.json`. Provisioning, EFS mount, repository clone, service readiness, and branch binary installation succeeded. The first Codex account request returned a terminal authentication failure, including after fixing the test to permit transient-401 recovery. At failure, `codex login status` reported ChatGPT and the account cache retained the uploaded `2026-08-27` refresh timestamp. On explicit operator request, the desktop and workspace access point were cleaned up on 2026-09-11 at 23:55 UTC; they are no longer reusable. The live authenticated reload and automatic success-cleanup path still need a fresh run; explicit cleanup has now been verified.
- Isolation check: a direct native Codex request with the same auth file and all API/seed environment overrides removed also exited 1 with HTTP 401, refresh failure, and the ChatGPT backend (not the API backend). The remaining live blocker is the current account credential snapshot; the check did not print tokens or replace the shared AWS secret.
# Isolate the agent secret from desktop-wide environment variables

Mode: approval-required; the user explicitly requested the secret-boundary correction after desktop `d-36c62e9b` exposed an overlapping `OPENAI_API_KEY`. Governing requirement: AUTH-2b. Scope: keep the configured agent secret tracked for rotation but write it only to `agents.env`; write additional desktop secrets only to the desktop environment and shell files during create and reload. Preserve transactional reload, credential-cache invalidation, secret-safe diagnostics, and show the two scopes separately in human-readable status. Rollout: install the updated CLI, reload the affected desktop, and use a newly rendered cloud-init configuration for future desktops. Rollback: restore the prior CLI/cloud-init behavior and explicitly reload; no AWS secret values are changed by this code change.

- [x] Add failing regressions for create-time source separation and reload-time output separation with an overlapping key.
- [x] Implement typed agent and desktop secret inputs without changing fleet persistence.
- [x] Split human-readable status into `Agent secret` and desktop-wide `Secrets` lines.
- [x] Make doctor omit desktop-wide secret-file checks for agent-only desktops after Copilot cycle 1.
- [x] Add create-to-cloud-init source-boundary coverage after Copilot cycle 2.
- [x] Preserve legacy untracked `agents.env` during application-secret operations after Copilot cycle 3.
- [x] Run focused and full tests, coverage/lint where practical, and diff hygiene.

Validation: the metadata/source-separation regression failed to compile against the prior single-list reload API, then passed after implementation. Dummy runtime tests prove an overlapping `OPENAI_API_KEY` remains agent-scoped in `agents.env` and desktop-scoped in both desktop environment files. Status regressions prove the tracked agent path is shown separately and an untracked configured path is not falsely reported. The cycle-1 doctor regression failed against the combined health-check input, then passed for agent-only, mixed, and untracked-agent desktops after filtering. Focused tests and race tests, the full Go suite, CLI build, command-package coverage (25.5%, with `cmd/` excluded from the repository's configured threshold), and `git diff --check` pass. Repository-wide lint reports one unrelated finding on the base branch at `internal/health/health.go:46`; no changed secret or status file is reported.

### PR #239 Copilot cycle 2

- [x] Score 2: route create-time secret scope through one bootstrap helper and render cloud-init with both a tracked agent path and an additional desktop path, proving neither source appears in the other's retrieval block. The existing reload runtime regression covers overlapping key values.

### PR #239 Copilot cycle 3

- [x] Score 2: carry explicit agent-surface replacement intent so legacy application-secret reload/add/remove operations preserve an untracked `agents.env`, while tracked agent rotation and removal still replace or clear it. Validate existing, missing, and unsafe output surfaces plus focused race and full-suite coverage.
# Agent profile provisioning repair

Mode: user-authorized runtime and configuration change. Governing requirements: PRD AP-1 through AP-4. Root cause: the documentation branch defined `agent.profile` but did not add a config field, CLI flag, or cloud-init installer. The configured plural repository name also differs from the existing singular private repository. Scope: implement validated selection and fail-closed provisioning, correct docs/operator config, then apply the reviewed profile to desktop `d-80bda9fc` and verify its user files. Risks: executing a profile installer and altering user-level agent configuration; review the exact repository and script before execution. Rollback: restore the previous user config files on the desktop and remove the selected profile from operator config; new provisioning can be rolled back by reverting this branch.

- [x] Confirm repository access and review the selected install script. The existing private repo is `markcallen/ai-desktop-profile` at revision `d21e264`; the desktop's GitHub SSH key can read it.
- [x] Add failing tests for reference validation, default/flag selection, and cloud-init order/failure behavior.
- [x] Implement profile loading and installation during create, with no-profile compatibility.
- [x] Correct the operator profile reference and update documentation.
- [x] Run targeted/full tests and coverage, then validate the live desktop profile files and CLI behavior. Full root Go suite and all three infra modules passed; adjusted internal coverage is 76.8%. The existing desktop has matching Codex/Claude files, Pilot registered for both agents, and completed Claude onboarding.
- [x] Update PR #260 at `68d9ea4`, request Copilot review, and check CI. All seven checks passed; no Copilot comments were present after the request. The PR title and body now describe the implemented behavior.
- [x] Resolve Copilot review findings: wait for successful cloud-init before marking a profiled desktop ready, and run nested profile installers from their own directory. The full Go suite passed. Fresh desktop `d-23b41782` remained pending during cloud-init, then created successfully; SSM verified matching Codex/Claude files, mode 0600, and Pilot registration. Finish cleanup, push, CI, and Copilot follow-up before closing PR #260.
