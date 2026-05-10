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

Created by [Ballast](https://github.com/everydaydevopsio/ballast) v5.9.3. Do not edit this section.

Read and follow these rule files in `.claude/rules/` when they apply:

- `.claude/rules/local-dev-badges.md` — Add standard badges (CI, Release, License, GitHub Release, npm) to the top of README.md
- `.claude/rules/local-dev-env.md` — Local development environment specialist - reproducible dev setup, DX, and documentation
- `.claude/rules/local-dev-license.md` — License setup - ensure LICENSE file, package.json license field, and README reference (default MIT; overridable in AGENTS.md/CLAUDE.md)
- `.claude/rules/local-dev-mcp.md` — Optional: use GitHub MCP and issues MCP (Jira/Linear/GitHub) for local-dev context
- `.claude/rules/docs.md` — Documentation specialist - GitHub Markdown docs by default, or maintain existing Docusaurus sites with publish-docs automation
- `.claude/rules/cicd.md` — CI/CD specialist - pipeline design, quality gates, and deployment
- `.claude/rules/observability.md` — Observability specialist - logging, tracing, metrics, and SLOs
- `.claude/rules/publishing-api.md` — REST API publishing specialist - Docker CD with Kubernetes health probes and Helm chart update
- `.claude/rules/publishing-apps.md` — App publishing specialist - npmjs for Node apps, PyPI for Python apps, GitHub Releases for Go apps
- `.claude/rules/publishing-apt.md` — APT/deb package publishing specialist - GoReleaser nfpms and GitHub Releases
- `.claude/rules/publishing-brew.md` — Homebrew tap publishing specialist - GoReleaser brews block and tap repo setup
- `.claude/rules/publishing-cli.md` — CLI publishing specialist - GoReleaser for Go, npmjs for Node, PyPI for Python
- `.claude/rules/publishing-libraries.md` — Library publishing specialist - npmjs for TypeScript, PyPI for Python, GitHub tags/releases for Go
- `.claude/rules/publishing-sdks.md` — SDK publishing specialist - npmjs for TypeScript SDKs, PyPI for Python SDKs, GitHub tags/releases for Go SDKs
- `.claude/rules/publishing-web.md` — Web app publishing specialist - Docker to GHCR/Docker Hub with Helm chart CD on push to main
- `.claude/rules/git-hooks.md` — Git hook specialist - configure pre-commit, pre-push, and Husky workflows that match the repository layout
- `.claude/rules/tasks-task-system.md` — Task system integration - use {{taskSystem}} for work items and configure the MCP server
- `.claude/rules/tasks-todo.md` — Branch-local TODO tracking - manage tasks/TODO.md and triage before PR
- `.claude/rules/typescript-linting.md` — TypeScript linting specialist - implements comprehensive linting and code formatting for TypeScript/JavaScript projects
- `.claude/rules/typescript-logging.md` — Centralized logging specialist - configures Pino with Fluentd for Node/Next.js, and pino-browser to /api/logs
- `.claude/rules/typescript-testing.md` — Testing specialist - sets up Jest (default) or Vitest for Vite projects, 50% coverage, and test step in build GitHub Action
