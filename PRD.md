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
5. Install and configure `ai-agent-bridge` on every desktop so AI agents can be launched and supervised in a consistent way.
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
- `ai-agent-bridge` for agent runtime management
- local developer tooling (`git`, `docker`, `nvim`, `tmux`)
- one persistent workspace that may contain multiple repositories from a single GitHub owner

The desktop is the unit of management. A desktop may be stopped and later resumed with its state intact.

---

## Operator Workflow

1. The operator invokes an `ai-desktops` CLI command to create a new desktop.
2. The CLI accepts the desktop profile, target GitHub owner, and optional repository list to clone into the workspace.
3. The system provisions a remote host and configures it with `novnc-desktop`, `ai-agent-bridge`, and the standard toolchain.
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
| FR-1.3 | Stopping a desktop must preserve its disk state so work can continue after restart. |
| FR-1.4 | Starting a previously stopped desktop must restore access to the same persisted workspace. |
| FR-1.5 | Terminating a desktop must permanently destroy its compute resources and attached state. |

### FR-2 — Desktop access

| ID | Requirement |
| --- | --- |
| FR-2.1 | Every desktop must expose a browser-accessible noVNC session over HTTPS. |
| FR-2.2 | Every desktop must expose SSH connectivity for operator debugging and recovery workflows. |
| FR-2.3 | Browser access is the primary access path; SSH is a secondary operational path. |
| FR-2.4 | The system must return both the browser URL and SSH connection details after desktop creation. |

### FR-3 — Desktop substrate

| ID | Requirement |
| --- | --- |
| FR-3.1 | Every managed desktop must use `novnc-desktop` as the browser desktop substrate. |
| FR-3.2 | `novnc-desktop` must be configured to use the `elementary` desktop environment for `ai-desktops` managed hosts. |
| FR-3.3 | Failure to provision the required desktop substrate must fail desktop creation clearly rather than producing a partially usable desktop. |

### FR-4 — Agent runtime

| ID | Requirement |
| --- | --- |
| FR-4.1 | Every desktop must install and configure `ai-agent-bridge`. |
| FR-4.2 | `ai-agent-bridge` must be the standard mechanism for launching and supervising AI agent processes on a desktop. |
| FR-4.3 | The managed desktop environment must support at least Codex, Claude, and Gemini as intended bridge-managed agent providers. |
| FR-4.4 | The fleet manager must surface enough connection or status information for the operator to verify that the bridge is running on the desktop. |
| FR-4.5 | The fleet manager must support remote agent control by creating an authenticated tunnel from the operator machine to the desktop-local `ai-agent-bridge` endpoint. |
| FR-4.6 | `ai-agent-bridge` must not be exposed directly to the public internet in v1. |

### FR-5 — Base toolchain

| ID | Requirement |
| --- | --- |
| FR-5.1 | Every desktop must include `git`. |
| FR-5.2 | Every desktop must include `docker`. |
| FR-5.3 | Every desktop must include `nvim`. |
| FR-5.4 | Every desktop must include `tmux`. |
| FR-5.5 | The base toolchain must be present before a desktop is reported ready. |

### FR-6 — Workspace and repository policy

| ID | Requirement |
| --- | --- |
| FR-6.1 | A desktop may contain multiple Git repositories in its workspace. |
| FR-6.2 | All repositories assigned to a desktop must belong to a single GitHub organization or a single GitHub personal account. |
| FR-6.3 | Desktop creation must require the caller to specify the GitHub owner boundary for that desktop. |
| FR-6.4 | The system must reject attempts to attach or clone repositories from a different owner into an existing desktop-managed workspace. |
| FR-6.5 | Automatic checkout into a standard workspace location must be supported during desktop creation when repositories are provided. |

### FR-7 — Persistence

| ID | Requirement |
| --- | --- |
| FR-7.1 | Desktop filesystem state must persist across stop/start operations. |
| FR-7.2 | Installed tools, checked-out repositories, editor state, and agent workspace artifacts must remain available after restart unless explicitly deleted by the operator. |
| FR-7.3 | Persistence semantics apply to normal desktop lifecycle operations, not to terminated desktops. |

### FR-8 — External access posture

| ID | Requirement |
| --- | --- |
| FR-8.1 | v1 may optimize for a single operator workflow. |
| FR-8.2 | The system architecture must not assume that only one human can ever access the application surface. |
| FR-8.3 | Future authenticated access for external users must be possible without redesigning the desktop lifecycle model. |

### FR-9 — Pre-baked AMI with Packer

| ID | Requirement |
| --- | --- |
| FR-9.1 | The system must support building per-region machine images with the base toolchain pre-installed using Packer. |
| FR-9.2 | The AMI build process must produce identical toolchain versions across all supported regions. |
| FR-9.3 | Built AMI IDs must be persisted in operator config (`config.yaml`) and used by subsequent desktop creates. |
| FR-9.4 | Cloud-init user-data must be reduced to runtime-only concerns: secret injection, WireGuard configuration, workspace setup, and repository cloning. |
| FR-9.5 | The base AMI must be built from Ubuntu 24.04 LTS (Noble) and pre-install: `docker`, `git`, `nvim`, `tmux`, `wireguard-tools`, `uv`, `go`, `brew` (linuxbrew), `ai-agent-bridge` (pinned version). |
| FR-9.6 | A CLI command `ai-desktops ami build` must invoke Packer and automatically update `config.yaml` with the resulting AMI IDs per region. |
| FR-9.7 | Desktop creation must prefer pre-baked AMI IDs from config over the hardcoded default Ubuntu AMI map. |

### FR-10 — WireGuard VPN support

| ID | Requirement |
| --- | --- |
| FR-10.1 | The system must support operating desktops behind a WireGuard VPN when enabled in config. |
| FR-10.2 | WireGuard tools must be installed in the base AMI; runtime configuration is injected at first boot via cloud-init. |
| FR-10.3 | The WireGuard server private key must be generated at desktop create time and stored in AWS SSM Parameter Store as a SecureString (never in config YAML). |
| FR-10.4 | The CLI must provide peer management commands: `ai-desktops wireguard add-peer`, `remove-peer`, `list-peers`, and `show-config`. |
| FR-10.5 | Each peer configuration must be displayable as a QR code for easy mobile client onboarding. |
| FR-10.6 | Peer private keys must be generated once at add-peer time and displayed to the operator; they must not be stored by `ai-desktops`. |
| FR-10.7 | For running desktops, adding or removing a peer must apply the change live (without reboot) when the `--desktop` flag is provided. |
| FR-10.8 | WireGuard peer list must be managed via the config system and persisted in `config.yaml`. |
| FR-10.9 | The health check system must verify WireGuard server status and connectivity when enabled. |

---

## Security Requirements

### SR-1 — Desktop access security

**When WireGuard is disabled** (legacy operator CIDR mode):
- SSH (port 22) is exposed via security group rules, restricted to configured `operator_cidr`.
- noVNC HTTPS (port 8443) is accessible from anywhere via Route53 DNS and TLS.
- HTTP port 80 is available for ACME challenges and temporary testing.

**When WireGuard is enabled:**
- SSH (port 22) access is restricted to WireGuard server network only (e.g., `10.99.0.0/24` if WireGuard subnet is `10.99.0.0/24`).
- noVNC HTTPS (port 8443) is accessible only from WireGuard tunnel network.
- HTTP port 80 is not exposed publicly (ACME DNS-01 challenge avoids HTTP dependency).
- WireGuard server endpoint (UDP port 51820 by default) is reachable from `0.0.0.0/0` to allow initial handshake before tunnel is established.
- All operator access to desktop resources must route through authenticated WireGuard VPN.
- `operator_cidr` becomes optional when WireGuard is enabled; it is not used for security group rules in that mode.

### SR-2 — Secret handling

- Secrets must not be baked into machine images.
- Agent provider credentials must be injected at runtime through a secure secret-management mechanism.
- Secrets must not be emitted in fleet-manager logs, agent logs, or browser-access bootstrap output.
- WireGuard server private keys are generated per-desktop at creation time and stored in AWS SSM Parameter Store as SecureString, never in config files or logs.
- WireGuard peer (operator) private keys are generated on-demand and displayed once to the operator; `ai-desktops` does not store them.

### SR-3 — Workspace boundary

- Repo policy enforcement must prevent a managed desktop workspace from mixing repositories from multiple GitHub owners.
- Desktop provisioning and repo bootstrap actions must validate the requested owner boundary before cloning repositories.

### SR-4 — Future user access

- External-user access, when implemented, must layer on top of authenticated application access rather than direct unauthenticated desktop URLs.
- Full RBAC is deferred, but the architecture must leave room for per-user or per-role authorization later.

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
- `ai-agent-bridge` installation and runtime enablement
- remote agent control through a CLI-managed tunnel to `ai-agent-bridge`
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
4. `ai-agent-bridge` is installed, running, and available as the desktop’s standard agent runtime.
5. The operator can control a desktop's AI agents remotely through a CLI-managed tunnel to `ai-agent-bridge`.
6. A desktop can be created with multiple repositories checked out into its workspace when all repositories belong to the same GitHub organization or personal account.
7. A request that mixes repositories from different GitHub owners is rejected before the desktop is reported ready.
8. After stopping and restarting a desktop, the workspace contents and prior desktop state remain present.
9. After terminating a desktop, the desktop and its persisted state are no longer recoverable through normal lifecycle operations.

---

## Build Risks and Implementation Decisions

The following items are likely to cause implementation churn or security gaps if they remain implicit.

### State ownership

The PRD requires persistent desktops, but persistence must be defined at the infrastructure layer. The v1 architecture should treat the EC2 instance plus its root EBS volume as the persisted desktop unit. Stop/start preserves the root volume. Terminate destroys it unless snapshot support is explicitly added later.

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

`ai-agent-bridge` needs provider credentials for Codex, Claude, and Gemini. The PRD intentionally forbids baking secrets into images, but the implementation still needs a delivery path. v1 should use AWS SSM Parameter Store or Secrets Manager references passed during provisioning, then render local bridge environment files on the desktop with restrictive permissions.

### Repository authentication

Repo cloning requires GitHub credentials. v1 uses a fine-scoped GitHub PAT stored in AWS secret storage and scoped to the configured owner. A GitHub App should replace the PAT after the first smoke path is working.

### Remote agent control

The operator needs remote control of agents running inside each desktop. v1 should keep `ai-agent-bridge` bound to localhost on the desktop and create a short-lived SSH or AWS SSM port-forward from the operator machine when remote control is requested. A private-network bridge endpoint with mTLS can be added later if a long-running control plane needs direct access.

### Desktop readiness

The PRD says the desktop is reported ready after provisioning, but readiness must be concrete. v1 readiness should require SSH reachable, HTTPS noVNC reachable, `novnc-auth` token generation working, Docker active, `ai-agent-bridge` active, and the requested repositories present under the workspace.

### Elementary support

`novnc-desktop` treats Elementary/Pantheon support as best-effort. This PRD requires Elementary as the standard desktop, so implementation must either pin a known-good Ubuntu/Pantheon combination or downgrade the requirement to a preferred desktop with Openbox fallback. Leaving this unresolved can break automated provisioning.

### Cost and cleanup

Persistent desktops imply ongoing cloud cost. Failed desktop creation should leave the instance running by default for debugging until `doctor` is useful. Automated idle shutdown, budgets, and cost alerts can follow after MVP.

### Pre-baked AMI provisioning

Desktop provisioning performance is critical for interactive operator workflows. v1 uses cloud-init to install the baseline toolchain (docker, git, nvim, tmux, novnc-desktop, ai-agent-bridge) at boot time, which can add 5–10 minutes to desktop creation. After initial MVP, `ai-desktops` supports pre-baked AMIs built with Packer to include all toolchain components pre-installed, reducing boot time to minutes and eliminating package installation failures.

The `ai-desktops ami build` command invokes Packer to build per-region AMIs containing the baseline toolchain with pinned versions. The resulting AMI IDs are stored in `config.yaml`. When creating a desktop with a pre-baked AMI, cloud-init is reduced to runtime-only steps: TLS certificate generation via certbot, nginx reverse-proxy configuration, GitHub PAT retrieval, and repository cloning. This approach keeps the stateless parts (application installs) in the AMI and the instance-specific parts (certs, secrets, repos) in cloud-init.

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
- Multi-desktop WireGuard mesh topology (peer-to-peer desktop connectivity)
- WireGuard server separate from desktop instance (shared bastion topology)
