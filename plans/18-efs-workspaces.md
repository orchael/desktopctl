# Plan 18 — Optional EFS-Backed Workspaces

## Objective

Add an opt-in EFS workspace mode that lets `/workspace` survive desktop termination. Local root EBS
remains the default. Each environment, such as `dev`, gets one shared encrypted EFS file system with
AWS Backup enabled by default. Operators can create named EFS workspaces such as
`orchael-factory-dev` and `orchael-factory-update`; each workspace is isolated under the environment
file system and can attach to one active desktop at a time.

The core behavior is separation of lifecycle:

- desktop: EC2 instance, root EBS, hostname, noVNC, SSH, bridge runtime
- workspace: EFS file system or access point, repo checkout directory, agent artifacts under `/workspace`

Only one active desktop may attach a given EFS workspace at a time.

## Depends On

- FR-1 desktop lifecycle
- FR-6 workspace and repository policy
- FR-7 persistence
- Plan 06 Pulumi desktop stack
- Plan 08 cloud-init bootstrap
- Plan 09 readiness and doctor

---

## Proposed Operator Workflow

### Default local workspace, unchanged

```bash
ai-desktops create \
  --name orchael-factory-dev \
  --github-owner orchael \
  --repo orchael/ai-desktops
```

This keeps `/workspace` on the desktop root EBS volume. `terminate` destroys the workspace with the
desktop, matching current behavior.

### Create a retained EFS workspace

```bash
ai-desktops workspace create \
  --name orchael-factory-dev \
  --github-owner orchael \
  --repo orchael/ai-desktops \
  --repo orchael/other-repo
```

The command creates or registers an EFS-backed workspace. It records the intended GitHub owner and
initial repo set, provisions the backing access point or directory immediately, but does not start a
desktop.

### Change a detached workspace repo set

```bash
ai-desktops workspace add-repo orchael-factory-dev --repo orchael/new-service
ai-desktops workspace remove-repo orchael-factory-dev --repo orchael/old-service
```

Repo membership changes are metadata-only operations and are allowed only while the workspace is
detached. Removing a repo from metadata does not delete the checkout directory or any retained files
from EFS.

### Attach an existing EFS workspace to a desktop

```bash
ai-desktops create \
  --name orchael-factory-dev \
  --github-owner orchael \
  --workspace-mode efs \
  --workspace-name orchael-factory-dev
```

Create fails before provisioning EC2 if `orchael-factory-dev` is already attached to another
non-terminated desktop.

### Maintain a second workspace for the same repos

```bash
ai-desktops workspace create \
  --name orchael-factory-update \
  --github-owner orchael \
  --repo orchael/ai-desktops \
  --repo orchael/other-repo

ai-desktops create \
  --name orchael-factory-update \
  --github-owner orchael \
  --workspace-mode efs \
  --workspace-name orchael-factory-update
```

The two workspaces clone the same repos but keep separate working trees and artifacts.

### Replace compute while retaining workspace

```bash
ai-desktops terminate orchael-factory-dev

ai-desktops create \
  --name orchael-factory-dev-2 \
  --github-owner orchael \
  --workspace-mode efs \
  --workspace-name orchael-factory-dev
```

The first command removes the desktop. The second command attaches the retained EFS workspace to new
compute.

---

## CLI Surface

### Desktop create additions

```bash
ai-desktops create \
  --name <desktop-name> \
  --workspace-mode local|efs \
  --workspace-name <workspace-name>
```

- `--workspace-mode local` is the default.
- `--workspace-name` is required when `--workspace-mode efs`.
- `--workspace-name` is rejected when `--workspace-mode local` unless the final design allows a
  local label only.
- `--name` is a desktop display name and stable operator handle. The generated `desktop_id` remains
  the fleet primary key unless a later design replaces it deliberately.

### Workspace command group

```bash
ai-desktops workspace create --name <name> --github-owner <owner> [--repo <repo>...]
ai-desktops workspace list [--github-owner <owner>] [--all]
ai-desktops workspace status <name>
ai-desktops workspace add-repo <name> --repo <repo>...
ai-desktops workspace remove-repo <name> --repo <repo>...
ai-desktops workspace delete <name>
ai-desktops workspace detach <name> --force
```

`detach --force` is an operator recovery command for stale metadata after failed provisioning or
manual AWS cleanup. It must not unmount a live desktop by itself.

---

## AWS Architecture

Confirmed implementation:

1. Foundation stack creates one shared EFS file system per environment:
   - encrypted EFS file system, for example the `dev` environment file system
   - AWS Backup enabled by default
   - EFS security group allowing NFS from desktop security group
   - mount targets in the private subnets used by desktops
2. Workspace resources are managed separately from desktop resources:
   - either by a workspace Pulumi stack namespace, one stack per named workspace
   - or by the foundation stack plus DynamoDB metadata if the implementation proves simpler
3. Each workspace stack creates:
   - an access point or directory under the shared environment EFS file system
   - POSIX ownership matching `ubuntu`
   - tags for environment, owner, workspace name, repo fingerprint, and managed-by
4. Desktop stack receives workspace attachment config:
   - storage mode
   - EFS file system ID
   - access point ID or mount path
   - mount target security group
5. Cloud-init installs/uses `amazon-efs-utils`, mounts EFS at `/workspace`, then runs the existing
   repo checkout logic.

The chosen topology is one EFS file system per environment, not one per workspace. This keeps AWS
resource count and cost under control while still allowing multiple isolated named workspaces through
access points or directories.

---

## Metadata Model

Add workspace records to the fleet metadata store.

```text
workspace_name          string, primary operator handle
workspace_mode          efs
environment             dev|prod
github_owner            string
repos                   []string
repo_fingerprint        string
efs_file_system_id      string
efs_access_point_id     string
mount_path              /workspace
state                   available|attaching|attached|detaching|deleting|failed|deleted
attached_desktop_id     string, optional
attached_desktop_name   string, optional
created_at              RFC3339
updated_at              RFC3339
last_failure_phase      string, optional
last_failure_message    string, optional
```

Desktop records should gain:

```text
desktop_name            string, optional but unique when present
workspace_mode          local|efs
workspace_name          string, optional
workspace_attachment    attached|detached|failed, optional
```

Attachment reservation should be conditional:

- create can reserve only when workspace state is `available` or detached
- create must fail when `attached_desktop_id` points at a non-terminated desktop
- terminate clears attachment only after desktop destroy succeeds or after recovery confirms no live
  compute remains

---

## Readiness and Doctor

For local mode, existing checks remain valid.

For EFS mode, add checks:

- `/workspace` is a mounted EFS filesystem
- mount uses the expected file system ID or access point
- `/workspace` is writable by `ubuntu`
- each requested repo exists under `/workspace/<repo>`
- `df -T /workspace` reports sufficient free space and expected filesystem type

`doctor` should display storage mode and workspace name so operators can distinguish local
persistence from retained EFS persistence.

---

## Failure Modes

- Workspace attached elsewhere: fail before EC2 provisioning.
- EFS mount target missing or not reachable: desktop enters failed state with phase `workspace-mount`.
- Repo owner mismatch against workspace metadata: fail before provisioning.
- Repo set differs from workspace metadata: fail clearly; EFS workspaces are bound to the current
  exact repo set in workspace metadata.
- Repo add/remove requested while attached: fail clearly; repo membership can change only after the
  desktop is terminated or detached through recovery.
- Terminate succeeds but metadata detach update fails: desktop is terminated, workspace remains in
  stale attached state; operator can run `workspace detach --force` after confirming no EC2 instance
  is using it.
- Workspace delete requested while attached: fail.

---

## Confirmed Decisions

1. Use one shared EFS file system per environment, such as `dev`.
2. Bind each EFS workspace to the GitHub owner and current exact repo set in workspace metadata.
3. Keep the EFS attachment lock while a desktop is stopped; release it only when the desktop is
   terminated or an operator runs recovery after confirming no live compute uses the mount.
4. Always retain EFS workspaces on desktop termination.
5. Require desktop names to be unique among non-terminated desktops in the same environment.
6. Provision EFS workspace resources immediately during `workspace create`.
7. Enable AWS Backup by default for the environment EFS file system.

---

## Files Expected To Change During Implementation

| File | Change |
| --- | --- |
| `PRD.md` | FR-12 requirements and acceptance criteria |
| `plans/18-efs-workspaces.md` | Implementation plan and confirmed workflow decisions |
| `cmd/ai-desktops/cmd/create.go` | `--name`, `--workspace-mode`, and `--workspace-name` validation |
| `cmd/ai-desktops/cmd/workspace.go` | New workspace command group |
| `internal/store` | Workspace record model and conditional attachment updates |
| `internal/desktop` | Desktop name and workspace storage config in records |
| `internal/pulumi` | Workspace stack and desktop stack config plumbing |
| `infra/pulumi/foundation` | EFS security group and optional shared mount target plumbing |
| `infra/pulumi/desktop` | EFS mount config passed to cloud-init |
| `internal/provision/cloudinit.go` | Mount EFS at `/workspace` before repo checkout |
| `internal/health` | Workspace storage mode checks |
| `README.md` and `docs/` | Operator workflow and cleanup docs |

---

## Verification

```bash
go test ./internal/store ./internal/desktop ./internal/pulumi ./internal/provision ./internal/health
go test ./cmd/ai-desktops/cmd -run 'Workspace|Create'
go run ./cmd/ai-desktops create --workspace-mode local --preview
go run ./cmd/ai-desktops workspace create --name orchael-factory-dev --github-owner orchael --repo orchael/ai-desktops --preview
go run ./cmd/ai-desktops create --name orchael-factory-dev --workspace-mode efs --workspace-name orchael-factory-dev --github-owner orchael --preview
```

Live acceptance should create an EFS workspace, attach a desktop, write a sentinel file under
`/workspace`, terminate the desktop, attach a replacement desktop, and verify the sentinel file and
repos remain present.
