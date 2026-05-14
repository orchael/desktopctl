# Task 13 - MVP Smoke and Operator Docs

## Objective

Create the end-to-end smoke path and operator documentation for the MVP.

## Depends On

- Task 01 through Task 12

## Scope

Create:

- `README.md`
- smoke checklist or script
- example config file without secrets
- docs for required AWS permissions
- docs for Route53 prerequisites
- docs for GitHub PAT secret setup
- docs for Pulumi CLI requirement
- docs for create, connect, agent control, stop, start, terminate, and doctor workflows

Smoke path:

1. Bootstrap S3 backend and DynamoDB.
2. Initialize foundation stack for `dev`.
3. Create one desktop under `desktops.orchael.dev`.
4. Verify browser noVNC access.
5. Verify SSH access.
6. Verify required tools.
7. Verify repo checkout.
8. Verify bridge status through CLI-managed tunnel.
9. Stop desktop.
10. Start desktop.
11. Verify state persisted.
12. Terminate desktop.

## Requirements

- Documentation must not contain real secrets.
- Example config must use placeholder secret references.
- Smoke steps must be copy-pasteable.
- Any destructive command must be clearly labeled.
- Known limitations must include Elementary/Pantheon reliability and root EBS persistence.

## Acceptance Criteria

- A new operator can follow the docs to run the MVP smoke path.
- Smoke path maps directly to PRD acceptance criteria.
- Docs include recovery guidance for failed provisioning.
- Docs describe why failed desktops are left running by default.

## Verification

```bash
go test ./...
```

Manual verification:

```bash
ai-desktops bootstrap --env dev
ai-desktops init-foundation --env dev
ai-desktops create --env dev --github-owner <owner> --repo github.com/<owner>/<repo>
ai-desktops doctor <desktop_id>
ai-desktops agent <desktop_id> status
ai-desktops stop <desktop_id>
ai-desktops start <desktop_id>
ai-desktops terminate <desktop_id>
```
