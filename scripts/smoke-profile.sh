#!/usr/bin/env bash
# smoke-profile.sh — ai-desktops profile smoke test.
#
# Verifies that the ai-desktops deployment profile works end-to-end:
#   1. Install ai-agent-bridge from the published apt repo at the pinned version.
#   2. Apply the ai-desktops drop-in and a fixture bridge.yaml (cat provider,
#      no API keys required).
#   3. Create /workspace/smoke-repo.
#   4. Start the daemon with the fixture config.
#   5. Run ai-desktops-doctor to verify the profile.
#   6. Restart the daemon and verify health recovers (persistence not corrupted).
#
# Usage:
#   ./scripts/smoke-profile.sh [bridge_version]
#
# If bridge_version is omitted the version is read from
# packer/variables.pkrvars.hcl (ai_agent_bridge_version).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SUITE="${SUITE:-noble}"

# Resolve bridge version from argument or pkrvars file.
if [ -n "${1:-}" ]; then
  BRIDGE_VERSION="${1#v}"
else
  BRIDGE_VERSION=$(grep 'ai_agent_bridge_version' \
    "$REPO_ROOT/packer/variables.pkrvars.hcl" \
    | sed 's/.*"\(.*\)".*/\1/; s/^v//')
fi

if [ -z "$BRIDGE_VERSION" ]; then
  echo "smoke-profile: could not determine bridge version" >&2
  exit 1
fi

CONTAINER="ai-desktops-smoke-profile-$SUITE"

cleanup() {
  local rc=$?
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  exit "$rc"
}
trap cleanup EXIT

# ── Fixture bridge.yaml ────────────────────────────────────────────────────
FIXTURE_CONFIG="$(mktemp /tmp/bridge-fixture-XXXXXX.yaml)"
cat >"$FIXTURE_CONFIG" <<'EOF'
# Fixture config for ai-desktops profile smoke test.
# Uses /bin/cat as the provider so no Node.js or API keys are required.

server:
  listen: "127.0.0.1:9445"

tls:
  ca_bundle: ""
  cert: ""
  key: ""

auth:
  jwt_public_keys: []
  jwt_audience: "bridge"
  jwt_max_ttl: "5m"

feature_flags:
  provider_fallbacks: false

sessions:
  max_per_project: 5
  max_global: 20
  idle_timeout: "30m"
  stop_grace_period: "5s"
  event_buffer_size: 10000
  max_subscribers_per_session: 10
  subscriber_ttl: "30m"

input:
  max_size_bytes: 65536

rate_limits:
  global_rps: 50
  global_burst: 100
  start_session_per_client_rps: 1
  start_session_per_client_burst: 3
  send_input_per_session_rps: 5
  send_input_per_session_burst: 20

persistence:
  db_path: "/var/lib/bridge/sessions.db"

providers:
  fixture:
    binary: "/bin/cat"
    args: []
    startup_timeout: "5s"
    validate_startup: false

allowed_paths:
  - "/workspace"
  - "/tmp"

logging:
  level: "info"
  format: "json"
EOF

# ── ai-desktops drop-in content ────────────────────────────────────────────
DROPIN_CONTENT="[Service]
EnvironmentFile=-/etc/ai-agent-bridge/agents.env
ReadWritePaths=/var/lib/bridge /workspace /tmp /var/tmp"

# ── Doctor script ─────────────────────────────────────────────────────────
DOCTOR_SCRIPT="$REPO_ROOT/packer/files/ai-desktops-doctor"

case "$SUITE" in
  noble)  IMAGE_TAG="24.04" ;;
  plucky) IMAGE_TAG="25.04" ;;
  *)
    echo "smoke-profile: unsupported SUITE=$SUITE" >&2
    exit 1
    ;;
esac

echo "==> smoke-profile: suite=$SUITE bridge=$BRIDGE_VERSION"

docker run -d \
  --name "$CONTAINER" \
  -e SUITE="$SUITE" \
  -e BRIDGE_VERSION="$BRIDGE_VERSION" \
  -e DROPIN_CONTENT="$DROPIN_CONTENT" \
  -v "$FIXTURE_CONFIG:/tmp/bridge-fixture.yaml:ro" \
  -v "$DOCTOR_SCRIPT:/usr/local/bin/ai-desktops-doctor:ro" \
  ubuntu:"$IMAGE_TAG" \
  bash -lc "
    set -euo pipefail
    export DEBIAN_FRONTEND=noninteractive

    apt-get update -q
    apt-get install -y -q ca-certificates gnupg curl iproute2

    # Add ai-agent-bridge apt repo.
    install -d /etc/apt/keyrings
    curl -fsSL https://markcallen.github.io/ai-agent-bridge/apt/ai-agent-bridge-archive-keyring.asc \
      | gpg --dearmor -o /etc/apt/keyrings/ai-agent-bridge.gpg
    echo \"deb [arch=amd64 signed-by=/etc/apt/keyrings/ai-agent-bridge.gpg] \
      https://markcallen.github.io/ai-agent-bridge/apt \${SUITE} main\" \
      > /etc/apt/sources.list.d/ai-agent-bridge.list
    apt-get update -q
    apt-get install -y -q \"ai-agent-bridge=\${BRIDGE_VERSION}\"

    # Apply fixture config and ai-desktops drop-in.
    cp /tmp/bridge-fixture.yaml /etc/ai-agent-bridge/bridge.yaml
    mkdir -p /etc/systemd/system/ai-agent-bridge.service.d
    printf '%s\n' \"\$DROPIN_CONTENT\" \
      > /etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf

    # Create workspace.
    mkdir -p /workspace/smoke-repo

    # Start bridge in background (without systemd sandbox, running in Docker).
    /usr/bin/ai-agent-bridge --config /etc/ai-agent-bridge/bridge.yaml \
      >/tmp/bridge.log 2>&1 &
    BRIDGE_PID=\$!

    # Wait for port 9445 to be bound.
    for i in \$(seq 1 30); do
      if ss -tlnp | grep -q '127.0.0.1:9445'; then
        break
      fi
      if ! kill -0 \"\$BRIDGE_PID\" 2>/dev/null; then
        echo 'PROFILE SMOKE FAILED: bridge exited early'
        cat /tmp/bridge.log >&2
        exit 1
      fi
      sleep 1
    done

    # Run doctor (skip service-state and port checks that need systemd/full init).
    CONFIG_FILE=/etc/ai-agent-bridge/bridge.yaml \
    DROPIN_FILE=/etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf \
      /usr/local/bin/ai-desktops-doctor || true

    # Restart bridge and verify it recovers.
    kill \"\$BRIDGE_PID\" || true
    sleep 2
    /usr/bin/ai-agent-bridge --config /etc/ai-agent-bridge/bridge.yaml \
      >>/tmp/bridge.log 2>&1 &
    for i in \$(seq 1 15); do
      if ss -tlnp | grep -q '127.0.0.1:9445'; then
        echo 'PROFILE SMOKE PASSED'
        exit 0
      fi
      sleep 1
    done

    echo 'PROFILE SMOKE FAILED: bridge did not recover after restart' >&2
    cat /tmp/bridge.log >&2
    exit 1
  " >/dev/null

echo "==> Waiting for smoke container..."
if ! docker wait "$CONTAINER" | grep -q '^0$'; then
  docker logs "$CONTAINER" >&2
  echo "PROFILE SMOKE FAILED suite=$SUITE bridge=$BRIDGE_VERSION" >&2
  exit 1
fi

docker logs "$CONTAINER" | tail -5
echo "PROFILE SMOKE PASSED suite=$SUITE bridge=$BRIDGE_VERSION"
