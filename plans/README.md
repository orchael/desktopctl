# ai-desktops MVP Task Plan

This directory contains small implementation tasks for building the MVP described in `PRD.md` and `ARCHITECTURE.md`.

Each task is intended to be handed to another coding agent as a self-contained work item. Tasks should be completed in order unless the dependency section says otherwise.

## Sequence

1. `01-go-module-cli-skeleton.md`
2. `02-config-and-repo-validation.md`
3. `03-pulumi-backend-bootstrap.md`
4. `04-pulumi-foundation-stack.md`
5. `05-dynamodb-fleet-store.md`
6. `06-pulumi-desktop-stack.md`
7. `07-lifecycle-commands.md`
8. `08-desktop-cloud-init-bootstrap.md`
9. `09-readiness-and-doctor.md`
10. `10-repository-checkout.md`
11. `11-bridge-tunnel-agent-control.md`
12. `12-vite-desktop-webapp.md`
13. `13-mvp-smoke-and-docs.md`
14. `14-pre-baked-ami.md`
15. `17-github-developer-tooling.md`
16. `16-desktop-web-root-welcome-page.md`
17. `15-wireguard-vpn.md`
18. `18-efs-workspaces.md`

Plans 14-17 were originally written as independent follow-on tasks. The implemented path
landed in this order:

| Implemented order | Plan | Current status |
|---|---|---|
| 14 | `14-pre-baked-ami.md` | Implemented with `ai-desktops ami build`, Packer/Ansible image builds, `desktop.active_ami`, AMI history in DynamoDB, and slimmed cloud-init for pre-baked images. |
| 15 | `17-github-developer-tooling.md` | Implemented through `ai-desktops setup`, per-owner `github_secret` records in Secrets Manager, SSH git credentials, `gh auth`, git identity, and desktop developer tooling baked/configured by the AMI/bootstrap flow. |
| 16 | `16-desktop-web-root-welcome-page.md` | Implemented with the desktop-web app served at `/`, noVNC at `/novnc/vnc.html`, `/api/desktop`, `ai-desktops-web.service`, and `update-web` for a running desktop. |
| Deferred | `15-wireguard-vpn.md` | Not implemented. Private access currently uses the Tailscale and step-ca flows documented in the README; keep this plan as a future VPN design unless the product direction changes. |

When adding future plan files, preserve numeric filenames for existing historical plans, but
sequence the README by implemented dependency order when that differs from the original file
number.

## Global Constraints

- Use Go for CLI and Pulumi programs.
- Use Pulumi Automation API from the CLI for infrastructure operations.
- Use an S3 DIY backend for Pulumi state.
- Use DynamoDB for fleet metadata.
- Use Route53 zones `desktops.orchael.com` and `desktops.orchael.dev`.
- Keep `bridgectl` bound to `127.0.0.1` in v1.
- Use CLI-managed SSM or SSH tunnels for remote bridge access.
- Use a fine-scoped GitHub PAT from AWS secret storage for the first smoke path.
- Preserve desktop state through EC2 stop/start using the root EBS volume by default.
- Keep EFS-backed `/workspace` persistence optional and managed separately from desktop compute.
- Leave failed desktop instances running by default for debugging.

## Completion Standard

For each task:

- Update `README.md`, `PRD.md`, and the relevant plan file when behavior or operator workflow changes.
- Add focused Go, TypeScript, Pulumi, or shell tests for non-trivial logic.
- Keep implementation scoped to the task and avoid rewriting older plan files except to correct sequencing, status, or verification guidance.
- Record unresolved work in the task file, `tasks/todo.md`, or a follow-up GitHub issue.
- For setup and GitHub developer tooling changes, verify `ai-desktops setup` config output, Secrets Manager path handling, SSH git credential setup, and `gh auth` bootstrap behavior.
- For AMI changes, verify `ai-desktops ami build`, `ai-desktops ami list`, Packer/Ansible image contents, `desktop.active_ami`, and the slim cloud-init path used by `create`.
- For desktop-web root page changes, verify `pnpm run build`, server tests, `/`, `/api/desktop`, `/novnc/vnc.html`, `ai-desktops-web.service`, and `update-web` when runtime updates are touched.
- For private access changes, follow the implemented Tailscale/step-ca workflow unless the deferred WireGuard plan is explicitly resumed.
- Run the relevant verification commands before handing off and record any live-only checks that were not run.
