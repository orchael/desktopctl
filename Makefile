SHELL   := bash
BINARY := ai-desktops
CMD     := ./cmd/ai-desktops

AI_DESKTOPS_TEST_BUCKET  ?= orchael-ai-desktops-test
AI_DESKTOPS_GITHUB_OWNER ?= orchael

.PHONY: build test setup-integration test-integration test-integration-adopt test-integration-fr clean-integration clean deps check-deps

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

# setup-integration performs one-time setup for the integration test environment.
#
# It generates an SSH key pair at tests/integration/id_ed25519, runs the
# ai-desktops setup wizard writing to tests/integration/config.yaml, then
# bootstraps the S3 backend and deploys the foundation stack.
#
# Run once before `make test-integration`. Re-running is idempotent.
#
# The generated files are gitignored. Copy tests/integration/config.yaml.example
# as a reference for the expected format.
setup-integration: check-deps build
	@echo "setup-integration: generating SSH key pair..."
	@if [ ! -f tests/integration/id_ed25519 ]; then \
	  ssh-keygen -t ed25519 -f tests/integration/id_ed25519 -N "" -C "ai-desktops-integration" > /dev/null; \
	  echo "setup-integration: generated tests/integration/id_ed25519"; \
	else \
	  echo "setup-integration: SSH key already exists, skipping keygen"; \
	fi
	@echo ""
	@echo "setup-integration: running ai-desktops setup..."
	@echo "  The wizard will ask for your configuration. When prompted for SSH key,"
	@echo "  enter: $$(pwd)/tests/integration/id_ed25519"
	@echo ""
	./$(BINARY) setup --config-out tests/integration/config.yaml
	@echo ""
	@echo "setup-integration: bootstrapping S3 backend..."
	./$(BINARY) bootstrap --config tests/integration/config.yaml
	@echo ""
	@echo "setup-integration: deploying foundation stack (env=test)..."
	./$(BINARY) init-foundation --config tests/integration/config.yaml --env test
	@echo ""
	@echo "setup-integration: done. Run 'make test-integration' to execute the suite."

# test-integration runs the full end-to-end integration suite.
#
# The suite is self-contained: it generates its own SSH key pair, writes its
# own config file, bootstraps the S3 Pulumi backend, deploys the foundation
# stack in us-west-2 (env=test), creates a desktop, runs all FR checks, then
# terminates the desktop.  It never reads ~/.ai-desktops/config.yaml or any
# key from ~/.ssh.
#
# Required:
#   AI_DESKTOPS_TEST_BUCKET  — globally-unique S3 bucket name for Pulumi state
#                              (created automatically if it doesn't exist)
#   AI_DESKTOPS_GITHUB_OWNER — GitHub org or user for the test desktop
#
# AWS credentials are taken from the environment in the standard order
# (AWS_PROFILE, AWS_ACCESS_KEY_ID/SECRET, or the default credential chain).
#
# Optional:
#   AI_DESKTOPS_TEST_REPO     — repo URL to clone (enables FR-6/7 workspace tests)
#   AI_DESKTOPS_EXISTING_ID   — adopt an already-running desktop instead of creating one
#
# The full suite always runs the Packer AMI build (15–20 min) before
# creating the test desktop.  Total expected runtime: ~2h.
test-integration: check-deps build
	@if [ -f tests/integration/config.yaml ]; then \
	  echo "test-integration: using tests/integration/config.yaml (from make setup-integration)"; \
	else \
	  test -n "$(AI_DESKTOPS_TEST_BUCKET)" || { \
	    echo ""; \
	    echo "ERROR: AI_DESKTOPS_TEST_BUCKET is not set and tests/integration/config.yaml does not exist."; \
	    echo ""; \
	    echo "Either run 'make setup-integration' first, or supply env vars:"; \
	    echo "  make test-integration AI_DESKTOPS_TEST_BUCKET=myorg-ai-desktops-test AI_DESKTOPS_GITHUB_OWNER=myorg"; \
	    echo ""; \
	    exit 1; \
	  }; \
	  test -n "$(AI_DESKTOPS_GITHUB_OWNER)" || { \
	    echo ""; \
	    echo "ERROR: AI_DESKTOPS_GITHUB_OWNER is not set and tests/integration/config.yaml does not exist."; \
	    echo ""; \
	    echo "Either run 'make setup-integration' first, or supply env vars:"; \
	    echo "  make test-integration AI_DESKTOPS_TEST_BUCKET=myorg-ai-desktops-test AI_DESKTOPS_GITHUB_OWNER=myorg"; \
	    echo ""; \
	    exit 1; \
	  }; \
	fi
	@set -o pipefail; \
	tmpout=$$(mktemp /tmp/ai-desktops-integration-XXXXXX.log); \
	if [ -f tests/integration/config.yaml ]; then \
	  AI_DESKTOPS_TEST_CONFIG=tests/integration/config.yaml \
	    go test -v -tags=integration -timeout=3h ./tests/integration/... 2>&1 | tee "$$tmpout"; \
	else \
	  AI_DESKTOPS_TEST_BUCKET=$(AI_DESKTOPS_TEST_BUCKET) \
	    AI_DESKTOPS_GITHUB_OWNER=$(AI_DESKTOPS_GITHUB_OWNER) \
	    go test -v -tags=integration -timeout=3h ./tests/integration/... 2>&1 | tee "$$tmpout"; \
	fi; \
	testret=$${PIPESTATUS[0]}; \
	echo ""; \
	echo "=== Integration Test Summary ==="; \
	printf "%-6s  %-55s  %s\n" "STATUS" "TEST" "DURATION"; \
	printf "%-6s  %-55s  %s\n" "------" "-------------------------------------------------------" "--------"; \
	grep -E '^--- (PASS|FAIL|SKIP):' "$$tmpout" | \
	  awk '{status=substr($$2,1,length($$2)-1); name=$$3; dur=$$4; gsub(/[()]/,"",dur); printf "%-6s  %-55s  %s\n", status, name, dur}'; \
	echo ""; \
	passed=$$(grep -c '^--- PASS:' "$$tmpout" || true); \
	failed=$$(grep -c '^--- FAIL:' "$$tmpout" || true); \
	skipped=$$(grep -c '^--- SKIP:' "$$tmpout" || true); \
	printf "Results: %d passed, %d failed, %d skipped\n" "$$passed" "$$failed" "$$skipped"; \
	rm -f "$$tmpout"; \
	exit $$testret

# test-integration-adopt re-runs the integration suite against an existing
# desktop, skipping the create step.  The suite still bootstraps and runs
# init-foundation (idempotent) so the config is always consistent.
#
# Example:
#   make test-integration-adopt DESKTOP_ID=d-abc123 AI_DESKTOPS_TEST_BUCKET=myorg-ai-desktops-test AI_DESKTOPS_GITHUB_OWNER=myorg
test-integration-adopt: check-deps build
	$(if $(DESKTOP_ID),,$(error DESKTOP_ID is required — e.g. make test-integration-adopt DESKTOP_ID=d-abc123))
	@test -n "$(AI_DESKTOPS_TEST_BUCKET)" || { echo "ERROR: AI_DESKTOPS_TEST_BUCKET is not set"; exit 1; }
	@test -n "$(AI_DESKTOPS_GITHUB_OWNER)" || { echo "ERROR: AI_DESKTOPS_GITHUB_OWNER is not set"; exit 1; }
	@set -o pipefail; \
	tmpout=$$(mktemp /tmp/ai-desktops-integration-XXXXXX.log); \
	AI_DESKTOPS_TEST_BUCKET=$(AI_DESKTOPS_TEST_BUCKET) \
	  AI_DESKTOPS_GITHUB_OWNER=$(AI_DESKTOPS_GITHUB_OWNER) \
	  AI_DESKTOPS_EXISTING_ID=$(DESKTOP_ID) \
	  go test -v -tags=integration -timeout=30m ./tests/integration/... 2>&1 | tee "$$tmpout"; \
	testret=$${PIPESTATUS[0]}; \
	echo ""; \
	echo "=== Integration Test Summary ==="; \
	printf "%-6s  %-55s  %s\n" "STATUS" "TEST" "DURATION"; \
	printf "%-6s  %-55s  %s\n" "------" "-------------------------------------------------------" "--------"; \
	grep -E '^--- (PASS|FAIL|SKIP):' "$$tmpout" | \
	  awk '{status=substr($$2,1,length($$2)-1); name=$$3; dur=$$4; gsub(/[()]/,"",dur); printf "%-6s  %-55s  %s\n", status, name, dur}'; \
	echo ""; \
	passed=$$(grep -c '^--- PASS:' "$$tmpout" || true); \
	failed=$$(grep -c '^--- FAIL:' "$$tmpout" || true); \
	skipped=$$(grep -c '^--- SKIP:' "$$tmpout" || true); \
	printf "Results: %d passed, %d failed, %d skipped\n" "$$passed" "$$failed" "$$skipped"; \
	rm -f "$$tmpout"; \
	exit $$testret

# test-integration-fr runs a single FR's tests.
# Set FR to the functional requirement number (1–10).
#
# Example:
#   make test-integration-fr FR=5 AI_DESKTOPS_TEST_BUCKET=myorg-ai-desktops-test AI_DESKTOPS_GITHUB_OWNER=myorg
#   make test-integration-fr FR=7 DESKTOP_ID=d-abc123 AI_DESKTOPS_TEST_BUCKET=myorg-ai-desktops-test AI_DESKTOPS_GITHUB_OWNER=myorg
test-integration-fr: check-deps build
	$(if $(FR),,$(error FR is required — e.g. make test-integration-fr FR=5))
	@test -n "$(AI_DESKTOPS_TEST_BUCKET)" || { echo "ERROR: AI_DESKTOPS_TEST_BUCKET is not set"; exit 1; }
	@test -n "$(AI_DESKTOPS_GITHUB_OWNER)" || { echo "ERROR: AI_DESKTOPS_GITHUB_OWNER is not set"; exit 1; }
	@set -o pipefail; \
	tmpout=$$(mktemp /tmp/ai-desktops-integration-XXXXXX.log); \
	AI_DESKTOPS_TEST_BUCKET=$(AI_DESKTOPS_TEST_BUCKET) \
	  AI_DESKTOPS_GITHUB_OWNER=$(AI_DESKTOPS_GITHUB_OWNER) \
	  $(if $(DESKTOP_ID),AI_DESKTOPS_EXISTING_ID=$(DESKTOP_ID),) \
	  go test -v -tags=integration -timeout=30m \
	  -run 'TestFR$(FR)_' ./tests/integration/... 2>&1 | tee "$$tmpout"; \
	testret=$${PIPESTATUS[0]}; \
	echo ""; \
	echo "=== Integration Test Summary ==="; \
	printf "%-6s  %-55s  %s\n" "STATUS" "TEST" "DURATION"; \
	printf "%-6s  %-55s  %s\n" "------" "-------------------------------------------------------" "--------"; \
	grep -E '^--- (PASS|FAIL|SKIP):' "$$tmpout" | \
	  awk '{status=substr($$2,1,length($$2)-1); name=$$3; dur=$$4; gsub(/[()]/,"",dur); printf "%-6s  %-55s  %s\n", status, name, dur}'; \
	echo ""; \
	passed=$$(grep -c '^--- PASS:' "$$tmpout" || true); \
	failed=$$(grep -c '^--- FAIL:' "$$tmpout" || true); \
	skipped=$$(grep -c '^--- SKIP:' "$$tmpout" || true); \
	printf "Results: %d passed, %d failed, %d skipped\n" "$$passed" "$$failed" "$$skipped"; \
	rm -f "$$tmpout"; \
	exit $$testret

clean:
	rm -f $(BINARY)

# clean-integration tears down any surviving test desktops and the foundation
# stack in us-west-2 (env=test) without needing a full integration run.
#
# It builds the CLI, writes a temporary test config, terminates all desktops
# whose fleet record lives in the test DynamoDB table, then destroys the
# foundation Pulumi stack.
#
# Required:
#   AI_DESKTOPS_TEST_BUCKET  — S3 bucket used as Pulumi backend for test state
#   AI_DESKTOPS_GITHUB_OWNER — GitHub owner recorded in test desktop records
#
# Optional:
#   AWS_PROFILE — passed through to AWS calls (default: standard chain)
clean-integration: build
	@test -n "$(AI_DESKTOPS_TEST_BUCKET)" || { \
	  echo "ERROR: AI_DESKTOPS_TEST_BUCKET is not set"; exit 1; \
	}
	@test -n "$(AI_DESKTOPS_GITHUB_OWNER)" || { \
	  echo "ERROR: AI_DESKTOPS_GITHUB_OWNER is not set"; exit 1; \
	}
	@which jq > /dev/null 2>&1 || { echo "ERROR: jq is required but not found"; exit 1; }
	@tmpconf=$$(mktemp /tmp/ai-desktops-clean-XXXXXX.yaml); \
	printf 'aws:\n  region: us-west-2\n  profile: %s\npulumi:\n  backend_bucket: %s\n  infra_dir: %s\nfleet:\n  table_name: ai-desktops-test-fleet\n  environment: test\ngithub:\n  owner: %s\n  pat_secret: /ai-desktops/github/pat\ndesktop:\n  instance_type: t3.large\n  operator_cidr: 0.0.0.0/0\nagent:\n  bridge_port: 9445\n' \
	  "$(AWS_PROFILE)" "$(AI_DESKTOPS_TEST_BUCKET)" "$$(pwd)" "$(AI_DESKTOPS_GITHUB_OWNER)" \
	  > "$$tmpconf"; \
	echo "clean-integration: config written to $$tmpconf"; \
	echo "clean-integration: listing test desktops..."; \
	ids=$$(./$(BINARY) list --config "$$tmpconf" --json 2>/dev/null | jq -r '.[].desktop_id // empty' 2>/dev/null || true); \
	if [ -n "$$ids" ]; then \
	  for id in $$ids; do \
	    echo "clean-integration: terminating desktop $$id..."; \
	    ./$(BINARY) terminate "$$id" --config "$$tmpconf" --force || \
	      echo "WARNING: terminate $$id failed (may already be gone)"; \
	  done; \
	else \
	  echo "clean-integration: no test desktops found"; \
	fi; \
	echo "clean-integration: listing test AMIs..."; \
	ami_ids=$$(./$(BINARY) ami list --config "$$tmpconf" --json 2>/dev/null | jq -r '.[].ami_id // empty' 2>/dev/null || true); \
	if [ -n "$$ami_ids" ]; then \
	  for ami in $$ami_ids; do \
	    echo "clean-integration: deleting AMI $$ami..."; \
	    ./$(BINARY) ami delete "$$ami" --config "$$tmpconf" --force || \
	      echo "WARNING: delete AMI $$ami failed (may already be gone)"; \
	  done; \
	else \
	  echo "clean-integration: no test AMIs found"; \
	fi; \
	echo "clean-integration: destroying foundation stack (env=test)..."; \
	./$(BINARY) destroy-foundation --config "$$tmpconf" --env test --yes || \
	  echo "WARNING: foundation teardown failed (may already be gone)"; \
	rm -f "$$tmpconf"; \
	echo "clean-integration: done"

# check-deps verifies that required binaries are present before running the
# integration suite.  Fails immediately with a clear message if any are missing.
#
# Required binaries:
#   pulumi — infrastructure deployment (https://get.pulumi.com)
#   packer — AMI builds (https://developer.hashicorp.com/packer/install)
#   aws    — AWS CLI for credential checks and S3 bootstrap
check-deps:
	@missing=""; \
	for tool in pulumi packer aws; do \
	  which $$tool > /dev/null 2>&1 || missing="$$missing $$tool"; \
	done; \
	if [ -n "$$missing" ]; then \
	  echo ""; \
	  echo "ERROR: required tool(s) not found in PATH:$$missing"; \
	  echo ""; \
	  echo "  pulumi  — https://get.pulumi.com"; \
	  echo "  packer  — https://developer.hashicorp.com/packer/install"; \
	  echo "  aws     — https://aws.amazon.com/cli/"; \
	  echo ""; \
	  exit 1; \
	fi
	@echo "check-deps: all required tools found (pulumi, packer, aws)"

deps:
	@which pulumi > /dev/null 2>&1 || (echo "Installing Pulumi..." && curl -fsSL https://get.pulumi.com | sh)
	@which aws > /dev/null 2>&1 || echo "WARNING: AWS CLI not found — install from https://aws.amazon.com/cli/"
	@which go > /dev/null 2>&1 || echo "WARNING: Go not found — install from https://go.dev/dl/"
	@which jq > /dev/null 2>&1 || echo "WARNING: jq not found — required for clean-integration (install from https://jqlang.org)"
	@which golangci-lint > /dev/null 2>&1 || (echo "Installing golangci-lint v2..." && curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $$(go env GOPATH)/bin v2.1.6)
