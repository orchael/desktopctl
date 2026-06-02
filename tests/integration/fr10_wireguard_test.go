//go:build integration

// FR-10 — WireGuard VPN support
//
// Acceptance criteria tested here:
//
//	AC-10.1  `ai-desktops wireguard --help` exits 0 and lists subcommands
//	AC-10.2  `ai-desktops wireguard add-peer --help` exits 0
//	AC-10.3  `ai-desktops wireguard list-peers --help` exits 0
//	AC-10.4  `ai-desktops wireguard remove-peer --help` exits 0
//	AC-10.5  `ai-desktops wireguard show-config --help` exits 0
//	AC-10.6  wireguard-tools is installed on the desktop (wg --version succeeds)
//	AC-10.7  WireGuard kernel module is available (wg is on PATH)
//
// Note: The wireguard CLI subcommands (add-peer, remove-peer, etc.) are not yet
// implemented.  The command-existence tests are written now and will start
// passing once the commands are wired into the CLI (FR-10.4).  They are NOT
// gated on a skip variable so that they act as a failing signal until the
// feature is shipped.
package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestFR10_WireGuardCommandExists verifies that the `wireguard` subcommand
// group is registered in the CLI (AC-10.1).
func TestFR10_WireGuardCommandExists(t *testing.T) {
	out, err := runCLI(context.Background(), 10*time.Second, "wireguard", "--help")
	if err != nil {
		t.Fatalf("wireguard --help failed: %v\noutput: %s", err, out)
	}
	lower := strings.ToLower(string(out))
	if !strings.Contains(lower, "wireguard") && !strings.Contains(lower, "vpn") && !strings.Contains(lower, "peer") {
		t.Errorf("wireguard --help output doesn't mention wireguard/vpn/peer\nraw: %s", out)
	}
}

// TestFR10_AddPeerCommandExists verifies `wireguard add-peer` is registered (AC-10.2).
func TestFR10_AddPeerCommandExists(t *testing.T) {
	_, err := runCLI(context.Background(), 10*time.Second, "wireguard", "add-peer", "--help")
	if err != nil {
		t.Fatalf("wireguard add-peer --help failed: %v", err)
	}
}

// TestFR10_ListPeersCommandExists verifies `wireguard list-peers` is registered (AC-10.3).
func TestFR10_ListPeersCommandExists(t *testing.T) {
	_, err := runCLI(context.Background(), 10*time.Second, "wireguard", "list-peers", "--help")
	if err != nil {
		t.Fatalf("wireguard list-peers --help failed: %v", err)
	}
}

// TestFR10_RemovePeerCommandExists verifies `wireguard remove-peer` is registered (AC-10.4).
func TestFR10_RemovePeerCommandExists(t *testing.T) {
	_, err := runCLI(context.Background(), 10*time.Second, "wireguard", "remove-peer", "--help")
	if err != nil {
		t.Fatalf("wireguard remove-peer --help failed: %v", err)
	}
}

// TestFR10_ShowConfigCommandExists verifies `wireguard show-config` is registered (AC-10.5).
func TestFR10_ShowConfigCommandExists(t *testing.T) {
	_, err := runCLI(context.Background(), 10*time.Second, "wireguard", "show-config", "--help")
	if err != nil {
		t.Fatalf("wireguard show-config --help failed: %v", err)
	}
}

// TestFR10_WireGuardToolsInstalled verifies wireguard-tools is present on the
// desktop (AC-10.6).  The `wg --version` command exits 0 when the package is
// installed.
func TestFR10_WireGuardToolsInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify wireguard-tools installation")
	}

	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "wg --version 2>&1")
	if err != nil {
		t.Errorf("wg --version failed — wireguard-tools may not be installed: %v\noutput: %s", err, out)
		return
	}
	if !strings.Contains(strings.ToLower(out), "wireguard") {
		t.Errorf("wg --version output doesn't contain 'wireguard'\nraw: %s", out)
	}
}

// TestFR10_WireGuardOnPath verifies wg is on PATH (AC-10.7).
func TestFR10_WireGuardOnPath(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify wg on PATH")
	}

	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "which wg 2>&1")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Errorf("wg not found on PATH: %v\noutput: %s", err, out)
	}
}
