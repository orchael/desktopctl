# Task 06 - Pulumi Desktop Stack

## Objective

Create the Pulumi desktop program for one persistent desktop per stack.

## Depends On

- Task 04
- Task 05

## Scope

Create:

- `infra/pulumi/desktop/Pulumi.yaml`
- `infra/pulumi/desktop/main.go`
- desktop stack config types
- stack output parsing in the CLI

Desktop stack owns:

- EC2 instance
- root EBS volume through normal instance root volume config
- security group attachment
- IAM instance profile attachment
- Route53 record under selected zone
- user-data or cloud-init payload
- resource tags

## Requirements

- One Pulumi stack per desktop: `ai-desktops/desktop-<desktop_id>`.
- Hostname format: `<desktop_id>.desktops.orchael.com` or `<desktop_id>.desktops.orchael.dev`.
- Root EBS volume must persist across stop/start.
- Termination destroys the root EBS volume unless snapshot support is explicitly added later.
- Do not expose raw VNC, Docker, CDP, or bridge ports publicly.
- SSH ingress must be restricted to configured operator CIDR when possible.

## Acceptance Criteria

- Desktop stack preview succeeds with valid foundation outputs.
- Stack exports desktop ID, instance ID, hostname, noVNC base URL, SSH target, workspace path, and GitHub owner.
- Generated tags include `managed-by=ai-desktops`, desktop ID, GitHub owner, and environment.

## Verification

```bash
go test ./...
go run ./cmd/ai-desktops create --env dev --github-owner <owner> --repo github.com/<owner>/<repo> --preview
```
