#!/usr/bin/env bash
# Opt-in AWS E2E tests. Normal go test never provisions resources.
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
exec go run ./tests/e2e "$@"
