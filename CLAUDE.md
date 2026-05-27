# CLAUDE.md

This file provides guidance to Claude Code for working in this repository.

## Repository Facts

- Canonical GitHub repo: `orchael/ai-desktops`
- Default branch: `main`
- Primary package manager: `go` (CLI); `pnpm` (apps/desktop-web)
- Version-file locations: `go.mod` (Go version), `.nvmrc` (Node version), `internal/version/version.go` (CLI version string)
- Canonical config files: `go.mod`, `apps/desktop-web/package.json`, `apps/desktop-web/pnpm-lock.yaml`
- Primary CI workflows: `.github/workflows/ci.yml`, `.github/workflows/lint.yml`
- Primary release/publish workflow: `.github/workflows/publish-cli.yml`
- Go build/test commands: `go build ./cmd/ai-desktops`, `go test ./...`
- Web build/test commands (from `apps/desktop-web/`): `pnpm run build`, `pnpm run test`, `pnpm run test:coverage`
- Lint commands: `pnpm run lint` and `pnpm run prettier` (in `apps/desktop-web/`)
- Coverage threshold: 60% for Go internal packages (excluding `internal/awsx` and `cmd/`, which need real AWS); 50% for TypeScript
- Generated or protected paths: `apps/desktop-web/dist/` (build output — do not edit directly)

## Installed agent rules

Created by Ballast. Do not edit this section.

Read and follow these rule files in `.claude/rules/` when they apply:

- `.claude/rules/common/local-dev-badges.md` — Rules for common/local-dev-badges
- `.claude/rules/common/local-dev-env.md` — Rules for common/local-dev-env
- `.claude/rules/common/local-dev-license.md` — Rules for common/local-dev-license
- `.claude/rules/common/local-dev-mcp.md` — Rules for common/local-dev-mcp
- `.claude/rules/common/docs.md` — Rules for common/docs
- `.claude/rules/common/cicd.md` — Rules for common/cicd
- `.claude/rules/common/observability.md` — Rules for common/observability
- `.claude/rules/common/publishing-libraries.md` — Rules for common/publishing-libraries
- `.claude/rules/common/publishing-sdks.md` — Rules for common/publishing-sdks
- `.claude/rules/common/publishing-apps.md` — Rules for common/publishing-apps
- `.claude/rules/common/git-hooks.md` — Rules for common/git-hooks
- `.claude/rules/common/tasks.md` — Rules for common/tasks
- `.claude/rules/typescript/typescript-linting.md` — Rules for typescript/linting
- `.claude/rules/typescript/typescript-logging.md` — Rules for typescript/logging
- `.claude/rules/typescript/typescript-testing.md` — Rules for typescript/testing
- `.claude/rules/python/python-linting.md` — Rules for python/linting
- `.claude/rules/python/python-logging.md` — Rules for python/logging
- `.claude/rules/python/python-testing.md` — Rules for python/testing
- `.claude/rules/go/go-linting.md` — Rules for go/linting
- `.claude/rules/go/go-logging.md` — Rules for go/logging
- `.claude/rules/go/go-testing.md` — Rules for go/testing
- `.claude/rules/ansible/ansible-linting.md` — Rules for ansible/linting
- `.claude/rules/ansible/ansible-logging.md` — Rules for ansible/logging
- `.claude/rules/ansible/ansible-testing.md` — Rules for ansible/testing
- `.claude/rules/terraform/terraform-linting.md` — Rules for terraform/linting
- `.claude/rules/terraform/terraform-logging.md` — Rules for terraform/logging
- `.claude/rules/terraform/terraform-testing.md` — Rules for terraform/testing

## Installed skills

Created by Ballast. Do not edit this section.

Read and use these skill files in `.claude/skills/` when they are relevant:

- `.claude/skills/github-health-check.skill` — run a comprehensive GitHub repository health check covering CI status, code quality, branch hygiene, and repo configuration
