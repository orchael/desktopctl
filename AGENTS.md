# AGENTS.md

This file provides shared repository guidance for agent tools that read AGENTS.md.

## Repository Facts

- Canonical GitHub repo: `orchael/desktopctl`
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

Read and follow these rule files in `.codex/rules/` when they apply:

- `.codex/rules/common/local-dev-badges.md` — Rules for common/local-dev-badges
- `.codex/rules/common/local-dev-env.md` — Rules for common/local-dev-env
- `.codex/rules/common/local-dev-license.md` — Rules for common/local-dev-license
- `.codex/rules/common/local-dev-mcp.md` — Rules for common/local-dev-mcp
- `.codex/rules/common/docs.md` — Rules for common/docs
- `.codex/rules/common/cicd.md` — Rules for common/cicd
- `.codex/rules/common/observability.md` — Rules for common/observability
- `.codex/rules/common/publishing-api.md` — Rules for common/publishing-api
- `.codex/rules/common/publishing-apps.md` — Rules for common/publishing-apps
- `.codex/rules/common/publishing-apt.md` — Rules for common/publishing-apt
- `.codex/rules/common/publishing-brew.md` — Rules for common/publishing-brew
- `.codex/rules/common/publishing-cli.md` — Rules for common/publishing-cli
- `.codex/rules/common/publishing-libraries.md` — Rules for common/publishing-libraries
- `.codex/rules/common/publishing-sdks.md` — Rules for common/publishing-sdks
- `.codex/rules/common/publishing-web.md` — Rules for common/publishing-web
- `.codex/rules/common/git-hooks.md` — Rules for common/git-hooks
- `.codex/rules/common/tasks-task-system.md` — Rules for common/tasks-task-system
- `.codex/rules/common/tasks-todo.md` — Rules for common/tasks-todo
- `.codex/rules/typescript/typescript-linting.md` — Rules for typescript/linting
- `.codex/rules/typescript/typescript-logging.md` — Rules for typescript/logging
- `.codex/rules/typescript/typescript-testing.md` — Rules for typescript/testing
- `.codex/rules/go/go-linting.md` — Rules for go/linting
- `.codex/rules/go/go-logging.md` — Rules for go/logging
- `.codex/rules/go/go-testing.md` — Rules for go/testing
- `.codex/rules/ansible/ansible-linting.md` — Rules for ansible/linting
- `.codex/rules/ansible/ansible-logging.md` — Rules for ansible/logging
- `.codex/rules/ansible/ansible-testing.md` — Rules for ansible/testing

## Installed skills

Created by Ballast. Do not edit this section.

Read and use these skill files in `.codex/skills/` when they are relevant:

- `.codex/skills/github-health-check/SKILL.md` — run a comprehensive GitHub repository health check covering CI status, code quality, branch hygiene, and repo configuration
- `.codex/skills/github-pr-copilot-cycle/SKILL.md` — create or update a GitHub PR, request Copilot review, triage and fix Copilot comments, push fixes, check CI, and repeat up to three cycles
