# CLAUDE.md

This file provides guidance to Claude Code for working in this repository.

## Repository Facts

- Canonical GitHub repo: `orchael/desktopctl`
- Default branch: `main`
- Primary package manager: `go` (CLI); `pnpm` (apps/desktop-web)
- Version-file locations: `go.mod` (Go version), `.nvmrc` (Node version), `internal/version/version.go` (CLI version string)
- Canonical config files: `go.mod`, `apps/desktop-web/package.json`, `apps/desktop-web/pnpm-lock.yaml`
- Primary CI workflows: `.github/workflows/ci.yml`, `.github/workflows/lint.yml`
- Primary release/publish workflow: `.github/workflows/publish-cli.yml`
- Go build/test commands: `make build` (writes `bin/desktopctl`), `go test ./...`
- Web build/test commands (from `apps/desktop-web/`): `pnpm run build`, `pnpm run test`, `pnpm run test:coverage`
- Lint commands: `pnpm run lint` and `pnpm run prettier` (in `apps/desktop-web/`)
- Coverage threshold: 60% for Go internal packages (excluding `internal/awsx` and `cmd/`, which need real AWS); 50% for TypeScript
- Generated or protected paths: `apps/desktop-web/dist/` (build output — do not edit directly)
- Branch-local task file: `tasks/TODO.md` (uppercase, canonical). Never create `tasks/todo.md` — both paths can be tracked in git but collide on case-insensitive filesystems, leaving one permanently dirty. The installed `tasks-todo` rule names the lowercase path; use `tasks/TODO.md` instead.

## Installed agent rules

Created by Ballast. Do not edit this section.

### Repository Tool Policy

- Check `.rulesrc.json` `tools` before adding, installing, or running language tooling.
- Configured tools: ansible=ansible-lint,molecule; docker=docker,hadolint,trivy; go=go,gofumpt,golangci-lint; typescript=pnpm,corepack.
- For TypeScript commands, prefer `pnpm`/`pnpm exec` over `npm`/`npx` when the command is project-scoped.

Read and follow these rule files in `.claude/rules/` when they apply:

- `.claude/rules/common/local-dev-autonomy.md` — Rules for common/local-dev-autonomy
- `.claude/rules/common/local-dev-badges.md` — Rules for common/local-dev-badges
- `.claude/rules/common/local-dev-env.md` — Rules for common/local-dev-env
- `.claude/rules/common/local-dev-license.md` — Rules for common/local-dev-license
- `.claude/rules/common/docs.md` — Rules for common/docs
- `.claude/rules/common/cicd.md` — Rules for common/cicd
- `.claude/rules/common/observability.md` — Rules for common/observability
- `.claude/rules/common/publishing.md` — Rules for common/publishing
- `.claude/rules/common/publishing-apps.md` — Rules for common/publishing-apps
- `.claude/rules/common/publishing-cli.md` — Rules for common/publishing-cli
- `.claude/rules/common/publishing-libraries.md` — Rules for common/publishing-libraries
- `.claude/rules/common/publishing-sdks.md` — Rules for common/publishing-sdks
- `.claude/rules/common/git-hooks.md` — Rules for common/git-hooks
- `.claude/rules/common/tasks-task-system.md` — Rules for common/tasks-task-system
- `.claude/rules/common/tasks-todo.md` — Rules for common/tasks-todo
- `.claude/rules/typescript/typescript-linting.md` — Rules for typescript/linting
- `.claude/rules/typescript/typescript-logging.md` — Rules for typescript/logging
- `.claude/rules/typescript/typescript-testing.md` — Rules for typescript/testing
- `.claude/rules/go/go-linting.md` — Rules for go/linting
- `.claude/rules/go/go-logging.md` — Rules for go/logging
- `.claude/rules/go/go-testing.md` — Rules for go/testing
- `.claude/rules/ansible/ansible-linting.md` — Rules for ansible/linting
- `.claude/rules/ansible/ansible-logging.md` — Rules for ansible/logging
- `.claude/rules/ansible/ansible-testing.md` — Rules for ansible/testing
- `.claude/rules/docker/docker-linting.md` — Rules for docker/linting
- `.claude/rules/docker/docker-logging.md` — Rules for docker/logging
- `.claude/rules/docker/docker-testing.md` — Rules for docker/testing

## Installed skills

Created by Ballast. Do not edit this section.

These skills are registered with Claude Code. Invoke one by name (for example `/ballast-audit`) when it is relevant:

- `/github-health-check` — run a comprehensive GitHub repository health check covering CI status, code quality, branch hygiene, and repo configuration
- `/github-pr-copilot-cycle` — create or update a GitHub PR, request Copilot review, triage and fix Copilot comments, push fixes, check CI, and repeat up to three cycles
- `/ballast-audit` — audit a Ballast installation for stale, unowned, oversized, and irrelevant rules and skills, and report the narrowest config that still covers the repository
