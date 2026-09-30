//go:build integration

// FR-9 — Pre-baked AMI with Packer
//
// Acceptance criteria tested here:
//
//	AC-9.1  `ai-desktops ami build` command exists and can be invoked
//	AC-9.1  `ai-desktops ami build --help` succeeds (confirms command registration)
//	AC-9.3  `create --preview --ami <id>` references the supplied AMI in output
//	AC-9.5  Helm is installed in the AMI through Linuxbrew
//	AC-9.11 VS Code stable package and desktop launcher are installed
//
// Note: AC-9.1 full smoke (actually running Packer to build AMIs) is an
// expensive long-running operation.  The full AMI build test only runs when
// AI_DESKTOPS_RUN_AMI_BUILD=true is set.  The default test validates the
// command is wired and config round-trips correctly.
package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestFR9_AMIBuildCommandExists verifies that the `ami build` subcommand is
// registered in the CLI (AC-9.1).
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

// TestFR9_AMIListCommandExists verifies that `ami list` is registered (AC-9.2).
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
	if !fx.ownedByTest {
		t.Skip("Helm AMI validation requires a desktop created from this test run's AMI")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify Helm")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		"/home/linuxbrew/.linuxbrew/bin/brew list --versions helm >/dev/null && /home/linuxbrew/.linuxbrew/bin/helm version --short")
	if err != nil {
		t.Fatalf("Helm is not installed through Linuxbrew: %v\noutput: %s", err, out)
	}
	variables, err := os.ReadFile(filepath.Join(moduleRootPath, "packer", "variables.pkrvars.hcl"))
	if err != nil {
		t.Fatalf("read Packer variables: %v", err)
	}
	match := regexp.MustCompile(`(?m)^helm_version\s*=\s*"([^"]+)"\s*$`).FindSubmatch(variables)
	if len(match) != 2 {
		t.Fatal("Packer variables must contain one helm_version pin")
	}
	got := strings.SplitN(strings.TrimSpace(out), "+", 2)[0]
	if want := string(match[1]); got != want {
		t.Errorf("helm version = %q, want configured pin %q", got, want)
	}
}

// TestFR9_VSCodeInstalled verifies the pinned package and usable desktop
// launcher on a desktop created from this test run's AMI (AC-9.11).
func TestFR9_VSCodeInstalled(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("VS Code AMI validation requires a desktop created from this test run's AMI")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify VS Code")
	}
	variables, err := os.ReadFile(filepath.Join(moduleRootPath, "packer", "variables.pkrvars.hcl"))
	if err != nil {
		t.Fatalf("read Packer variables: %v", err)
	}
	match := regexp.MustCompile(`(?m)^vscode_version\s*=\s*"([^"]+)"\s*$`).FindSubmatch(variables)
	if len(match) != 2 {
		t.Fatal("Packer variables must contain one vscode_version pin")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		"dpkg-query -W -f='${Version}' code && echo && desktop-file-validate /usr/share/applications/code.desktop && code --version")
	if err != nil {
		t.Fatalf("VS Code package or launcher validation failed: %v\noutput: %s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("VS Code validation returned too few lines: %q", out)
	}
	if got, want := lines[0], string(match[1]); got != want {
		t.Errorf("VS Code package version = %q, want %q", got, want)
	}
	if got, want := lines[1], strings.SplitN(string(match[1]), "-", 2)[0]; got != want {
		t.Errorf("VS Code CLI version = %q, want %q", got, want)
	}
}

// TestFR9_CreateAMIFlagOverride verifies that `create --preview --ami <id>`
// references the explicitly supplied AMI ID in its output.
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
// 15–20 minutes and incurs cloud cost (AC-9.4).
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
