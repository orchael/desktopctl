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
15. `15-wireguard-vpn.md`
16. `16-desktop-web-root-welcome-page.md`
17. `17-github-developer-tooling.md`
18. `18-efs-workspaces.md`

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

- Update docs when behavior or operator workflow changes.
- Add focused tests for non-trivial logic.
- Keep implementation scoped to the task.
- Record any unresolved issue in the task file or a follow-up GitHub issue.
- Run the relevant verification commands before handing off.
