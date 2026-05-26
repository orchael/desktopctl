# TODO

## Phase 1 — Foundation (Tasks 01–03)

- [x] **Task 01** — Go module and CLI skeleton
  - `go.mod`, `cmd/ai-desktops/main.go`, `internal/version`
  - Wire all MVP commands with Cobra (`bootstrap`, `init-foundation`, `create`, `list`, `status`, `url`, `ssh`, `agent`, `stop`, `start`, `terminate`, `doctor`)
  - Global flags: `--config`, `--profile`, `--region`, `--json`
  - `version` command prints version string
  - Verify: `go test ./...`, `go run ./cmd/ai-desktops --help`, `go run ./cmd/ai-desktops version`

- [x] **Task 02** — Config and repository validation
  - `internal/config` — AWS region/profile, Pulumi bucket, DynamoDB table, env (`prod`/`dev`), Route53 zone, desktop profile, secret refs
  - `internal/repo` — parse and validate all GitHub URL forms; enforce single-owner boundary; reject mixed-owner and non-GitHub inputs
  - `prod` → `desktops.orchael.com`, `dev` → `desktops.orchael.dev`
  - Verify: unit tests for URL formats, mixed-owner rejection, non-GitHub rejection

- [x] **Task 03** — Pulumi backend bootstrap (`bootstrap` command)
  - `internal/backend` + AWS SDK helpers for S3 and DynamoDB
  - S3: create/verify bucket, enable versioning, SSE, block public access
  - DynamoDB: create/verify fleet table with `desktop_id` PK
  - Command must be idempotent; print Pulumi backend URL; support `--json`
  - Verify: `go test ./internal/backend ./internal/awsx`, idempotent live run

## Phase 2 — Infrastructure (Tasks 04–06)

- [x] **Task 04** — Pulumi foundation stack (`init-foundation` command)
  - `infra/pulumi/foundation/` — VPC/subnet refs, security group baseline, IAM role + instance profile with SSM permissions, Route53 zone lookup
  - Fail clearly if hosted zone missing; export subnet IDs, SG IDs, instance profile, zone ID, table name
  - Use Pulumi Automation API from CLI; support `--preview`
  - Verify: `go test ./...`, `go run ./cmd/ai-desktops init-foundation --env dev --preview`

- [x] **Task 05** — DynamoDB fleet store (`internal/store`)
  - Desktop record: `desktop_id`, `stack_name`, `github_owner`, `lifecycle_state`, `instance_id`, `hostname`, `novnc_url`, `ssh_target`, `readiness`, `created_at`, `updated_at`, last failure phase/message
  - CRUD + mark-terminated operations; timestamps in RFC3339; `list`/`status` commands read from DynamoDB
  - Verify: unit tests for marshaling and lifecycle transitions

- [x] **Task 06** — Pulumi desktop stack
  - `infra/pulumi/desktop/` — one stack per desktop (`ai-desktops/desktop-<id>`), EC2 + root EBS, SG attachment, IAM profile, Route53 record, cloud-init payload, resource tags
  - Hostname format: `<desktop_id>.desktops.orchael.{com,dev}`
  - No public exposure of VNC/Docker/bridge ports; SSH restricted by operator CIDR
  - Stack exports: desktop ID, instance ID, hostname, noVNC base URL, SSH target, workspace path, GitHub owner
  - Verify: stack preview with valid foundation outputs

## Phase 3 — Desktop Provisioning (Tasks 07–09)

- [x] **Task 07** — Lifecycle commands (`create`, `stop`, `start`, `terminate`)
  - `create`: Pulumi preview → DynamoDB record in `creating` → Pulumi up; log runs to `~/.ai-desktops/runs/<run_id>/`
  - `stop`: AWS stop-instance, preserve Pulumi state, update DynamoDB
  - `start`: AWS start-instance, refresh DynamoDB
  - `terminate`: Pulumi destroy → mark `terminated` only on success; leave failed instance running for debugging
  - Unit tests cover lifecycle state transitions; record last failure phase/message
  - Verify: `go test ./internal/desktop ./internal/store ./internal/pulumi`

- [x] **Task 08** — Desktop cloud-init bootstrap
  - Install: `git`, `docker`, `nvim`, `tmux`, SSM agent, `novnc-desktop` (elementary desktop type), `ai-agent-bridge` (systemd, localhost-only), `/workspace`, `/opt/ai-desktops`
  - Secrets from AWS SSM/Secrets Manager via instance role; secret files get restrictive permissions
  - Bootstrap logs available over SSH; leave enough logs for `doctor`
  - Verify: SSH up after boot, all tools on PATH, Docker active, novnc and bridge services active

- [x] **Task 09** — Readiness checks and `doctor` command
  - `internal/health` — checks: EC2 running, SSH accepts key, noVNC HTTPS responds, `novnc-desktop-url` mints URL, Docker active, bridge active, tools on PATH, repos present under `/workspace`, repo owner matches config
  - `create` only marks `ready` after all checks pass; `doctor` works on partially provisioned desktops
  - `doctor --json` produces machine-readable output; exits non-zero on failure; updates DynamoDB with result
  - Verify: `go test ./internal/health ./internal/desktop`

## Phase 4 — Workspace and Agent Control (Tasks 10–12)

- [x] **Task 10** — Repository checkout
  - Normalize repo inputs; validate owner boundary before any Pulumi change
  - Retrieve fine-scoped GitHub PAT from AWS secret storage (SSM or Secrets Manager); PAT never logged
  - Pass repo list to bootstrap; clone repos under `/workspace`; host-side clone revalidates owner boundary
  - Re-run must not clobber existing work; `doctor` reports missing or owner-mismatched repos
  - Verify: unit tests for normalization and mixed-owner rejection; multi-repo create flow

- [x] **Task 11** — Bridge tunnel and agent control
  - `internal/tunnel` + `internal/agent`; `ai-desktops agent <id> ...` command group
  - Commands: `agent <id> status`, `providers`, `start --provider <name> --repo <repo>`, `attach <session_id>`, `stop <session_id>`
  - SSM Session Manager port-forward first; SSH local-forward fallback; ephemeral local port; tunnel lifetime tied to CLI command
  - Bridge stays on `127.0.0.1:9445`; bridge port not in public SG
  - Unit tests cover tunnel command construction without real SSH/SSM
  - Verify: `go test ./internal/tunnel ./internal/agent`, `agent status` through tunnel

- [x] **Task 12** — Vite desktop webapp (`apps/desktop-web`)
  - Vite scaffold; build to `/opt/ai-desktops/desktop-web/dist`; served via Nginx or static route on desktop
  - UI shows: desktop ID, hostname, GitHub owner, workspace repo list, Docker status, bridge status, links to noVNC
  - No secrets in browser; API shim (if any) binds to localhost; deployable by cloud-init
  - Verify: `pnpm install && pnpm build`, webapp loads in browser after provisioning

## Phase 5 — MVP Validation (Task 13)

- [x] **Task 13** — MVP smoke and operator docs
  - `README.md` with: install steps, required AWS permissions, Route53 prerequisites, GitHub PAT setup, Pulumi CLI requirement, full lifecycle workflow docs
  - Example config file (no real secrets); smoke checklist covering all 12 steps (bootstrap → init-foundation → create → verify access/tools/repos/bridge → stop → start → verify persistence → terminate)
  - Known limitations: Elementary/Pantheon reliability, root EBS persistence, failed desktops left running
  - Verify: full smoke run against `desktops.orchael.dev`

## Post-MVP

- [x] Upgrade `novnc-desktop` to v0.1.5 and switch to custom ports 8080 (HTTP) and 8443 (HTTPS) — https://github.com/markcallen/ai-desktops/issues/7
- [x] Pin `novnc-desktop` and `ai-agent-bridge` installs to release tags — https://github.com/markcallen/ai-desktops/issues/4
- [x] Replace self-signed TLS cert with certbot + Route53 DNS-01 (Let's Encrypt) — https://github.com/markcallen/ai-desktops/issues/2
- [x] `doctor` SSH-based checks (Docker, tools, repos, bridge systemd unit) — https://github.com/markcallen/ai-desktops/issues/3
- [x] Make bridge port configurable in desktop Pulumi stack — https://github.com/markcallen/ai-desktops/issues/5
- [x] BUG FIX: `ai-desktops ssh` command fails with "Identity file not accessible" — added SSHKeyPath default to ~/.ssh/id_rsa in config.Defaults()
- [x] FEAT: SSH via SSM tunnel by default (--tunnel flag, falls back to direct SSH with --tunnel ssh)
- [x] FEAT: Add Region field to desktop records and display in `ai-desktops list`
- [x] FEAT: Region fallback to cfg.AWS.Region in list and status commands for pre-existing records
- [x] DOCS: Add SSM debugging guide to README after doctor command
- [x] ISSUE: Created GitHub Issue #23 — Add WireGuard VPN support (multi-platform: iOS, macOS, Linux, Windows)
- [x] DOCS: Updated PRD with temporary public access (SSH/HTTP/HTTPS) until WireGuard, then restrict to VPN
- [x] IMPL: Public access model — open SSH (port 22) to 0.0.0.0/0, add HTTP (port 80), update Pulumi config
- [ ] Install ai-agent-bridge v0.2.0 on desktops — blocked until an apt package or downloadable binary release exists (currently only a Docker image and Go module tag are published at v0.2.0)
- [ ] SSH tunnel: make StrictHostKeyChecking configurable — https://github.com/markcallen/ai-desktops/issues/6
- [ ] Determine if `--github-owner` flag is actually needed or if it can be inferred from repo URLs — https://github.com/markcallen/ai-desktops/issues/33
- [ ] Fix: `ai-desktops url` output cannot be connected to — novnc-desktop URL connection fails (defer until after novnc-desktop is baked into AMI) — https://github.com/markcallen/ai-desktops/issues/34

## Copilot review follow-ups (PR #31, commit 48f3e48)

- [x] `cmd/ai-desktops/cmd/stores.go` — `openAMIStore` comment corrected: returns error on AWS config failure, not in-memory fallback
- [x] `tests/integration/fr03_substrate_test.go` — switched to `dpkg -s` + `test -f` with `echo installed/missing` for reliable elementary-desktop detection
- [x] `tests/integration/fr03_substrate_test.go` — replaced `strings.Contains(..., "1")` with exact `== "installed"` check
- [x] `tests/integration/fr02_access_test.go` — merged stdlib imports into a single alphabetised group (gofmt)
- [x] `tests/integration/fr09_ami_test.go` — renamed `TestFR9_CreateUsesAMIFromConfig` → `TestFR9_CreateAMIFlagOverride`; updated comments to clarify this tests flag override, not config-based AMI selection
- [x] `PACKER_WORKFLOW.md` — replaced contributor-specific absolute paths with repo-relative references
- [ ] `internal/provision/ansible/desktop-setup/inventory.ini` — investigate and consolidate duplicate inventory.ini — https://github.com/markcallen/ai-desktops/issues/32
- [x] `internal/health/health.go` — updated `HTTPSChecker` doc comment to reflect non-4xx/5xx semantics (3xx is a pass)
