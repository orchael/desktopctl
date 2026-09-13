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
//	AC-5.6  `brew` binary is present at /home/linuxbrew/.linuxbrew/bin/brew
//	AC-5.7  `brew` is on PATH in a login shell and reports a version
package integration_test

import (
	"strings"
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

// TestFR5_NvChadConfigured verifies the AMI-managed editor starts headlessly
// with Catppuccin selected (AC-9.7).
func TestFR5_NvChadConfigured(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("NvChad AMI validation requires a desktop created from this test run's AMI")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify NvChad")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		`sudo -u ubuntu -H nvim --headless '+qa' && grep -q 'theme = "catppuccin"' /home/ubuntu/.config/nvim/lua/chadrc.lua`)
	if err != nil {
		t.Fatalf("NvChad headless startup or Catppuccin verification failed: %v\noutput: %s", err, out)
	}
}

// TestFR5_DeveloperTerminalWorkflow verifies editor choice, managed ownership,
// plugin installation, and detached tmux startup (AC-9.8, AC-9.9).
func TestFR5_DeveloperTerminalWorkflow(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("terminal workflow AMI validation requires a desktop created from this test run's AMI")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify terminal workflow")
	}
	script := `set -eu
test "$(sudo -u ubuntu -H gh config get editor)" = vim
test "$(stat -c '%U:%G' /home/ubuntu/.tmux.conf)" = ubuntu:ubuntu
test "$(stat -c '%U:%G' /home/ubuntu/.config/nvim)" = ubuntu:ubuntu
for plugin in tpm tmux-sensible tmux tmux-cpu tmux-kubectx tmux-resurrect tmux-continuum; do
  test -d "/home/ubuntu/.tmux/plugins/$plugin/.git"
  test "$(stat -c '%U:%G' "/home/ubuntu/.tmux/plugins/$plugin")" = ubuntu:ubuntu
done
sudo -u ubuntu -H tmux -L ai-desktops-test -f /home/ubuntu/.tmux.conf new-session -d -s ai-desktops-test
test "$(sudo -u ubuntu -H tmux -L ai-desktops-test show-options -gv prefix)" = C-a
sudo -u ubuntu -H tmux -L ai-desktops-test kill-server`
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, script)
	if err != nil {
		t.Fatalf("developer terminal workflow verification failed: %v\noutput: %s", err, out)
	}
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

// TestFR5_BrewBinaryPresent verifies that the Homebrew binary was installed
// into the AMI at the expected Linuxbrew path (AC-5.6).
func TestFR5_BrewBinaryPresent(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify Homebrew")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "test -x /home/linuxbrew/.linuxbrew/bin/brew && echo ok")
	if err != nil || strings.TrimSpace(out) != "ok" {
		t.Errorf("brew binary not present at /home/linuxbrew/.linuxbrew/bin/brew: err=%v output=%q", err, out)
	}
}

// TestFR5_BrewOnLoginPath verifies that `brew` is on PATH in a login shell
// (i.e. /etc/profile.d/homebrew.sh is sourced) and reports a version (AC-5.7).
func TestFR5_BrewOnLoginPath(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify Homebrew")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "bash -l -c 'brew --version 2>&1 | head -1'")
	if err != nil {
		t.Errorf("brew not on PATH in login shell: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Homebrew") {
		t.Errorf("brew --version output does not contain 'Homebrew': %q", out)
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
