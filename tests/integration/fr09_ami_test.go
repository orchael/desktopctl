//go:build integration

// FR-9 — Pre-baked AMI with Packer
//
// Acceptance criteria tested here:
//
//	AC-9.1  `ai-desktops ami build` command exists and can be invoked
//	AC-9.6  `ai-desktops ami build --help` succeeds (confirms command registration)
//	AC-9.7  `create --preview --ami <id>` references the supplied AMI in output
//	         (partial: flag-override path only; config-based selection requires a separate test)
//	AC-9.5  Helm is installed in the AMI through Linuxbrew
//
// Note: AC-9.1 full smoke (actually running Packer to build AMIs) is an
// expensive long-running operation.  The full AMI build test only runs when
// AI_DESKTOPS_RUN_AMI_BUILD=true is set.  The default test validates the
// command is wired and config round-trips correctly.
package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFR9_AMIBuildCommandExists verifies that the `ami build` subcommand is
// registered in the CLI (AC-9.6).
func TestFR9_AMIBuildCommandExists(t *testing.T) {
	out, err := runCLI(context.Background(), 10*time.Second,
		"ami", "build", "--help")
	if err != nil {
		t.Fatalf("ami build --help failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(out)), "packer") &&
		!strings.Contains(strings.ToLower(string(out)), "ami") {
		t.Errorf("ami build --help output doesn't mention packer or ami\nraw: %s", out)
	}
}

// TestFR9_AMIListCommandExists verifies that `ami list` is registered (AC-9.6).
func TestFR9_AMIListCommandExists(t *testing.T) {
	_, err := runCLI(context.Background(), 10*time.Second,
		"ami", "list", "--help")
	if err != nil {
		t.Fatalf("ami list --help failed: %v", err)
	}
}

// TestFR9_HelmInstalled verifies that Helm is present at the Linuxbrew path in
// the built AMI and can report its version (AC-9.5).
func TestFR9_HelmInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify Helm")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		"/home/linuxbrew/.linuxbrew/bin/brew list --versions helm >/dev/null && /home/linuxbrew/.linuxbrew/bin/helm version --short")
	if err != nil {
		t.Fatalf("Helm is not installed through Linuxbrew: %v\noutput: %s", err, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "v") {
		t.Errorf("unexpected helm version output: %q", out)
	}
}

// TestFR9_CreateAMIFlagOverride verifies that `create --preview --ami <id>`
// references the explicitly supplied AMI ID in its output.
//
// Note: this test exercises the --ami flag override path, not config-based
// AMI selection (AC-9.7). A separate test wiring the AMI into config.yaml
// is needed to cover AC-9.7 end-to-end.
//
// This test requires that AI_DESKTOPS_AMI_ID is set to a known AMI ID for
// the test region. If not set, the test is skipped.
func TestFR9_CreateAMIFlagOverride(t *testing.T) {
	amiID := os.Getenv("AI_DESKTOPS_AMI_ID")
	if amiID == "" {
		t.Skip("AI_DESKTOPS_AMI_ID not set — skipping AMI flag-override test")
	}

	out, err := runCLI(context.Background(), 30*time.Second,
		"create", "--config", configPath, "--preview",
		"--github-owner", fx.Owner,
		"--ami", amiID,
	)
	// Preview exits non-zero (no pulumi state); we check stdout for the AMI.
	_ = err
	if !strings.Contains(string(out), amiID) {
		t.Errorf("create --preview output does not reference AMI %q\nraw: %s", amiID, out)
	}
}

// TestFR9_AMIBuildFull runs the full `ami build` command.  It is guarded
// behind AI_DESKTOPS_RUN_AMI_BUILD=true because the Packer build takes
// 15–20 minutes and incurs cloud cost (AC-9.1, AC-9.2, AC-9.3).
func TestFR9_AMIBuildFull(t *testing.T) {
	if os.Getenv("AI_DESKTOPS_RUN_AMI_BUILD") != "true" {
		t.Skip("set AI_DESKTOPS_RUN_AMI_BUILD=true to run full AMI build test")
	}
	_, err := runCLI(context.Background(), 45*time.Minute,
		"ami", "build", "--config", configPath)
	if err != nil {
		t.Fatalf("ami build: %v", err)
	}
}
