# Plan 14 — Pre-baked AMI with Packer

## Objective

Replace cloud-init-only software provisioning with a pre-baked AWS machine image built by Packer. The AMI includes all base toolchain components and services pre-installed with pinned versions, while cloud-init is reduced to runtime concerns (secrets injection, workspace setup, repository cloning, WireGuard configuration). This dramatically reduces boot time, eliminates package installation failures, and improves desktop readiness reliability.

## Scope

- Create a Packer HCL2 configuration to build per-region AMIs for Ubuntu 22.04 LTS
- Add a new `ami build` CLI command that invokes Packer and persists resulting AMI IDs in `config.yaml`
- Refactor Pulumi desktop stack to accept pre-baked AMI IDs from config instead of hardcoded Ubuntu map
- Eliminate the duplicate cloud-init template in the Pulumi program by passing pre-rendered user-data from the CLI
- Slim the cloud-init template to exclude package installations when a pre-baked AMI is used
- Add `AMIs` configuration section to `DesktopConfig` in `config.yaml`

## Depends on

- Plan 01–08 (foundation infrastructure, config system, Pulumi baseline)

## Acceptance Criteria

1. `packer/ubuntu-desktop.pkr.hcl` exists and successfully builds Ubuntu 22.04 AMIs for us-east-2.
2. Built AMI includes pre-installed: `docker`, `git`, `nvim`, `tmux`, `wireguard-tools`, `uv`, `go`, `brew`, `ai-agent-bridge` (pinned version), `novnc-desktop` (installed without TLS certificates).
3. `ai-desktops ami build --regions us-east-1,us-west-2 --vars-file packer/variables.pkrvars.hcl` succeeds and updates `config.yaml` with AMI IDs.
4. `ai-desktops ami list` displays a table of region → AMI ID.
5. `ai-desktops create` with pre-baked AMI IDs in config detects them and passes them to Pulumi instead of hardcoded Ubuntu map.
6. Cloud-init template is rendered in the CLI and passed to Pulumi as a config value (no inline template in Pulumi program).
7. Cloud-init template omits package installation steps when `PackagesPreInstalled` flag is true.
8. Existing test suite still passes; new tests added for AMI config validation and Packer manifest parsing.
9. Desktop provisioning with pre-baked AMI is noticeably faster than cloud-init-only path.

## Verification Steps

```bash
# Build AMI for us-east-2
cd packer
# Edit variables.pkrvars.hcl with version pins for novnc-desktop, ai-agent-bridge, go
packer init ubuntu-desktop.pkr.hcl
packer build -var-file=variables.pkrvars.hcl .

# Verify manifest and update config
ai-desktops ami build --regions us-east-2

# Verify config updated
ai-desktops ami list

# Create a desktop with pre-baked AMI
ai-desktops create --github-owner myorg --repo myrepo --preview
# Check cloud-init in Pulumi stack output: should not have package install steps

# Boot the desktop and verify tools are available
ai-desktops status <desktop-id>
ai-desktops doctor <desktop-id>  # should verify docker, nvim, tmux, git on PATH

# Run test suite
go test ./...
```

## Key Decisions

### User-data rendering in CLI, not Pulumi

The current Pulumi desktop stack program contains an inline cloud-init template that is a near-duplicate of `internal/provision/cloudinit.go`. To eliminate this duplication and make AMI support cleaner:
- The CLI (`create.go`) renders user-data by calling `provision.RenderCloudInit()` with populated `BootstrapConfig`
- The rendered user-data is passed to Pulumi as a config value (`"userData"`)
- The Pulumi program uses `cfg.Get("userData")` directly; it no longer renders templates

This cleanly separates concerns: CLI owns provisioning logic, Pulumi owns infrastructure.

### novnc-desktop installation in AMI with deferred TLS

**Decision**: Install `novnc-desktop` in the AMI without TLS certificates. At boot time (in cloud-init):
- Run `certbot certonly --dns-route53` to obtain TLS certificates for the desktop's hostname
- Configure nginx as a reverse proxy: HTTPS frontend on port 8443 → novnc-desktop's HTTP port (8080)
- This separates the stateless application install (Packer, fast) from instance-specific TLS setup (cloud-init, ~1 min for cert generation)

**Why this approach**:
- novnc-desktop application is pre-baked → faster boot, no installation failures
- TLS certificates are instance-specific (bound to hostname) → cannot be baked in
- nginx configuration at boot time → keeps cloud-init simple and integrates naturally with certbot
- Boot time is still much faster than cloud-init-only (only cert generation added at boot, not full application install)

### AMI versioning and naming

Built AMIs are named `ai-desktops-base-{novnc_desktop_version}-{timestamp}`. This allows operators to identify which component version is baked in and provides an audit trail in AWS. The AMI ID (not the name) is what matters; storing AMI IDs per region in config is the contract.

## Trade-offs

| Trade-off | Rationale |
|---|---|
| AMI build is CLI-initiated, not CI/CD | v1 trades automation for simplicity. Operator runs `ami build` when component versions are ready. CI/CD-driven rebuilds can follow once the Packer config is stable. |
| Cross-region AMI copy via Packer (not manual) | Packer handles the copy; no need for custom scripts. Cost is the AMI size × regions; acceptable at MVP scale. |
| No AMI versioning scheme (latest per build) | The config stores AMI IDs directly, not version pointers. Operator can keep old AMI IDs in config if they want fallback. Future: AMI tagging and version management. |
| `PackagesPreInstalled` flag instead of auto-detect | The CLI must know whether an AMI is pre-baked to render slim cloud-init correctly. Detecting this from AMI metadata is harder; a config flag is explicit and controllable. |

## Testing Approach

**Unit tests:**
- `internal/packer/packer_test.go`: test `ParseManifest()` with sample Packer manifest JSON
- `internal/config/config_test.go`: extend tests to validate `AMIs` map field, persistence via `Save()`
- `internal/provision/cloudinit_test.go`: add test cases for `PackagesPreInstalled: true` (verify package steps absent)

**Integration (manual):**
- Build an actual AMI via Packer
- Verify manifest is created
- Verify `ai-desktops ami build` updates config
- Provision a desktop with the pre-baked AMI
- Boot and verify all pre-installed tools are available and functional

## Out of Scope (for follow-up)

- CI/CD-triggered AMI rebuilds on version bumps
- AMI tagging and version management beyond AMI IDs in config
- AMI marketplace publishing
- Multi-region simultaneous builds (Packer handles this, but no custom orchestration)
- Signing or encryption of AMIs
