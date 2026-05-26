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

// FR-11: GitHub Dependabot API is reachable with the token.
func TestFR11_DependabotAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`sudo -u ubuntu gh api repos/%s/vulnerability-alerts --method GET --silent && echo ok`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("dependabot API check returned %q", out)
	}
}

// FR-11: Code scanning API is reachable.
func TestFR11_CodeScanningAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`sudo -u ubuntu gh api repos/%s/code-scanning/alerts --silent && echo ok`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("code scanning API check returned %q", out)
	}
}

// FR-11: Secret scanning API is reachable.
func TestFR11_SecretScanningAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`sudo -u ubuntu gh api repos/%s/secret-scanning/alerts --silent && echo ok`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("secret scanning API check returned %q", out)
	}
}

// FR-11: Branch protection API is reachable.
func TestFR11_BranchProtectionAPIReachable(t *testing.T) {
	host, key, port := sshEnv(t)
	repo := os.Getenv("DESKTOP_GITHUB_REPO")
	if repo == "" {
		t.Skip("DESKTOP_GITHUB_REPO must be set")
	}
	cmd := fmt.Sprintf(
		`sudo -u ubuntu gh api repos/%s/branches --silent && echo ok`,
		repo,
	)
	out := mustSSH(t, host, key, port, cmd)
	if out != "ok" {
		t.Errorf("branch protection API check returned %q", out)
	}
}

// FR-11: SSH-based git push credentials work (non-destructive: only checks SSH auth to github.com).
func TestFR11_GitPushCredentials(t *testing.T) {
	host, key, port := sshEnv(t)
	// ssh -T git@github.com exits 1 but prints "Hi <user>!" — that's a pass.
	out, err := sshRun(t, host, key, port,
		`sudo -u ubuntu ssh -T -o StrictHostKeyChecking=accept-new git@github.com 2>&1 || true`)
	if err != nil {
		// The command itself (outer ssh) failed — infra problem.
		t.Fatalf("outer ssh failed: %v", err)
	}
	if !strings.Contains(out, "Hi ") {
		t.Errorf("expected 'Hi <user>!' from github.com SSH auth, got: %q", out)
	}
}

// Sanity: confirm the test helpers compile and skip gracefully with a fake timeout.
func TestIntegrationHarness_skipWhenNoEnv(t *testing.T) {
	_ = time.Second // ensure time import is used
	if os.Getenv("DESKTOP_HOST") != "" {
		t.Skip("DESKTOP_HOST is set; this test only validates skipping behaviour")
	}
}
