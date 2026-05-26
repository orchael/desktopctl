//go:build integration

// FR-5 — Base toolchain
//
// Acceptance criteria tested here:
//
//	AC-5.1  `git` is on PATH and reports a version
//	AC-5.2  `docker` is on PATH and the daemon is active
//	AC-5.3  `nvim` is on PATH and reports a version
//	AC-5.4  `tmux` is on PATH and reports a version
//	AC-5.5  Desktop is in state=ready only after all tools are confirmed present
//	         (verified by AC-5.1–5.4 running against a ready desktop)
package integration_test

import (
	"testing"
)

// TestFR5_GitInstalled verifies that git is installed and executable (AC-5.1).
func TestFR5_GitInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify toolchain")
	}
	assertCommandExists(t, fx.SSHTarget, fx.SSHKey, "git", "--version")
}

// TestFR5_DockerInstalled verifies that docker is installed (AC-5.2).
func TestFR5_DockerInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify toolchain")
	}
	assertCommandExists(t, fx.SSHTarget, fx.SSHKey, "docker", "--version")
}

// TestFR5_DockerDaemonActive verifies that the docker daemon is running and
// reachable by the ubuntu user (AC-5.2).
func TestFR5_DockerDaemonActive(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify Docker daemon")
	}
	assertSystemdActive(t, fx.SSHTarget, fx.SSHKey, "docker")
	// Also verify the ubuntu user can run docker info without sudo.
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "docker info 2>&1 | head -3")
	if err != nil {
		t.Errorf("docker info failed (ubuntu user may not be in docker group): %v\noutput: %s", err, out)
	}
}

// TestFR5_NvimInstalled verifies that nvim is installed and executable (AC-5.3).
func TestFR5_NvimInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify toolchain")
	}
	// nvim --version exits 0 and prints version string.
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "which nvim && nvim --version 2>&1 | head -1")
	if err != nil {
		t.Errorf("nvim not found: %v\noutput: %s", err, out)
	}
}

// TestFR5_TmuxInstalled verifies that tmux is installed and executable (AC-5.4).
func TestFR5_TmuxInstalled(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify toolchain")
	}
	assertCommandExists(t, fx.SSHTarget, fx.SSHKey, "tmux", "-V")
}

// TestFR5_AllToolsOnPath verifies that every required tool is on PATH in a
// single SSH round-trip, confirming FR-5.5.
func TestFR5_AllToolsOnPath(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify toolchain")
	}
	script := "which git docker nvim tmux 2>&1"
	out := sshRun(t, fx.SSHTarget, fx.SSHKey, script)

	required := []string{"git", "docker", "nvim", "tmux"}
	for _, tool := range required {
		found := false
		for _, line := range splitLines(out) {
			if len(line) > 0 && (containsSuffix(line, "/"+tool) || line == tool) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tool %q not on PATH; which output:\n%s", tool, out)
		}
	}
}

func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func containsSuffix(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
