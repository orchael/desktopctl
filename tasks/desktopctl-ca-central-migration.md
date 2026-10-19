# Desktopctl ca-central-1 migration

## Target

Fresh production setup in account 819363892004, region ca-central-1, with the
desktopctl namespace and XDG config. Do not adopt legacy resources or state.

## Steps

- [x] Create migration branch and inspect configuration, names, provisioning, AMI, and E2E flows.
- [x] Move default config to XDG and preserve explicit `--config` selection.
- [x] Rename first-party paths, tags, and resource names to desktopctl.
- [x] Scope foundation stack and default tables to environment and region.
- [x] Add fresh bootstrap/foundation collision checks and an explicit reuse override.
- [x] Add public AMI discovery and SSH login scenario to opt-in E2E runner.
- [x] Run full local gates and inspect name changes; root Go, Pulumi modules, both web builds/tests, and coverage passed.
- [x] Preview the no-DNS production foundation against temporary local Pulumi state: 31 creates, no updates, imports, or deletes. Remove the temporary stack afterward.
- [x] Copy the noVNC base to ca-central-1 as `ami-0db78418f8538fb8f`; verify it is available.
- [x] Bootstrap fresh S3 state and apply `foundation-prod-ca-central-1`: 31 resources created in account 819363892004, including fleet/AMI tables and EFS.
- [x] Verify SSH ingress is this machine's public IPv4 `/32` (157.230.223.137/32).
- [x] Teach `setup` to detect and interview for this IPv4 CIDR, and state the exact GitHub token scopes and AMI build input.
- [x] Validate the Packer template against the copied base AMI, rerun `go test ./...`, `go vet ./...`, Go lint, and the E2E Python unit suite.
- [x] Name new public images by CLI major and UTC build date; discover only matching major unless `public_ami_major` or `active_ami` overrides it.
- [x] Add `scripts/public-amis.sh` for regional builds, listing, exact deletion, and preview-first pruning with owner/tag/name checks.
- [x] Validate new Packer variables, full Go suite, `go vet`, Go lint, ShellCheck, offline script safety tests, and a live read-only ca-central-1 prune preview (no candidates).
- [x] Update bridgectl image and boot pins to v1.4.6; verify the noble/amd64 apt package SHA-256 matches the release asset and pass focused Go tests and `make build`.
- [x] Replace the moving Homebrew Helm install with the v4.3.1 upstream archive pinned to SHA-256; local Packer, Ansible, and Go playbook checks pass. The ca-central-1 build passed Ansible but AWS rejected public launch permissions because regional AMI Block Public Access is enabled; Packer deregistered the attempted AMI and deleted its snapshot.
- [x] Add a public AMI build preflight for regional AMI Block Public Access so this account setting is detected before paid provisioning.
- [x] Disable AMI Block Public Access in ca-central-1 as authorized, then start a new public AMI build. Update `scripts/public-amis.sh` with an explicit `--enable-public-sharing` option for blocked account-managed regions ca-central-1, us-east-1, us-east-2, us-west-1, and us-west-2 before regional builds, leaving the setting enabled.
- [x] Publish public AMI `ami-0696c3b567c3e6bc9` (`desktopctl-v0-20261019-1792403900`) in ca-central-1. EC2 reports `available` and `Public: true`, with desktopctl 0.7.0, bridgectl v1.4.6, and Helm v4.3.1 tags. DynamoDB history and the selected XDG config point to it. Leave regional AMI public sharing enabled as requested. The public-amis wrapper's final exit 127 followed an in-place edit while its Bash process was still running; its CLI build completed, and the current wrapper passes tests and live listing.
- [ ] Run live desktop create, SSH login, and teardown; record resource IDs and results.
