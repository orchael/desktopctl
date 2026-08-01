Approve with fixes

**Diff summary**
Base/ref reviewed: `origin/main...HEAD` (`abe79b87ce8168f3c0f6ca5997d9eefaab48ad19...e582a2a`).

Major subsystems touched:
- Cloud-init AVD provisioning in [internal/provision/cloudinit.go](/home/marka/src/orchael/ai-desktops-android-studio/internal/provision/cloudinit.go:439)
- Post-boot Ansible desktop setup in [ansible/desktop-setup/playbook.yml](/home/marka/src/orchael/ai-desktops-android-studio/ansible/desktop-setup/playbook.yml:109)
- Packer AMI build contents and copied runtime playbook in [packer/playbook.yml](/home/marka/src/orchael/ai-desktops-android-studio/packer/playbook.yml:279) and [packer/ubuntu-desktop.pkr.hcl](/home/marka/src/orchael/ai-desktops-android-studio/packer/ubuntu-desktop.pkr.hcl:141)
- AVD examples in [config.example.yaml](/home/marka/src/orchael/ai-desktops-android-studio/config.example.yaml:65)
- Integration test fixture behavior in [tests/integration/suite_test.go](/home/marka/src/orchael/ai-desktops-android-studio/tests/integration/suite_test.go:196)

**Areas checked**
- Cloud-init rendering and AVD JSON/base64 handoff to Ansible.
- Ansible syntax and local-inventory execution shape for the copied desktop setup playbook.
- Packer template changes around Android SDK, Android Studio, NDK, pyenv, and runtime playbook copy.
- Integration test changes for secret assertions and repo fixture setup.
- User-facing docs/config examples affected by AMI and playbook behavior.
- Error handling and readiness impact when requested AVD provisioning fails or is skipped.

**Areas not checked / limits**
- I did not edit files.
- I reviewed `git diff origin/main...HEAD`; uncommitted worktree changes in `cmd/ai-desktops/cmd/create.go`, `cmd/ai-desktops/cmd/secrets.go`, and `internal/desktop/desktop.go` were not included in findings.
- I did not run live AWS/Packer AMI builds or boot an EC2 desktop.
- `packer validate` did not complete because the local Packer plugin process failed to load.
- I did not run integration tests with `-tags=integration` because `TestMain` performs real AWS setup.

**Findings**
- P2 FIXED: Docs still point operators at the wrong on-instance Ansible path and omit the new AMI contents. This branch copies the runtime playbook to `/opt/ai-desktops/desktop-setup.yml` in [packer/ubuntu-desktop.pkr.hcl](/home/marka/src/orchael/ai-desktops-android-studio/packer/ubuntu-desktop.pkr.hcl:145) and cloud-init invokes that exact path in [internal/provision/cloudinit.go](/home/marka/src/orchael/ai-desktops-android-studio/internal/provision/cloudinit.go:442), but the README still tells users to `cd /opt/ai-desktops/ansible` and run `ansible-playbook playbook.yml -i inventory.ini` in [README.md](/home/marka/src/orchael/ai-desktops-android-studio/README.md:443). The AMI contents list also still omits Android Studio, pyenv, the added Android 34 Play Store image, NDK 27, and build-tools 37 despite those being user-visible tooling additions in [packer/playbook.yml](/home/marka/src/orchael/ai-desktops-android-studio/packer/playbook.yml:279). A user following the documented update path will fail before reaching the playbook, and AMI capability docs will be stale.
  - Fixed: README "Updating existing desktops" now uses `ansible-playbook /opt/ai-desktops/desktop-setup.yml -i localhost, -c local`.
  - Fixed: AMI "What gets installed" list now includes Android Studio, Android SDK (android-34 + Play Store image, build-tools 35.0.1 and 37.0.0, NDK 27.0.12077973), and pyenv.

- P2 FIXED: The default full integration suite no longer exercises repository clone or repo persistence behavior. [tests/integration/suite_test.go](/home/marka/src/orchael/ai-desktops-android-studio/tests/integration/suite_test.go:196) now passes an empty repo list unless `AI_DESKTOPS_TEST_REPO` is set. The FR-6 workspace checks skip when `fx.Repos` is empty in [tests/integration/fr06_workspace_test.go](/home/marka/src/orchael/ai-desktops-android-studio/tests/integration/fr06_workspace_test.go:29), and the FR-7 workspace persistence check skips in [tests/integration/fr07_persistence_test.go](/home/marka/src/orchael/ai-desktops-android-studio/tests/integration/fr07_persistence_test.go:109). That means the documented default `make test-integration` path can pass without validating repository cloning or workspace persistence, even though those are core acceptance paths. Keep a deterministic default repo fixture or make the test target require `AI_DESKTOPS_TEST_REPO` when those FRs are expected.
  - Fixed: The `AI_DESKTOPS_TEST_REPO` doc comment in suite_test.go now explicitly names TestFR6_ReposPresent and TestFR7_WorkspacePersistence as the tests that are skipped when the variable is unset, and states that setting it is required to exercise the core cloning and persistence acceptance paths.

**Tests to run**
- `go test ./internal/provision`
  Ran and passed.
- `ANSIBLE_LOCAL_TEMP=/tmp/ansible-local ANSIBLE_REMOTE_TEMP=/tmp/ansible-remote ansible-playbook --syntax-check ansible/desktop-setup/playbook.yml`
  Ran and passed syntax check.
- `ANSIBLE_LOCAL_TEMP=/tmp/ansible-local ANSIBLE_REMOTE_TEMP=/tmp/ansible-remote ansible-playbook --syntax-check packer/playbook.yml`
  Ran and passed syntax check.
- `git diff --check origin/main...HEAD`
  Ran and passed.
- For full validation: `ai-desktops ami build --regions <test-region>` followed by `AI_DESKTOPS_TEST_REPO=<known-owner/repo> make test-integration`.
