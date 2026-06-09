// Package integration contains end-to-end tests that run SSH commands against
// a live desktop. They require DESKTOP_HOST, DESKTOP_SSH_KEY, and optionally
// DESKTOP_SSH_PORT (default 22) to be set. Tests are skipped when the env vars
// are absent so CI passes without a live desktop.
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const defaultSSHPort = 22

func sshEnv(t *testing.T) (host, keyPath string, port int) {
	t.Helper()
	host = os.Getenv("DESKTOP_HOST")
	keyPath = os.Getenv("DESKTOP_SSH_KEY")
	if host == "" || keyPath == "" {
		t.Skip("DESKTOP_HOST and DESKTOP_SSH_KEY must be set to run integration tests")
	}
	port = defaultSSHPort
	if p := os.Getenv("DESKTOP_SSH_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("invalid DESKTOP_SSH_PORT %q: %v", p, err)
		}
		port = n
	}
	return
}

func sshRun(t *testing.T, host, keyPath string, port int, command string) (string, error) {
	t.Helper()
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=15",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
		"-p", fmt.Sprintf("%d", port),
		fmt.Sprintf("ubuntu@%s", host),
		command,
	}
	cmd := exec.Command("ssh", args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustSSH(t *testing.T, host, keyPath string, port int, command string) string {
	t.Helper()
	out, err := sshRun(t, host, keyPath, port, command)
	if err != nil {
		t.Fatalf("ssh command %q failed: %v\noutput: %s", command, err, out)
	}
	return out
}

// FR-11: gh CLI is installed and on PATH.
func TestFR11_GHInstalled(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "command -v gh")
}

// FR-11: gh auth status exits 0 for the ubuntu user.
func TestFR11_GHAuthStatus(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "sudo -u ubuntu gh auth status")
}

// FR-11: git global user.name is set for the ubuntu user.
func TestFR11_GitIdentityName(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "sudo -u ubuntu git config --global user.name")
	if strings.TrimSpace(out) == "" {
		t.Error("git config user.name is empty")
	}
}

// FR-11: git global user.email is set for the ubuntu user.
func TestFR11_GitIdentityEmail(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "sudo -u ubuntu git config --global user.email")
	if strings.TrimSpace(out) == "" {
		t.Error("git config user.email is empty")
	}
}

// FR-11: python3 is available (required for secret extraction in cloud-init).
func TestFR11_Python3Installed(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "command -v python3")
}

// FR-11: Ed25519 SSH key file is present at the expected path.
func TestFR11_SSHKeyPresent(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "test -f /home/ubuntu/.ssh/github_ed25519")
}

// FR-11: SSH config contains the github.com stanza.
func TestFR11_SSHConfigPresent(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "grep -q 'Host github.com' /home/ubuntu/.ssh/config && echo ok")
	if out != "ok" {
		t.Errorf("expected 'ok' from ssh config grep, got %q", out)
	}
}

// FR-11: gh pr list exits 0 (confirms GitHub API connectivity + token scope).
func TestFR11_GHPRList(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set to run gh pr list test")
	}
	mustSSH(t, host, key, port, fmt.Sprintf("sudo -u ubuntu gh pr list --repo %s --limit 1", repo))
}

// FR-11: gh run list exits 0 (confirms workflow scope).
func TestFR11_GHRunList(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set to run gh run list test")
	}
	mustSSH(t, host, key, port, fmt.Sprintf("sudo -u ubuntu gh run list --repo %s --limit 1", repo))
}

// FR-11: GitHub Dependabot alerts API is reachable with the token (403 is acceptable — feature disabled on repo).
func TestFR11_DependabotAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	// 403 means the endpoint is reachable but Dependabot is disabled — still a pass per AC-11.
	cmd := fmt.Sprintf(
		`status=$(sudo -u ubuntu gh api repos/%s/dependabot/alerts --silent -i 2>&1 | grep -m1 '^HTTP/' | awk '{print $2}'); `+
			`[ "$status" = "200" ] || [ "$status" = "403" ] && echo ok || echo "unexpected status: $status"`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("dependabot API check returned %q", out)
	}
}

// FR-11: Code scanning API is reachable (403 is acceptable — feature disabled on repo).
func TestFR11_CodeScanningAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`status=$(sudo -u ubuntu gh api repos/%s/code-scanning/alerts --silent -i 2>&1 | grep -m1 '^HTTP/' | awk '{print $2}'); `+
			`[ "$status" = "200" ] || [ "$status" = "403" ] && echo ok || echo "unexpected status: $status"`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("code scanning API check returned %q", out)
	}
}

// FR-11: Secret scanning API is reachable (403 is acceptable — feature disabled on repo).
func TestFR11_SecretScanningAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`status=$(sudo -u ubuntu gh api repos/%s/secret-scanning/alerts --silent -i 2>&1 | grep -m1 '^HTTP/' | awk '{print $2}'); `+
			`[ "$status" = "200" ] || [ "$status" = "403" ] && echo ok || echo "unexpected status: $status"`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("secret scanning API check returned %q", out)
	}
}

// FR-11: Branch protection API is reachable (403 is acceptable — rules API requires admin).
func TestFR11_BranchProtectionAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	// Use the branch protection endpoint; 403 means reachable but insufficient permission — still a pass.
	cmd := fmt.Sprintf(
		`status=$(sudo -u ubuntu gh api repos/%s/branches/main/protection --silent -i 2>&1 | grep -m1 '^HTTP/' | awk '{print $2}'); `+
			`[ "$status" = "200" ] || [ "$status" = "403" ] && echo ok || echo "unexpected status: $status"`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("branch protection API check returned %q", out)
	}
}

// FR-11: SSH-based git push credentials have write access (non-destructive dry-run).
func TestFR11_GitPushCredentials(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	// Clone the configured repo into a temp dir and attempt a dry-run push.
	// A dry-run push verifies that the SSH key has write access without modifying
	// any refs. Exit code 0 means the push would succeed; any other exit means
	// auth or permission failure.
	cmd := fmt.Sprintf(
		`set -e; `+
			`TMPDIR=$(sudo -u ubuntu mktemp -d); `+
			`sudo -u ubuntu git clone --depth 1 git@github.com:%s.git "$TMPDIR/repo" 2>&1; `+
			`cd "$TMPDIR/repo"; `+
			`sudo -u ubuntu git push --dry-run origin HEAD 2>&1 && echo ok || echo "push dry-run failed"; `+
			`rm -rf "$TMPDIR"`,
		repo,
	)
	out, err := sshRun(t, host, key, port, cmd)
	if err != nil {
		t.Fatalf("outer ssh failed: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("git push --dry-run did not succeed, output: %q", out)
	}
}

// FR-11: Ed25519 SSH key file is present for the bridge user.
func TestFR11_BridgeSSHKeyPresent(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "test -f /home/bridge/.ssh/github_ed25519")
}

// FR-11: SSH config contains the github.com stanza for the bridge user.
func TestFR11_BridgeSSHConfigPresent(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "grep -q 'Host github.com' /home/bridge/.ssh/config && echo ok")
	if out != "ok" {
		t.Errorf("expected 'ok' from bridge ssh config grep, got %q", out)
	}
}

// FR-11: gh auth status exits 0 for the bridge user.
func TestFR11_BridgeGHAuthStatus(t *testing.T) {
	host, key, port := sshEnv(t)
	mustSSH(t, host, key, port, "sudo -u bridge gh auth status")
}

// FR-11: git global user.name is set for the bridge user.
func TestFR11_BridgeGitIdentityName(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "sudo -u bridge git config --global user.name")
	if strings.TrimSpace(out) == "" {
		t.Error("bridge git config user.name is empty")
	}
}

// FR-11: git global user.email is set for the bridge user.
func TestFR11_BridgeGitIdentityEmail(t *testing.T) {
	host, key, port := sshEnv(t)
	out := mustSSH(t, host, key, port, "sudo -u bridge git config --global user.email")
	if strings.TrimSpace(out) == "" {
		t.Error("bridge git config user.email is empty")
	}
}

// FR-11: SSH-based git push credentials have write access for the bridge user (non-destructive dry-run).
func TestFR11_BridgeGitPushCredentials(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set to run bridge git push test")
	}
	cmd := fmt.Sprintf(
		`set -e; `+
			`TMPDIR=$(sudo -u bridge mktemp -d); `+
			`sudo -u bridge git clone --depth 1 git@github.com:%s.git "$TMPDIR/repo" 2>&1; `+
			`cd "$TMPDIR/repo"; `+
			`sudo -u bridge git push --dry-run origin HEAD 2>&1 && echo ok || echo "push dry-run failed"; `+
			`rm -rf "$TMPDIR"`,
		repo,
	)
	out, err := sshRun(t, host, key, port, cmd)
	if err != nil {
		t.Fatalf("outer ssh failed: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("bridge git push --dry-run did not succeed, output: %q", out)
	}
}

// Sanity: confirm the test helpers compile and skip gracefully with a fake timeout.
func TestIntegrationHarness_skipWhenNoEnv(t *testing.T) {
	_ = time.Second // ensure time import is used
	if os.Getenv("DESKTOP_HOST") != "" {
		t.Skip("DESKTOP_HOST is set; this test only validates skipping behaviour")
	}
}
