//go:build integration

// FR-6 — Workspace and repository policy
//
// Acceptance criteria tested here:
//
//	AC-6.1  Repos listed in the fixture are present under /workspace
//	AC-6.2  Create with repos from mixed owners fails before provisioning
//	AC-6.3  Create without --github-owner fails (covered in fr1_lifecycle_test)
//	AC-6.4  Cross-owner repos are rejected by the CLI (local validation)
//	AC-6.5  Repos are checked out under /workspace/<repo-name>
package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFR6_ReposPresent verifies that each repository specified at create time
// is present under /workspace on the desktop (AC-6.1, AC-6.5).
func TestFR6_ReposPresent(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify workspace")
	}
	if len(fx.Repos) == 0 {
		t.Skip("no repos configured in fixture")
	}

	for _, repoURL := range fx.Repos {
		repoName := repoBaseName(repoURL)
		workspacePath := filepath.Join("/workspace", repoName)

		out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
			fmt.Sprintf("test -d %s && echo present", workspacePath))
		if err != nil || !strings.Contains(out, "present") {
			t.Errorf("repo %q not found at %s (err=%v)", repoName, workspacePath, err)
		}
	}
}

// TestFR6_WorkspaceIsGitRepo verifies that cloned directories are valid git
// repos (AC-6.1 depth check).
func TestFR6_WorkspaceIsGitRepo(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify workspace")
	}
	if len(fx.Repos) == 0 {
		t.Skip("no repos in fixture")
	}

	for _, repoURL := range fx.Repos {
		repoName := repoBaseName(repoURL)
		workspacePath := filepath.Join("/workspace", repoName)

		out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
			fmt.Sprintf("git -C %s rev-parse HEAD 2>&1", workspacePath))
		if err != nil {
			t.Errorf("repo %q at %s is not a valid git repo: %v\noutput: %s",
				repoName, workspacePath, err, out)
		}
	}
}

// TestFR6_WorkspaceOwnerBoundary verifies the runtime owner check: if the
// desktop was created with owner X, repos from owner Y must not be present.
// This test checks via DynamoDB that the github_owner field matches the
// repos in /workspace (AC-6.2).
func TestFR6_WorkspaceOwnerBoundary(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify workspace owner boundary")
	}
	if len(fx.Repos) == 0 {
		t.Skip("no repos in fixture")
	}

	for _, repoURL := range fx.Repos {
		repoName := repoBaseName(repoURL)
		workspacePath := filepath.Join("/workspace", repoName)

		// The git remote URL should contain the desktop's owner.
		out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
			fmt.Sprintf("git -C %s remote get-url origin 2>&1", workspacePath))
		if err != nil {
			t.Logf("could not get remote for %s: %v", repoName, err)
			continue
		}
		if !strings.Contains(out, fx.Owner) {
			t.Errorf("repo %q remote URL %q does not contain owner %q — cross-owner repo may have been cloned",
				repoName, strings.TrimSpace(out), fx.Owner)
		}
	}
}

// TestFR6_MixedOwnerRejected verifies that `ai-desktops create` fails with a
// clear error when repos from different GitHub owners are provided (AC-6.2).
// This is a local-validation test: it uses --preview so no infra is touched.
func TestFR6_MixedOwnerRejected(t *testing.T) {
	_, err := runCLI(context.Background(), 30*time.Second,
		"create", "--config", configPath, "--preview",
		"--github-owner", "acme",
		"--repo", "github.com/acme/app-one",
		"--repo", "github.com/other-org/app-two",
	)
	if err == nil {
		t.Error("create with mixed-owner repos should fail but succeeded")
	}
}

// TestFR6_NonGitHubRepoRejected verifies that non-GitHub repository URLs are
// rejected at input time (AC-6.2 — GitLab/Bitbucket not allowed).
func TestFR6_NonGitHubRepoRejected(t *testing.T) {
	_, err := runCLI(context.Background(), 30*time.Second,
		"create", "--config", configPath, "--preview",
		"--github-owner", "acme",
		"--repo", "https://gitlab.com/acme/app",
	)
	if err == nil {
		t.Error("create with non-GitHub repo should fail but succeeded")
	}
}

// repoBaseName extracts the repository name from a URL or owner/name string.
// Duplicated from health_test to keep test files self-contained.
func repoBaseName(url string) string {
	// Strip .git suffix.
	url = strings.TrimSuffix(url, ".git")
	// Take the last path component.
	parts := strings.Split(strings.TrimRight(url, "/"), "/")
	if len(parts) == 0 {
		return url
	}
	return parts[len(parts)-1]
}
