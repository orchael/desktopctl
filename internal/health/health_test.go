package health

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBuildReport_allPass(t *testing.T) {
	results := []CheckResult{
		{Name: "ssh", Status: StatusPass},
		{Name: "novnc", Status: StatusPass},
	}
	r := buildReport("d-001", results)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "all checks passed" {
		t.Errorf("summary: got %q", r.Summary)
	}
}

func TestBuildReport_withFailures(t *testing.T) {
	results := []CheckResult{
		{Name: "ssh", Status: StatusPass},
		{Name: "novnc", Status: StatusFail, Message: "timeout"},
		{Name: "docker", Status: StatusFail, Message: "not active"},
	}
	r := buildReport("d-002", results)
	if r.Passed {
		t.Error("expected passed=false")
	}
	if r.Summary == "all checks passed" {
		t.Error("summary should not say all passed")
	}
}

func TestBuildReport_skippedDoesNotFail(t *testing.T) {
	results := []CheckResult{
		{Name: "ssh", Status: StatusPass},
		{Name: "docker-active", Status: StatusSkipped, Message: "no SSH key configured"},
	}
	r := buildReport("d-003", results)
	if !r.Passed {
		t.Error("skipped checks must not cause report to fail")
	}
}

func TestFnChecker_pass(t *testing.T) {
	c := NewFnChecker("test-pass", func(ctx context.Context) error {
		return nil
	})
	result := c.Run(context.Background())
	if result.Status != StatusPass {
		t.Errorf("status: got %q", result.Status)
	}
}

func TestFnChecker_fail(t *testing.T) {
	c := NewFnChecker("test-fail", func(ctx context.Context) error {
		return errors.New("something broke")
	})
	result := c.Run(context.Background())
	if result.Status != StatusFail {
		t.Errorf("status: got %q", result.Status)
	}
	if result.Message != "something broke" {
		t.Errorf("message: got %q", result.Message)
	}
}

func TestRunner(t *testing.T) {
	pass := NewFnChecker("pass", func(ctx context.Context) error { return nil })
	fail := NewFnChecker("fail", func(ctx context.Context) error { return errors.New("bad") })

	r := NewRunner("d-test", pass, fail)
	report := r.Run(context.Background())

	if report.Passed {
		t.Error("expected report to not be passed")
	}
	if len(report.Checks) != 2 {
		t.Errorf("got %d checks", len(report.Checks))
	}
}

func TestStandardCheckers(t *testing.T) {
	checkers := StandardCheckers("d-001.desktops.orchael.dev", 22)
	if len(checkers) == 0 {
		t.Error("expected at least one standard checker")
	}
	for _, c := range checkers {
		if c.Name() == "" {
			t.Error("checker must have a name")
		}
	}
}

func TestStandardCheckers_noVNCPort(t *testing.T) {
	// Verify the noVNC URL uses port 8443, not the default 443.
	hostname := "d-001.desktops.orchael.dev"
	checkers := StandardCheckers(hostname, 22)
	found := false
	for _, c := range checkers {
		if c.Name() == "novnc-https" {
			found = true
			// HTTPSChecker exposes the url field for verification.
			h, ok := c.(*HTTPSChecker)
			if !ok {
				t.Fatal("novnc-https checker is not an HTTPSChecker")
			}
			if !strings.Contains(h.url, ":8443") {
				t.Errorf("novnc-https URL must include :8443, got %q", h.url)
			}
		}
	}
	if !found {
		t.Error("novnc-https checker not found in StandardCheckers output")
	}
}

func TestSSHChecker_skippedWhenNoKey(t *testing.T) {
	c := NewSSHChecker("docker-active", "host", 22, "ubuntu", "", "systemctl is-active docker", 5*1e9)
	result := c.Run(context.Background())
	if result.Status != StatusSkipped {
		t.Errorf("expected skipped when keyPath is empty, got %q", result.Status)
	}
}

func TestSSHCheckers_returnsExpectedChecks(t *testing.T) {
	repos := []string{"github.com/acme/app-one", "github.com/acme/app-two"}
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", repos)

	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}

	required := []string{
		"docker-active", "nvim-installed", "tmux-installed",
		"gh-installed", "gh-auth", "python3-installed",
		"git-identity", "ssh-key-present",
		"repo-app-one", "repo-app-two",
	}
	for _, n := range required {
		if !names[n] {
			t.Errorf("SSHCheckers missing expected checker %q", n)
		}
	}
}

func TestSSHCheckers_githubToolingPresent(t *testing.T) {
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", nil)
	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, n := range []string{"gh-installed", "gh-auth", "python3-installed", "git-identity", "ssh-key-present"} {
		if !names[n] {
			t.Errorf("SSHCheckers missing github-tooling checker %q", n)
		}
	}
}

func TestSSHCheckers_allSkippedWithNoKey(t *testing.T) {
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", nil)
	for _, c := range checkers {
		result := c.Run(context.Background())
		if result.Status != StatusSkipped {
			t.Errorf("checker %q: expected skipped with empty key, got %q", c.Name(), result.Status)
		}
	}
}

func TestRepoBaseName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"github.com/acme/app-one", "app-one"},
		{"https://github.com/acme/app-two.git", "app-two"},
		{"acme/myrepo.git", "myrepo"},
	}
	for _, tc := range cases {
		if got := repoBaseName(tc.input); got != tc.want {
			t.Errorf("repoBaseName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
