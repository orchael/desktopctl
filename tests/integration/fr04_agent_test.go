//go:build integration

// FR-4 — Agent runtime
//
// Acceptance criteria tested here:
//
//	AC-4.1  ai-agent-bridge systemd service is active on the desktop
//	AC-4.2  Bridge is bound to localhost only (not 0.0.0.0)
//	AC-4.4  `ai-desktops agent <id> status` returns bridge status without error
//	AC-4.5  Agent command works through the CLI-managed tunnel
//	AC-4.6  Bridge port is not exposed in the public security group
package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestFR4_BridgeServiceActive verifies that the ai-agent-bridge systemd unit
// is running (AC-4.1).
func TestFR4_BridgeServiceActive(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify systemd unit")
	}
	assertSystemdActive(t, fx.SSHTarget, fx.SSHKey, "ai-agent-bridge")
}

// TestFR4_BridgeLocalhostOnly verifies that ai-agent-bridge is bound to
// 127.0.0.1 and not 0.0.0.0, enforcing FR-4.6 (bridge not on public internet).
func TestFR4_BridgeLocalhostOnly(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify bridge binding")
	}
	out := sshRun(t, fx.SSHTarget, fx.SSHKey,
		"ss -tlnp 2>/dev/null | grep ':9445'")
	if out == "" {
		t.Error("bridge port 9445 not listening — is ai-agent-bridge running?")
		return
	}
	// The address field should be 127.0.0.1:9445, not 0.0.0.0:9445 or *:9445.
	if strings.Contains(out, "0.0.0.0:9445") || strings.Contains(out, "*:9445") {
		t.Errorf("bridge is listening on 0.0.0.0 or *:9445; must be localhost-only.\nss output: %s", out)
	}
}

// TestFR4_AgentStatusCommand verifies that `ai-desktops agent <id> status`
// completes without error, confirming the CLI tunnel and bridge integration
// work end-to-end (AC-4.4, AC-4.5).
//
// The agent command creates an SSM or SSH port-forward under the hood, which
// requires a healthy desktop and valid AWS credentials.
func TestFR4_AgentStatusCommand(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot establish agent tunnel")
	}
	_, err := runCLI(context.Background(), 2*time.Minute,
		"agent", "status", fx.ID, "--config", configPath)
	if err != nil {
		t.Errorf("agent status failed: %v", err)
	}
}

// TestFR4_AgentProvidersCommand verifies that the agent providers subcommand
// lists the expected AI providers (AC-4.3 — Codex, Claude, Gemini).
func TestFR4_AgentProvidersCommand(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot establish agent tunnel")
	}
	out, err := runCLI(context.Background(), 2*time.Minute,
		"agent", "providers", fx.ID, "--config", configPath)
	if err != nil {
		t.Errorf("agent providers failed: %v", err)
		return
	}

	s := strings.ToLower(string(out))
	for _, provider := range []string{"codex", "claude", "gemini"} {
		if !strings.Contains(s, provider) {
			t.Errorf("agent providers output missing %q\nraw: %s", provider, out)
		}
	}
}
