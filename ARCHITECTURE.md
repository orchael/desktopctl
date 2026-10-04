# Architecture - ai-desktops

> This is the original single-repository design record. For the audited code and the desktopctl/Desktops split, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). Compatibility names in this document describe existing deployments.

## Purpose

`ai-desktops` is a CLI-driven fleet manager for persistent remote AI coding desktops. The implementation keeps the control surface small: a Go CLI owns lifecycle operations, Pulumi owns AWS infrastructure, DynamoDB stores fleet metadata, and each desktop runs a standard Ubuntu-based runtime with `novnc-desktop`, `bridgectl`, and developer tools.

The design optimizes for a single operator in v1 while preserving a clean path to authenticated external-user access later.

---

## System Context

```text
Operator terminal
  |
  | ai-desktops CLI
  v
Go CLI
  |
  | Pulumi Automation API
  | S3 DIY backend for Pulumi state
  | DynamoDB for fleet metadata
  v
AWS account
  |
  | EC2 instance + EBS root volume
  | Security group
  | IAM instance profile
  | Route53 record
  | SSM/Secrets Manager secret reads
  | DynamoDB desktop records
  v
Managed desktop host
  |
  | noVNC HTTPS desktop
  | SSH for debugging
  | bridgectl
  | Vite desktop webapp
  | workspace repositories
```

---

## Major Components

### Go CLI

The Go CLI is the primary operator interface. It should be implemented as a single binary named `ai-desktops`.

Initial commands:

| Command | Purpose |
| --- | --- |
| `ai-desktops bootstrap` | Create or verify the S3 Pulumi backend bucket and DynamoDB fleet table. |
| `ai-desktops init-foundation` | Deploy shared AWS foundation resources such as network, IAM, DNS prerequisites, and secret namespaces. |
| `ai-desktops create` | Create one persistent desktop and run first-boot provisioning. |
| `ai-desktops list` | List known desktops from DynamoDB and reconcile with AWS tags when requested. |
| `ai-desktops status <id>` | Show lifecycle state, URLs, SSH target, Pulumi stack name, and readiness checks. |
| `ai-desktops url <id>` | Mint or retrieve a browser access URL for the desktop. |
| `ai-desktops ssh <id>` | Open an SSH session to the desktop. |
| `ai-desktops agent <id> ...` | Control bridge-managed AI agent sessions through a CLI-managed tunnel. |
| `ai-desktops stop <id>` | Stop the EC2 instance while preserving the root EBS volume. |
| `ai-desktops start <id>` | Start a stopped EC2 instance and refresh access details. |
| `ai-desktops terminate <id>` | Destroy the desktop stack and its persistent state. |
| `ai-desktops doctor <id>` | Run desktop health checks over AWS, SSH, and HTTPS. |

Recommended package boundaries:

| Package | Responsibility |
| --- | --- |
| `cmd/ai-desktops` | CLI command wiring and output formatting. |
| `internal/config` | Operator config, AWS region, backend bucket, DNS zone, default profile. |
| `internal/backend` | S3 backend bootstrap and Pulumi backend URL configuration. |
| `internal/pulumi` | Pulumi Automation API wrapper, stack naming, config, preview, up, destroy, output parsing. |
| `internal/awsx` | AWS SDK helpers for EC2, S3, DynamoDB, SSM, Secrets Manager, Route53, and tags. |
| `internal/store` | DynamoDB fleet metadata repository. |
| `internal/desktop` | Desktop model, lifecycle transitions, readiness orchestration. |
| `internal/repo` | GitHub owner validation and repository input normalization. |
| `internal/provision` | SSH-based post-provision checks and bootstrap command execution. |
| `internal/health` | noVNC, bridge, Docker, SSH, workspace, and repo health checks. |
| `internal/tunnel` | SSH and SSM port-forward management for bridge access. |
| `internal/agent` | Bridge client integration for remote agent session control. |

The CLI may use Cobra for command structure, but lifecycle logic should live below `cmd/` so tests can exercise it without shelling out.

---

## Pulumi Architecture

Pulumi provisions AWS resources. The Go CLI should use Pulumi Automation API so lifecycle commands can create, update, preview, and destroy stacks programmatically.

Pulumi documents Automation API as a way to embed Pulumi operations in applications instead of manually shelling out to `pulumi up`, `pulumi preview`, and `pulumi destroy`: <https://www.pulumi.com/docs/iac/automation-api/>.

### Backend

All Pulumi stacks use an S3 DIY backend.

Backend requirements:

- S3 bucket with versioning enabled.
- Server-side encryption enabled.
- Public access blocked.
- Pulumi state stored under a project-specific prefix.
- Pulumi stack locking left enabled by default.
- Pulumi secrets encrypted with a real provider, preferably AWS KMS, not only a local passphrase.

Pulumi documents S3 DIY backends with `pulumi login s3://<bucket-name>` and stores stack state under a `.pulumi` prefix. Pulumi DIY backends include state locking, history tracking, project-scoped stacks, and secrets encryption support: <https://www.pulumi.com/docs/reference/state/>.

### Project layout

```text
infra/
  pulumi/
    foundation/
      Pulumi.yaml
      main.go
    desktop/
      Pulumi.yaml
      main.go
```

Both Pulumi programs should be written in Go so the infrastructure language matches the CLI and can share typed config structs where appropriate.

### Stack topology

Use separate Pulumi stacks for shared foundation resources and each desktop:

```text
ai-desktops/foundation
ai-desktops/desktop-<desktop_id>
```

This keeps desktop lifecycle operations isolated. Creating, updating, stopping, starting, or destroying one desktop should not require changing the whole fleet.

### Foundation stack

The foundation stack owns shared infrastructure:

- VPC or selected existing VPC reference
- public subnet selection
- common security group templates
- IAM instance profile for desktop hosts
- Route53 hosted zone integration for `desktops.orchael.com` and `desktops.orchael.dev`
- SSM/Secrets Manager parameter namespace
- DynamoDB fleet table

The foundation stack should not own individual desktops.

### Desktop stack

Each desktop stack owns exactly one desktop:

- EC2 instance
- root EBS volume
- security group rules or security group attachment
- IAM instance profile attachment
- Elastic IP or public IP strategy
- Route53 record for the selected environment zone
- cloud-init user data or rendered bootstrap script
- tags for owner, desktop ID, GitHub owner, lifecycle, and managed-by

The desktop stack exports:

- desktop ID
- instance ID
- desktop hostname
- noVNC base URL
- SSH connection string
- workspace path
- GitHub owner boundary

### DNS zones

v1 requires Route53-backed DNS.

| Environment | Zone |
| --- | --- |
| Production | `desktops.orchael.com` |
| Local/development | `desktops.orchael.dev` |

Per-desktop hostnames should be created beneath the selected zone:

```text
<desktop_id>.desktops.orchael.com
<desktop_id>.desktops.orchael.dev
```

The operator is responsible for creating the Route53 hosted zones before the foundation stack is initialized. The foundation stack should look up the hosted zone by name and fail clearly if it is missing.

---

## DynamoDB Fleet Store

Pulumi state remains the source of truth for cloud resources. DynamoDB is the operational index for the fleet.

Table name:

```text
ai-desktops-fleet-<env>   (e.g. ai-desktops-fleet-dev, ai-desktops-fleet-test, ai-desktops-fleet-prod)
```

Primary key:

| Field | Type | Purpose |
| --- | --- | --- |
| `desktop_id` | string | Stable desktop identifier. |

Recommended attributes:

| Attribute | Purpose |
| --- | --- |
| `stack_name` | Pulumi stack name for the desktop. |
| `github_owner` | Required owner boundary for all repos in the workspace. |
| `lifecycle_state` | Last known state: `creating`, `ready`, `stopped`, `unhealthy`, `failed`, `terminating`, `terminated`. |
| `instance_id` | EC2 instance ID. |
| `hostname` | DNS name or public address. |
| `novnc_url` | Last known base URL. |
| `ssh_target` | Last known SSH target. |
| `readiness` | Last readiness-check summary. |
| `created_at` | Creation timestamp. |
| `updated_at` | Last update timestamp. |

Optional indexes:

- GSI on `lifecycle_state` for cleanup and dashboards.
- GSI on `github_owner` for owner-scoped list views.

The CLI should reconcile DynamoDB with AWS tags and Pulumi stack outputs during `status` and `doctor`. If DynamoDB and live infrastructure disagree, Pulumi outputs and AWS live state win.

---

## Provisioning Model

Provisioning has two layers.

### Infrastructure provisioning

Pulumi creates the instance and passes minimal bootstrap data through cloud-init.

Cloud-init responsibilities:

- install OS package prerequisites
- create the primary desktop user
- prepare `/workspace`
- install or invoke Ansible role dependencies
- install `novnc-desktop`
- set `desktop_type=elementary`
- install `git`, `docker`, `nvim`, and `tmux`
- install and enable `bridgectl`
- install the on-desktop webapp bundle
- configure systemd services

### Readiness verification

The CLI must not trust instance creation alone. After Pulumi completes the desktop stack update, the CLI runs readiness checks:

- EC2 instance is running.
- SSH accepts the configured operator key.
- noVNC HTTPS endpoint responds.
- `novnc-desktop-url` can mint a valid browser URL.
- Docker service is active.
- `bridgectl` service is active.
- required tools exist on `PATH`.
- requested repositories exist under `/workspace`.
- every requested repository matches the configured GitHub owner.

Only after these checks pass should `create` mark the DynamoDB desktop record as `ready`.

---

## Desktop Runtime

Each desktop host uses a standard runtime contract.

| Path or service | Purpose |
| --- | --- |
| `/workspace` | Persistent repository workspace. |
| `/opt/ai-desktops` | Local runtime assets managed by this project. |
| `/opt/bridgectl` | Provider CLI runtime assets. |
| `novnc-desktop` services | Browser desktop substrate and access-token service. |
| `bridgectl.service` | Agent process supervisor. |
| `docker.service` | Container runtime for builds and app dependencies. |
| `ai-desktop-web.service` | Optional local service for the Vite-built desktop webapp. |

The root EBS volume is the persistence boundary for MVP. A later architecture can move workspace state to a separate EBS volume if snapshot and clone workflows become important.

---

## On-Desktop Webapp

Any on-desktop webapp should be built with Vite.

Purpose:

- provide a local browser landing surface for the desktop
- show workspace repositories
- show bridge/provider status
- provide links to terminal, noVNC, local app ports, and logs
- later host external-user experience primitives

Recommended layout:

```text
apps/
  desktop-web/
    src/
    package.json
    vite.config.ts
```

Build and deployment:

- Vite builds static assets during packaging or host provisioning.
- Assets are copied to `/opt/ai-desktops/desktop-web/dist`.
- A small local HTTP service or Nginx location serves the built app.
- The webapp talks only to local desktop services or a same-host API shim.

The desktop webapp is not the primary fleet control plane in v1. Fleet lifecycle remains in the Go CLI.

## Kubernetes Control Plane

The repository also contains a separate Kubernetes-hosted control plane for operators who want a persistent fleet surface outside individual desktops. The public Next.js application in `apps/control-plane-web` owns Google authentication, PostgreSQL organizations, memberships, and RLS. It proxies organization-scoped fleet requests to the private Go API in `cmd/control-plane` and `internal/controlplane`; each DynamoDB desktop carries the same organization UUID.

The control plane runs well on DOKS. Since DOKS cannot use AWS IRSA, AWS access is bootstrapped by `infra/pulumi/control-plane-access`, which creates a narrow IAM user and an assumable role. The Kubernetes Secret holds only the user access key and role-assumption settings; runtime fleet calls use temporary STS credentials from the role.

The first server implementation supports health, readiness, fleet list, desktop detail, refresh, start, and stop. Create and terminate remain CLI-first until the Pulumi lifecycle code is extracted into a shared service that both the CLI and HTTP API can call safely.

---

## Repository Boundary

Each desktop has a configured GitHub owner boundary:

```text
github_owner = "markcallen"
```

Valid repository inputs:

```text
github.com/markcallen/app-one
github.com/markcallen/app-two
```

Invalid repository inputs for that desktop:

```text
github.com/other-owner/app-three
```

Validation should happen before Pulumi updates the desktop stack. A second validation should run on the host before cloning so manual or malformed inputs cannot bypass the policy.

For v1, repository credentials should come from an AWS secret reference configured at create time. The secret must be scoped to the GitHub owner whenever possible.

The first smoke implementation should use a fine-scoped GitHub PAT stored in AWS SSM Parameter Store or Secrets Manager. A GitHub App should replace the PAT once the provisioning flow is stable.

---

## Security Architecture

### Network access

- noVNC is exposed only through HTTPS.
- Raw VNC ports stay bound to localhost on the desktop.
- SSH is restricted to the operator source CIDR where practical.
- `bridgectl` binds to localhost in v1.
- Remote bridge access happens through a CLI-managed SSH or SSM tunnel.
- Security groups must not expose Docker, bridge, CDP, or local development ports publicly by default.

### Identity and secrets

- AWS credentials are supplied to the CLI through the operator environment or profile.
- Pulumi backend credentials must not be hardcoded in project files.
- Pulumi config secrets should use AWS KMS-backed encryption for the S3 DIY backend.
- Agent provider keys are stored in AWS SSM Parameter Store or Secrets Manager.
- GitHub credentials are stored in AWS SSM Parameter Store or Secrets Manager.
- Desktop provisioning retrieves secrets using the instance IAM role.
- Rendered secret files on the desktop use restrictive ownership and permissions.

### noVNC access

The MVP should rely on `novnc-desktop` signed access URLs. The CLI `url` command should SSH to the host or call a private endpoint to mint a fresh access URL.

External-user access later should wrap this with application-level authentication rather than exposing permanent desktop URLs.

### Remote agent control

The v1 bridge access mode is `tunnel`.

`bridgectl` listens on the desktop at `127.0.0.1:9445`. When the operator runs an agent command, the CLI establishes a short-lived tunnel from the operator machine to that desktop-local bridge endpoint and then uses the bridge client through the local forwarded port.

Preferred tunnel order:

1. AWS SSM Session Manager port forwarding, when the instance role and local AWS CLI support it.
2. SSH local port forwarding as the fallback.

SSM tunnel support requires the desktop instance profile to allow Session Manager access and the desktop image to have a working SSM agent. SSH tunnel support requires the operator SSH key and inbound SSH access from the operator source.

Example flow:

```text
ai-desktops agent desk-123 start --provider codex --repo app-one
  |
  | ensure desktop is running
  | open local tunnel localhost:<ephemeral> -> desktop:127.0.0.1:9445
  | call bridgectl through the local forwarded port
  | stream session events to the CLI
  v
operator terminal
```

This preserves remote control without exposing the bridge to the public internet. A later `private` bridge access mode can expose the bridge on a VPC-only listener with mTLS and short-lived JWTs if a persistent control plane needs direct network access.

---

## Lifecycle Semantics

### Create

1. Validate CLI input and GitHub owner boundary.
2. Ensure the S3 backend bucket and DynamoDB fleet table exist.
3. Ensure foundation stack outputs exist.
4. Create a DynamoDB record in `creating` state.
5. Create or select Pulumi stack `ai-desktops/desktop-<desktop_id>`.
6. Set stack config for region, profile, owner boundary, repo list, secret references, DNS, and desktop profile.
7. Run Pulumi preview by default.
8. Run Pulumi up through Automation API.
9. Wait for SSH.
10. Run readiness verification.
11. Update DynamoDB with outputs and readiness status.
12. Print desktop ID, URL, and SSH target.

### Stop

Stop the EC2 instance through the AWS API. Do not destroy Pulumi state. Do not destroy the EBS volume. Update DynamoDB to `stopped` after AWS confirms the instance stopped.

### Start

Start the EC2 instance, refresh public network outputs if needed, update DNS if needed, rerun readiness checks, and update DynamoDB.

### Terminate

Run Pulumi destroy for the desktop stack. Termination destroys the EC2 instance and root EBS volume. Snapshot behavior is out of scope until explicitly added. Mark the DynamoDB record as `terminated` or delete it only after destroy succeeds.

---

## Observability

Minimum v1 observability:

- CLI structured status output.
- Pulumi preview/update event output stored under a local run directory.
- DynamoDB lifecycle and readiness fields.
- Desktop bootstrap logs available through SSH.
- `doctor` command for repeatable health checks.
- AWS tags on all managed resources.

Recommended local run directory:

```text
~/.ai-desktops/runs/<run_id>/
```

The CLI should support `--json` for status and list commands so later web or automation layers can consume the same lifecycle data.

---

## Failure Handling

Provisioning failures should leave enough information for recovery.

Rules:

- If Pulumi up fails, the CLI reports the Pulumi error, stack name, and run directory.
- If Pulumi succeeds but readiness fails, the desktop remains in `provisioning_failed` or `unhealthy` state for debugging.
- The CLI should not auto-destroy failed desktops unless the operator passes an explicit cleanup flag.
- `doctor` should work against partially provisioned desktops.
- DynamoDB should record the last failure phase and message.

---

## Does Pulumi Fix Any Issues?

Pulumi fixes or reduces some problems, but it does not remove the hard parts of provisioning desktops.

What improves:

- The Go CLI can drive infrastructure through Pulumi Automation API instead of shelling out to an external Terraform CLI wrapper.
- Infrastructure code can be written in Go, matching the CLI and enabling shared validation types.
- Per-desktop stacks are natural and easy to create programmatically.
- Pulumi DIY S3 backends include locking and history tracking, so the architecture no longer needs Terraform-specific lockfile handling.
- DynamoDB becomes a clear operational store for fleet listing and lifecycle state instead of using infrastructure state as a query database.

What still needs design:

- The S3 backend bucket still has to be bootstrapped before S3-backed stacks can use it.
- Pulumi state is still not a product database, so DynamoDB is still needed for fleet metadata.
- Secret delivery details, readiness implementation, Elementary reliability, and cloud cost controls still need implementation detail.
- Pulumi Automation API still requires the Pulumi CLI to be installed and available on `PATH`.

---

## Implementation Milestones

### Milestone 1 - Repository and CLI skeleton

- Go module
- `ai-desktops` CLI skeleton
- config file support
- JSON output mode
- unit tests for config and repository owner validation

### Milestone 2 - Pulumi backend, DynamoDB, and foundation

- `bootstrap` command
- S3 Pulumi backend bucket creation or verification
- DynamoDB fleet table creation or verification
- foundation Pulumi stack
- Pulumi backend URL configuration
- smoke test for creating/selecting a stack against the S3 backend

### Milestone 3 - Single desktop create

- desktop Pulumi stack
- EC2 instance provisioning
- SSH readiness check
- stop/start/terminate commands
- AWS tagging
- DynamoDB lifecycle updates
- Route53 hostnames under `desktops.orchael.com` or `desktops.orchael.dev`

### Milestone 4 - Runtime bootstrap

- `novnc-desktop` installation with Elementary config
- base toolchain installation
- `bridgectl` installation
- `bridgectl` localhost binding
- CLI-managed SSM or SSH tunnel for bridge control
- readiness checks for browser URL, Docker, bridge, and tools

### Milestone 5 - Workspace policy

- GitHub owner validation before Pulumi update
- repository checkout during provisioning
- host-side owner validation before clone
- rejection tests for mixed-owner repo input

### Milestone 6 - Vite desktop webapp

- Vite app scaffold
- static build deployment to desktop
- bridge and workspace status view
- noVNC/local-service routing decision documented

---

## Review Decisions

1. v1 requires Route53 DNS using `desktops.orchael.com` for production and `desktops.orchael.dev` for local/development.
2. The root EBS volume is the persistence boundary for MVP.
3. v1 uses a fine-scoped GitHub PAT for the first smoke path, stored in AWS secret storage; GitHub App support comes later.
4. `bridgectl` remains localhost-bound, and remote control uses a short-lived SSH or SSM tunnel.
5. Failed desktop creation leaves the EC2 instance running by default for debugging.
6. Pulumi preview runs by default before create/update.
