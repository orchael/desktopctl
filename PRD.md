# Product Requirements Document — ai-desktops

## Overview

`ai-desktops` manages a fleet of remote servers that act as persistent AI coding desktops. Each desktop is a browser-accessible Linux environment designed primarily for AI coding agents such as Codex, Claude, and Gemini, while still allowing human operator access for supervision and debugging.

The product is not an orchestrator for arbitrary repo agents. The product is the desktop fleet itself: provisioning it, securing it, connecting to it, and keeping it useful for real application development work.

---

## Problem

AI coding agents are most useful when they run inside stable, long-lived development environments with real tools, real repositories, real browsers, and enough compute to build and test applications. Local laptops are often the wrong place to run that workload:

- They are resource-constrained.
- They are hard to standardize.
- They are inconvenient to access remotely.
- They create friction when switching between projects or machine contexts.
- They blur the boundary between operator machine state and agent machine state.

Generic VMs solve only part of the problem. A useful AI coding desktop needs:

- browser-accessible desktop access for human supervision
- a consistent Linux GUI environment
- agent runtime management
- project and repo bootstrap rules
- persistent state across stop/start cycles
- a clear security boundary for access, secrets, and repo scope

`ai-desktops` exists to provide that environment as a managed fleet rather than a one-off manual server setup.

---

## Goals

1. Provision and manage a fleet of persistent remote desktops intended for AI-assisted software development.
2. Make desktop creation operator-driven through a CLI-first workflow.
3. Provide browser-based desktop access by default, with SSH available for debugging and recovery.
4. Standardize each desktop on the `novnc-desktop` substrate using the Elementary desktop environment.
5. Install and configure `bridgectl` on every desktop so AI agents can be launched and supervised in a consistent way.
6. Ensure every desktop includes the baseline developer toolchain: `git`, `docker`, `nvim`, and `tmux`.
7. Support multiple repositories per desktop, with the constraint that all repos on a given desktop must belong to the same GitHub organization or the same personal account.
8. Preserve desktop state across stop/start lifecycle operations.
9. Keep the product operable by a single primary operator in v1 without blocking future authenticated access for external users.

---

## Non-Goals

- Building a general multi-repo orchestration control plane.
- Making Slack or Discord the primary control surface.
- Building a polished multi-tenant SaaS product in v1.
- Supporting multiple cloud providers in the first release.
- Providing ephemeral-by-default desktops in v1.
- Running unrelated workloads outside the AI coding desktop use case.
- Solving broad organization-wide IAM and RBAC in the initial release.

---

## Users

### Primary user

The primary v1 user is the operator: you. The operator creates desktops, assigns repos, connects through the browser, debugs over SSH when needed, and manages lifecycle operations.

### Secondary users

External users may need access to the application surface later, but they are not the primary workflow in v1. The system must not assume a single hardcoded local user forever, but full multi-user product design is deferred.

---

## Product Shape

`ai-desktops` is a fleet manager for remote AI coding desktops.

Each managed desktop includes:

- a remote Ubuntu host
- `novnc-desktop` configured with the `elementary` desktop type
- browser-accessible desktop access via noVNC over HTTPS
- SSH access for debugging
- `bridgectl` for agent runtime management
- local developer tooling (`git`, `docker`, `nvim`, `tmux`)
- one persistent workspace that may contain multiple repositories from a single GitHub owner

The desktop is the unit of management. A desktop may be stopped and later resumed with its state intact.

---

## Operator Workflow

1. The operator invokes an `ai-desktops` CLI command to create a new desktop.
2. The CLI accepts the desktop profile, target GitHub owner, and optional repository list to clone into the workspace.
3. The system provisions a remote host and configures it with `novnc-desktop`, `bridgectl`, and the standard toolchain.
4. The system returns the desktop identifier, browser access URL, and SSH connection details.
5. The operator opens the browser URL to access the desktop and may use SSH for debugging when needed.
6. The operator uses the desktop to run AI coding agents and work against one or more repositories in the allowed owner scope.
7. The operator can stop the desktop while preserving state, then start it again later.
8. The operator can terminate the desktop when it is no longer needed.

---

## Functional Requirements

### FR-1 — Desktop lifecycle

| ID | Requirement |
| --- | --- |
| FR-1.1 | The system must provide a CLI-first interface for creating, listing, inspecting, starting, stopping, and terminating desktops. |
| FR-1.2 | Creating a desktop must provision a remote server and prepare it for browser-based desktop access and AI agent execution. |
| FR-1.3 | Stopping a desktop must hibernate it, preserving both disk and RAM state, so work resumes exactly where it left off after restart. |
| FR-1.4 | Starting a previously hibernated desktop must restore access to the same persisted workspace with in-memory process state intact. |
| FR-1.5 | Terminating a desktop must permanently destroy its compute resources and attached state. |
| FR-1.6 | Fleet listing must hide terminated desktop records by default while allowing operators to include them explicitly. |
| FR-1.7 | Operators must be able to preview and purge terminated desktop records from fleet metadata. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-1.1 | `ai-desktops create --github-owner <owner> --json` exits 0 and returns `desktop_id`, `novnc_url`, and `ssh_target` | `TestFR1_CreateOutput` |
| AC-1.2 | DynamoDB `lifecycle_state` is `ready` immediately after create completes | `TestFR1_StateReady` |
| AC-1.3 | `ai-desktops list --json` output includes the created desktop ID | `TestFR1_List` |
| AC-1.4 | `ai-desktops status <id> --json` returns all required fields: id, state, hostname, novnc_url, ssh_target, github_owner, instance_id, stack_name | `TestFR1_StatusFields` |
| AC-1.5 | `ai-desktops stop <id>` exits 0, hibernates the instance, and transitions `lifecycle_state` to `stopped` | `TestFR7_01_Stop` |
| AC-1.6 | `ai-desktops start <id>` exits 0 and transitions `lifecycle_state` back to `ready` | `TestFR7_02_Start` |
| AC-1.7 | After `ai-desktops terminate <id>`, the desktop no longer appears in `list` output | TestMain cleanup |
| AC-1.8 | `create` without `--github-owner` fails with a clear error message | `TestFR1_CreateRejectsWithoutOwner` |
| AC-1.9 | `ai-desktops list --all` includes terminated desktop records | Unit and manual CLI verification |
| AC-1.10 | `ai-desktops purge --dry-run` previews terminated records without deleting them | Unit and manual CLI verification |
| AC-1.11 | `ai-desktops purge` deletes terminated records and reports the number deleted | Unit and manual CLI verification |

### FR-2 — Desktop access

| ID | Requirement |
| --- | --- |
| FR-2.1 | Every desktop must expose a browser-accessible noVNC session over HTTPS. |
| FR-2.2 | Every desktop must expose SSH connectivity for operator debugging and recovery workflows. |
| FR-2.3 | Browser access is the primary access path; SSH is a secondary operational path. |
| FR-2.4 | The system must return both the browser URL and SSH connection details after desktop creation. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-2.1 | HTTPS GET to `novnc_url` (port 8443) returns a non-5xx response within 5 minutes of creation | `TestFR2_NoVNCHTTPSReachable` |
| AC-2.2 | TCP connection to `hostname:22` succeeds within 3 minutes of creation | `TestFR2_SSHPortReachable` |
| AC-2.3 | SSH key authentication to `ubuntu@hostname` succeeds | `TestFR2_SSHAuthentication` |
| AC-2.4 | `create --json` output contains both `novnc_url` (HTTPS, port 8443) and `ssh_target` (`ubuntu@…`) | `TestFR1_CreateOutput` |

### FR-3 — Desktop substrate

| ID | Requirement |
| --- | --- |
| FR-3.1 | Every managed desktop must use `novnc-desktop` as the browser desktop substrate. |
| FR-3.2 | `novnc-desktop` must be configured to use the `elementary` desktop environment for `ai-desktops` managed hosts. |
| FR-3.3 | Failure to provision the required desktop substrate must fail desktop creation clearly rather than producing a partially usable desktop. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-3.1 | `systemctl is-active novnc-desktop` returns `active` via SSH | `TestFR3_NoVNCServiceActive` |
| AC-3.2 | `systemctl is-active nginx` returns `active` (noVNC HTTPS proxy) | `TestFR3_NginxServiceActive` |
| AC-3.3 | Port 8443 has a listening process (confirmed by `ss -tlnp`) | `TestFR3_NoVNCListening` |
| AC-3.4 | Pantheon greeter package or xsession desktop file for elementary is present | `TestFR3_ElementaryDesktopEnvironment` |
| AC-3.5 | A desktop that fails provisioning enters `failed` state, not `ready` | TestMain: `waitForState` fails cleanly |

### FR-4 — Agent runtime

| ID | Requirement |
| --- | --- |
| FR-4.1 | Every desktop must install and configure `bridgectl`. |
| FR-4.2 | `bridgectl` must be the standard mechanism for launching and supervising AI agent processes on a desktop. |
| FR-4.3 | The managed desktop environment must support at least Codex, Claude, and Gemini as intended bridge-managed agent providers. |
| FR-4.4 | The fleet manager must surface enough connection or status information for the operator to verify that the bridge is running on the desktop. |
| FR-4.5 | The fleet manager must support remote agent control by creating an authenticated tunnel from the operator machine to the desktop-local `bridgectl` endpoint. |
| FR-4.6 | `bridgectl` must not be exposed directly to the public internet in v1. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-4.1 | `systemctl --user is-active bridgectl` returns `active` via SSH | `TestFR4_BridgeServiceActive` |
| AC-4.2 | Bridge listens on `127.0.0.1:9445`, not `0.0.0.0:9445` | `TestFR4_BridgeLocalhostOnly` |
| AC-4.3 | `ai-desktops agent <id> status` completes without error | `TestFR4_AgentStatusCommand` |
| AC-4.4 | `ai-desktops agent <id> providers` output includes codex, claude, and gemini | `TestFR4_AgentProvidersCommand` |

### FR-5 — Base toolchain

| ID | Requirement |
| --- | --- |
| FR-5.1 | Every desktop must include `git`. |
| FR-5.2 | Every desktop must include `docker`. |
| FR-5.3 | Every desktop must include `nvim`. |
| FR-5.4 | Every desktop must include `tmux`. |
| FR-5.5 | The base toolchain must be present before a desktop is reported ready. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-5.1 | `which git && git --version` exits 0 via SSH | `TestFR5_GitInstalled` |
| AC-5.2 | `which docker && docker --version` exits 0 and `docker info` succeeds as ubuntu user | `TestFR5_DockerInstalled`, `TestFR5_DockerDaemonActive` |
| AC-5.3 | `which nvim && nvim --version` exits 0 via SSH | `TestFR5_NvimInstalled` |
| AC-5.4 | `which tmux && tmux -V` exits 0 via SSH | `TestFR5_TmuxInstalled` |
| AC-5.5 | All four tools resolve via `which git docker nvim tmux` in a single SSH call | `TestFR5_AllToolsOnPath` |
| AC-5.6 | Desktop is in state `ready` only after all tools are confirmed present (desktop tested is already ready) | Implied by all AC-5.x tests running against a ready fixture |

### FR-6 — Workspace and repository policy

| ID | Requirement |
| --- | --- |
| FR-6.1 | A desktop may contain multiple Git repositories in its workspace. |
| FR-6.2 | All repositories assigned to a desktop must belong to a single GitHub organization or a single GitHub personal account. |
| FR-6.3 | Desktop creation must require the caller to specify the GitHub owner boundary for that desktop. |
| FR-6.4 | The system must reject attempts to attach or clone repositories from a different owner into an existing desktop-managed workspace. |
| FR-6.5 | Automatic checkout into a standard workspace location must be supported during desktop creation when repositories are provided. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-6.1 | Each repo URL passed to `create` is present under `/workspace/<repo-name>` on the desktop | `TestFR6_ReposPresent` |
| AC-6.2 | Each `/workspace/<repo-name>` is a valid git repository (`git rev-parse HEAD` exits 0) | `TestFR6_WorkspaceIsGitRepo` |
| AC-6.3 | Git remote URL for each cloned repo contains the desktop's GitHub owner | `TestFR6_WorkspaceOwnerBoundary` |
| AC-6.4 | `create --preview` with repos from two different owners exits non-zero with an error | `TestFR6_MixedOwnerRejected` |
| AC-6.5 | `create --preview` with a non-GitHub repo URL exits non-zero | `TestFR6_NonGitHubRepoRejected` |
| AC-6.6 | `create` without `--github-owner` exits non-zero | `TestFR1_CreateRejectsWithoutOwner` |

### FR-7 — Persistence

| ID | Requirement |
| --- | --- |
| FR-7.1 | Desktop filesystem and RAM state must persist across stop/start operations via EC2 hibernation. |
| FR-7.2 | Installed tools, checked-out repositories, editor state, and agent workspace artifacts must remain available after restart unless explicitly deleted by the operator. |
| FR-7.3 | Persistence semantics apply to normal desktop lifecycle operations, not to terminated desktops. |
| FR-7.4 | Desktops must be launched with EC2 hibernation enabled and an encrypted root EBS volume (both required by AWS for hibernation). |
| FR-7.5 | The root EBS volume must default to 100 GiB to accommodate OS, applications, and the in-memory RAM dump written during hibernation. |
| FR-7.6 | An operator may resize an existing on-demand desktop to a different EC2 instance type while preserving its root EBS volume and fleet identity. |
| FR-7.7 | Resize operations must power-stop the instance before changing instance type; RAM hibernation state is not preserved during resize. |
| FR-7.8 | Resize must reject incompatible nested-virtualization target instance families and Spot desktops until Spot resize semantics are explicitly supported. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-7.1 | After `stop` + `start`, each cloned repo is still present under `/workspace/<repo-name>` | `TestFR7_03_WorkspacePersists` |
| AC-7.2 | After `stop` + `start`, all base toolchain commands (`git`, `docker`, `nvim`, `tmux`) remain on PATH | `TestFR7_04_ToolsPersist` |
| AC-7.3 | After `stop`, `lifecycle_state` is `stopped`; after `start`, it is `ready` | `TestFR7_01_Stop`, `TestFR7_02_Start` |
| AC-7.4 | The root EBS volume is encrypted and hibernation is configured at instance launch time | Infrastructure review |
| AC-7.5 | `ai-desktops create --volume-size <n>` launches an instance with a root volume of the specified size | Manual CLI verification |
| AC-7.6 | `ai-desktops resize --help` exits 0 and describes `--instance-type` | Unit/smoke help verification |
| AC-7.7 | Resize validation rejects empty, unchanged, Spot, and nested-virtualization-incompatible target instance types | Unit tests |

### FR-8 — External access posture

| ID | Requirement |
| --- | --- |
| FR-8.1 | v1 may optimize for a single operator workflow. |
| FR-8.2 | The system architecture must not assume that only one human can ever access the application surface. |
| FR-8.3 | Future authenticated access for external users must be possible without redesigning the desktop lifecycle model. |

*FR-8 requirements are architectural constraints verified by design review, not integration tests.*

### FR-9 — Pre-baked AMI with Packer

| ID | Requirement |
| --- | --- |
| FR-9.1 | The system must support building per-region machine images with the base toolchain pre-installed using Packer. |
| FR-9.2 | The AMI build process must produce identical toolchain versions across all supported regions. |
| FR-9.3 | Built AMI IDs must be persisted in operator config (`config.yaml`) and used by subsequent desktop creates. |
| FR-9.4 | Cloud-init user-data must be reduced to runtime-only concerns: secret injection, workspace setup, and repository cloning. |
| FR-9.5 | The base AMI must be built from Ubuntu 24.04 LTS (Noble) and pre-install: `docker`, `git`, `nvim`, `tmux`, `uv`, `go`, `brew` (linuxbrew), `bridgectl` (pinned version). |
| FR-9.6 | A CLI command `ai-desktops ami build` must invoke Packer and automatically update `config.yaml` with the resulting AMI IDs per region. |
| FR-9.7 | Desktop creation must prefer pre-baked AMI IDs from config over the hardcoded default Ubuntu AMI map. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-9.1 | `ai-desktops ami build --help` exits 0 and mentions packer or ami | `TestFR9_AMIBuildCommandExists` |
| AC-9.2 | `ai-desktops ami list --help` exits 0 | `TestFR9_AMIListCommandExists` |
| AC-9.3 | `create --preview --ami <id>` output references the provided AMI ID | `TestFR9_CreateUsesAMIFromConfig` |
| AC-9.4 | Full `ami build` succeeds and config is updated (gated on `AI_DESKTOPS_RUN_AMI_BUILD=true`) | `TestFR9_AMIBuildFull` |

### FR-11 — GitHub developer tooling

| ID | Requirement |
| --- | --- |
| FR-11.1 | Every desktop must have the `gh` CLI installed and on PATH. |
| FR-11.2 | `gh` must be pre-authenticated for the desktop's configured GitHub owner at first boot, using the same GitHub PAT already provisioned for repository cloning. |
| FR-11.3 | `git` must be configured with a commit identity (`user.name` and `user.email`) so that commits created on the desktop are attributed correctly. |
| FR-11.4 | The developer must be able to create and manage pull requests from the desktop (`gh pr create`, `gh pr list`, `gh pr merge`). |
| FR-11.5 | The developer must be able to view and inspect GitHub Actions workflow runs from the desktop (`gh run list`, `gh run view`). |
| FR-11.6 | The developer must be able to retrieve failing Actions log output from the desktop (`gh run view --log-failed`). |
| FR-11.7 | `python3` must be installed and on PATH to support scripted GitHub health checks that use Python for JSON parsing and date arithmetic. |
| FR-11.8 | The developer must be able to access GitHub security APIs from the desktop: Dependabot alerts, code-scanning alerts, and secret-scanning alerts via `gh api`. |
| FR-11.9 | The developer must be able to inspect and merge Dependabot PRs from the desktop, including auto-merge of safe minor/patch upgrades. |
| FR-11.10 | The developer must be able to inspect branch protection rules and repository rulesets via `gh api` from the desktop. |
| FR-11.11 | The developer must be able to push commits and branches to GitHub from the desktop without re-entering credentials. |

**Acceptance criteria:**

| ID | Criterion | Integration test |
| --- | --- | --- |
| AC-11.1 | `which gh && gh --version` exits 0 via SSH | `TestFR11_GHInstalled` |
| AC-11.2 | `gh auth status` exits 0 and reports an authenticated account | `TestFR11_GHAuthStatus` |
| AC-11.3 | `git config --global user.name` returns a non-empty value via SSH | `TestFR11_GitIdentityName` |
| AC-11.4 | `git config --global user.email` returns a non-empty value via SSH | `TestFR11_GitIdentityEmail` |
| AC-11.5 | `which python3 && python3 --version` exits 0 via SSH | `TestFR11_Python3Installed` |
| AC-11.6 | `gh pr list --help` exits 0 | `TestFR11_GHPRList` |
| AC-11.7 | `gh run list --limit 1 --json status,conclusion` exits 0 (or returns empty array for repos with no runs) | `TestFR11_GHRunList` |
| AC-11.8 | `gh api /repos/OWNER/REPO/dependabot/alerts?state=open\&per_page=1` exits 0 or returns HTTP 403 (enabled but no admin access) rather than HTTP 404 (not enabled) | `TestFR11_DependabotAPIReachable` |
| AC-11.9 | `gh api /repos/OWNER/REPO/code-scanning/alerts?state=open\&per_page=1` exits 0 or returns HTTP 403 | `TestFR11_CodeScanningAPIReachable` |
| AC-11.10 | `gh api /repos/OWNER/REPO/secret-scanning/alerts?state=open\&per_page=1` exits 0 or returns HTTP 403 | `TestFR11_SecretScanningAPIReachable` |
| AC-11.11 | `gh api /repos/OWNER/REPO/branches/main/protection` exits 0 or returns HTTP 403 | `TestFR11_BranchProtectionAPIReachable` |
| AC-11.12 | `git push --dry-run` succeeds against the cloned workspace repo without credential prompts | `TestFR11_GitPushCredentials` |

---

## Security Requirements

### SR-1 — Desktop access security

- SSH (port 22) is exposed via security group rules, restricted to configured `operator_cidr`.
- noVNC HTTPS (port 8443) is accessible from anywhere via Route53 DNS and TLS.
- HTTP port 80 is available for ACME challenges and temporary testing.

### SR-2 — Secret handling

- Secrets must not be baked into machine images.
- Agent provider credentials must be injected at runtime through a secure secret-management mechanism.
- Secrets must not be emitted in fleet-manager logs, agent logs, or browser-access bootstrap output.

### SR-3 — Workspace boundary

- Repo policy enforcement must prevent a managed desktop workspace from mixing repositories from multiple GitHub owners.
- Desktop provisioning and repo bootstrap actions must validate the requested owner boundary before cloning repositories.

### SR-4 — Future user access

- External-user access, when implemented, must layer on top of authenticated application access rather than direct unauthenticated desktop URLs.
- Full RBAC is deferred, but the architecture must leave room for per-user or per-role authorization later.

**v1 decision (deferred):** v1 is single-operator only. Future access will require an authentication gateway in front of port 8443 (e.g. OAuth2 proxy, Cloudflare Access, or a purpose-built auth service). The desktop lifecycle model does not change — auth is a layer inserted in front of nginx. The nginx routing layout established in Plan 16 (`/`, `/novnc/`, `/api/`) must remain compatible with a future `auth_request` directive or proxy insertion at the root without restructuring.

---

## Non-Functional Requirements

| ID | Requirement |
| --- | --- |
| NFR-1 | Desktops must be long-lived and operationally stable enough for ongoing application development, not just smoke-test sessions. |
| NFR-2 | Desktop creation must be repeatable and automatable through the CLI. |
| NFR-3 | Provisioning failures must be diagnosable through clear lifecycle status and operator-visible errors. |
| NFR-4 | The fleet manager must treat browser access as a first-class capability, not an afterthought. |
| NFR-5 | The product must standardize the desktop baseline so agents behave consistently across desktops. |
| NFR-6 | The system should prefer simple lifecycle semantics: create, inspect, start, stop, terminate. |

---

## MVP Scope

The MVP includes:

- CLI-driven desktop creation and lifecycle management
- AWS as the first cloud provider target for remote hosts
- Pulumi-managed AWS infrastructure with an S3 DIY backend for Pulumi state
- DynamoDB-backed fleet metadata for fast desktop lookup and lifecycle status
- persistent desktop state
- browser access via `novnc-desktop`
- `elementary` desktop environment
- SSH debugging access
- `bridgectl` installation and runtime enablement
- remote agent control through a CLI-managed tunnel to `bridgectl`
- baseline toolchain installation: `git`, `docker`, `nvim`, `tmux`
- multi-repo workspace support under a single GitHub owner boundary

The MVP does not include:

- Slack or Discord control
- generalized multi-agent orchestration across many repos
- rich multi-user product workflows
- non-persistent desktop classes
- multi-cloud support

---

## Assumptions

- The initial infrastructure target is AWS-backed remote hosts, carried forward from the prior project direction.
- Managed desktops run on Ubuntu 24.04 LTS (Noble) hosts compatible with `novnc-desktop` and the required toolchain.
- `ai-desktops` will reuse existing first-party components rather than reimplementing noVNC desktop provisioning or agent process supervision from scratch.

If any of these assumptions are wrong, the PRD should be updated before implementation work proceeds.

---

## Acceptance Criteria

1. An operator can run a CLI command to create a new desktop and receives a desktop ID, browser URL, and SSH connection details.
2. The created desktop is reachable in a browser through the `novnc-desktop` interface using the Elementary desktop environment.
3. The created desktop has `git`, `docker`, `nvim`, and `tmux` installed and usable.
4. `bridgectl` is installed, running, and available as the desktop’s standard agent runtime.
5. The operator can control a desktop's AI agents remotely through a CLI-managed tunnel to `bridgectl`.
6. A desktop can be created with multiple repositories checked out into its workspace when all repositories belong to the same GitHub organization or personal account.
7. A request that mixes repositories from different GitHub owners is rejected before the desktop is reported ready.
8. After stopping and restarting a desktop, the workspace contents and prior desktop state remain present.
9. After terminating a desktop, the desktop and its persisted state are no longer recoverable through normal lifecycle operations.

---

## Build Risks and Implementation Decisions

The following items are likely to cause implementation churn or security gaps if they remain implicit.

### State ownership

The PRD requires persistent desktops, but persistence must be defined at the infrastructure layer. The v1 architecture treats the EC2 instance plus its root EBS volume as the persisted desktop unit. Stop hibernates the instance, saving RAM to the encrypted root volume; start resumes it. Terminate destroys the instance and volume unless snapshot support is explicitly added later.

EC2 hibernation requires two immutable launch-time settings: `Hibernation: true` and an encrypted root EBS volume. These cannot be enabled on existing instances. The desktop Pulumi stack sets both unconditionally. Operators must recreate existing desktops to gain hibernation support.

### Pulumi backend bootstrap

Pulumi can store state in an S3 DIY backend, but the backend bucket still needs to exist before S3-backed stacks can use it. The implementation needs a bootstrap step that creates or verifies the S3 state bucket and DynamoDB fleet table before Pulumi-managed desktop stacks run.

### Pulumi stack topology

One large infrastructure state for the full fleet will make individual desktop lifecycle operations risky over time. The architecture should separate foundation infrastructure from per-desktop Pulumi stacks so one desktop can be previewed, updated, stopped, or destroyed without touching unrelated desktops.

### Fleet metadata

Pulumi state should remain the source of truth for cloud resources, but it is not the right query layer for fast fleet listing, workflow state, or failed-provisioning diagnostics. The implementation should use DynamoDB as the fleet metadata store for desktop records, lifecycle state, hostnames, owner boundaries, and last readiness results.

### DNS and TLS

v1 requires Route53-backed DNS. Production desktops use `desktops.orchael.com`; local and development desktops use `desktops.orchael.dev`. Per-desktop hostnames should be created under the selected zone.

### Access control

The PRD says external-user access must remain possible but does not define v1 auth. v1 should protect noVNC through `novnc-desktop` signed access URLs and restrict SSH by key pair and security group. Full user accounts, invitation flows, and RBAC remain future work.

### Secrets and provider credentials

`bridgectl` needs provider credentials for Codex, Claude, and Gemini. The PRD intentionally forbids baking secrets into images, but the implementation still needs a delivery path. v1 should use AWS SSM Parameter Store or Secrets Manager references passed during provisioning, then render local bridge environment files on the desktop with restrictive permissions.

The operator workflow must support updating the owner-scoped agent credential secret after initial setup without overwriting unrelated provider keys. In addition to API keys, the agent secret may carry Codex ChatGPT auth as `CODEX_AUTH` from a local Codex `auth.json` file and Claude Code long-lived auth as `CLAUDE_CODE_OAUTH_TOKEN` from the operator-provided setup-token output.

Operators must also be able to manage per-desktop injected secret references after creation. Adding a secret path should validate that the AWS Secrets Manager secret exists, inject all configured desktop secrets, and persist the updated fleet metadata. Removing a secret path should rewrite the desktop environment files without the removed secret, clear those files when no configured secrets remain, and persist the updated fleet metadata. Reloading should re-fetch the currently configured fleet secret list without changing it.

### Repository authentication

Repo cloning requires GitHub credentials. v1 uses a fine-scoped GitHub PAT stored in AWS secret storage and scoped to the configured owner. A GitHub App should replace the PAT after the first smoke path is working.

### Remote agent control

The operator needs remote control of agents running inside each desktop. v1 should keep `bridgectl` bound to localhost on the desktop and create a short-lived SSH or AWS SSM port-forward from the operator machine when remote control is requested. A private-network bridge endpoint with mTLS can be added later if a long-running control plane needs direct access.

### Desktop readiness

The PRD says the desktop is reported ready after provisioning, but readiness must be concrete. v1 readiness should require SSH reachable, HTTPS noVNC reachable, `novnc-auth` token generation working, Docker active, `bridgectl` active, and the requested repositories present under the workspace.

### Elementary support

`novnc-desktop` treats Elementary/Pantheon support as best-effort. This PRD requires Elementary as the standard desktop, so implementation must either pin a known-good Ubuntu/Pantheon combination or downgrade the requirement to a preferred desktop with Openbox fallback. Leaving this unresolved can break automated provisioning.

### Cost and cleanup

Persistent desktops imply ongoing cloud cost. Failed desktop creation should leave the instance running by default for debugging until `doctor` is useful. Automated idle shutdown, budgets, and cost alerts can follow after MVP.

### Pre-baked AMI provisioning

Desktop provisioning performance is critical for interactive operator workflows. `ai-desktops`
uses pre-baked AMIs built with Packer so package installation and toolchain setup do not run during
desktop creation. This reduces boot time and removes package-install failures from the runtime path.

The `ai-desktops ami build` command invokes Packer to build one or more regions sequentially.
Version pins are maintained in `packer/variables.pkrvars.hcl`, and resulting AMI IDs are stored in
`config.yaml`. The baked image includes the baseline development toolchain, `novnc-desktop`, and
`bridgectl`. Cloud-init is reduced to runtime-only steps: TLS certificate generation via
certbot, nginx reverse-proxy configuration, secret injection, workspace setup, and repository cloning. Additional desktop applications such as `ai-agent-browser` and
`android-emulator-webapp` can be added to the image when they become required by a shipped workflow.

---

## Future Enhancements

- Authenticated external-user access to the application surface
- Per-desktop sharing and access control
- Multiple desktop profiles by compute class or GPU tier
- Snapshot, clone, or template support
- Deeper bridge health and session observability
- Web UI on top of the CLI-driven lifecycle engine
- Support for additional cloud or on-premise providers
- Automated CI/CD-driven AMI rebuilds when pinned component versions are updated
- WireGuard VPN support (FR-10): per-desktop server keys in SSM, peer management CLI (`add-peer`, `remove-peer`, `list-peers`, `show-config`), QR-code onboarding, live peer updates, health checks, and restricted network access through VPN tunnel
- Multi-desktop WireGuard mesh topology (peer-to-peer desktop connectivity)
- WireGuard server separate from desktop instance (shared bastion topology)
