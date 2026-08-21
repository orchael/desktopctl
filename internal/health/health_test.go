package health

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestRunner_Names(t *testing.T) {
	a := NewFnChecker("alpha", func(ctx context.Context) error { return nil })
	b := NewFnChecker("beta", func(ctx context.Context) error { return nil })
	r := NewRunner("d-test", a, b)
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestRunner_Callbacks(t *testing.T) {
	pass := NewFnChecker("p", func(ctx context.Context) error { return nil })
	fail := NewFnChecker("f", func(ctx context.Context) error { return errors.New("oops") })

	var started, completed []string
	r := NewRunner("d-cb", pass, fail)
	r.OnStart = func(name string, idx, total int) {
		started = append(started, name)
	}
	r.OnComplete = func(result CheckResult, idx, total int) {
		completed = append(completed, result.Name)
	}

	r.Run(context.Background())

	if len(started) != 2 {
		t.Errorf("OnStart called %d times, want 2", len(started))
	}
	if len(completed) != 2 {
		t.Errorf("OnComplete called %d times, want 2", len(completed))
	}
	if started[0] != "p" || started[1] != "f" {
		t.Errorf("unexpected start order: %v", started)
	}
}

func TestSystemCheckers_returnsCheckers(t *testing.T) {
	checkers := SystemCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
	if len(checkers) == 0 {
		t.Error("SystemCheckers must return at least one checker")
	}
	for _, c := range checkers {
		if c.Name() == "" {
			t.Error("checker must have a name")
		}
	}
}

func TestWorkspaceCheckers_returnsCheckers(t *testing.T) {
	checkers := WorkspaceCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
	if len(checkers) == 0 {
		t.Error("WorkspaceCheckers must return at least one checker")
	}
	for _, c := range checkers {
		if c.Name() == "" {
			t.Error("checker must have a name")
		}
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

func TestNoVNCCheckers_returnsExpectedChecks(t *testing.T) {
	hostname := "d-001.desktops.orchael.dev"
	checkers := NoVNCCheckers(hostname, 22, "ubuntu", "")
	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, n := range []string{"novnc-https", "novnc-running"} {
		if !names[n] {
			t.Errorf("NoVNCCheckers missing expected checker %q", n)
		}
	}
}

func TestNoVNCCheckers_httpsPort8443(t *testing.T) {
	// Verify the noVNC URL uses port 8443, not the default 443.
	hostname := "d-001.desktops.orchael.dev"
	checkers := NoVNCCheckers(hostname, 22, "ubuntu", "")
	found := false
	for _, c := range checkers {
		if c.Name() == "novnc-https" {
			found = true
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
		t.Error("novnc-https checker not found in NoVNCCheckers output")
	}
}

func TestSSHChecker_skippedWhenNoKey(t *testing.T) {
	c := NewSSHChecker("docker-active", "host", 22, "ubuntu", "", "systemctl is-active docker", 5*1e9)
	result := c.Run(context.Background())
	if result.Status != StatusSkipped {
		t.Errorf("expected skipped when keyPath is empty, got %q", result.Status)
	}
}

func TestSSHOptionalChecker_skippedWhenNoKey(t *testing.T) {
	c := NewSSHOptionalChecker("swap-active", "host", 22, "ubuntu", "",
		"test -f /swapfile", "no swap configured",
		"swapon --show --noheadings | grep -q '^/swapfile'", 5*1e9)
	result := c.Run(context.Background())
	if result.Status != StatusSkipped {
		t.Errorf("expected skipped when keyPath is empty, got %q", result.Status)
	}
}

func TestSSHOptionalChecker_failsOnTransportError(t *testing.T) {
	// Use localhost on port 1 — connection refused is immediate, causing SSH
	// to exit 255 (transport failure) before our context times out.
	// The checker must return StatusFail, not StatusSkipped.
	c := NewSSHOptionalChecker("swap-active", "127.0.0.1", 1, "ubuntu", "/dev/null",
		"test -f /swapfile", "no swap configured",
		"swapon --show --noheadings | grep -q '^/swapfile'", 10*time.Second)
	result := c.Run(context.Background())
	if result.Status != StatusFail {
		t.Errorf("expected fail on SSH transport error, got %q (msg: %s)", result.Status, result.Message)
	}
}

func TestSystemCheckers_includesSwapAndCloudWatch(t *testing.T) {
	checkers := SystemCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "key")
	names := make(map[string]bool, len(checkers))
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, want := range []string{"swap-active", "cloudwatch-agent-active"} {
		if !names[want] {
			t.Errorf("SystemCheckers missing %q", want)
		}
	}
}

func TestSSHCheckers_returnsExpectedChecks(t *testing.T) {
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")

	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}

	required := []string{
		"docker-active", "nvim-installed", "tmux-installed",
		"gh-installed", "gh-auth", "python3-installed",
		"git-identity", "ssh-key-present",
	}
	for _, n := range required {
		if !names[n] {
			t.Errorf("SSHCheckers missing expected checker %q", n)
		}
	}
}

func TestSSHCheckers_githubToolingPresent(t *testing.T) {
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
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
	checkers := SSHCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
	for _, c := range checkers {
		result := c.Run(context.Background())
		if result.Status != StatusSkipped {
			t.Errorf("checker %q: expected skipped with empty key, got %q", c.Name(), result.Status)
		}
	}
}

func TestRepoCheckers_returnsExpectedChecks(t *testing.T) {
	repos := []string{"github.com/acme/app-one", "github.com/acme/app-two"}
	checkers := RepoCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", repos)

	if len(checkers) != 2 {
		t.Fatalf("expected 2 repo checkers, got %d", len(checkers))
	}
	names := map[string]bool{}
	for _, c := range checkers {
		names[c.Name()] = true
	}
	if !names["repo-app-one"] || !names["repo-app-two"] {
		t.Errorf("unexpected checker names: %v", names)
	}
}

func TestRepoCheckers_emptyRepos(t *testing.T) {
	checkers := RepoCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", nil)
	if len(checkers) != 0 {
		t.Errorf("expected 0 checkers for empty repos, got %d", len(checkers))
	}
}

func TestBridgectlCheckers_returnsExpectedChecks(t *testing.T) {
	checkers := BridgectlCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}
	required := []string{
		"bridgectl-installed",
		"bridgectl-config-exists",
		"bridgectl-credentials-env",
		"bridgectl-claude-configured",
		"bridgectl-codex-configured",
		"bridgectl-display-env",
		"bridgectl-service-active",
	}
	for _, n := range required {
		if !names[n] {
			t.Errorf("BridgectlCheckers missing expected checker %q", n)
		}
	}
}

func TestBridgectlCheckers_allSkippedWithNoKey(t *testing.T) {
	checkers := BridgectlCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "")
	for _, c := range checkers {
		result := c.Run(context.Background())
		if result.Status != StatusSkipped {
			t.Errorf("checker %q: expected skipped with empty key, got %q", c.Name(), result.Status)
		}
	}
}

func TestTailscaleCheckers_emptyNetwork(t *testing.T) {
	checkers := TailscaleCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", "", 9445)
	if len(checkers) != 0 {
		t.Errorf("expected 0 checkers for empty Tailscale network, got %d", len(checkers))
	}
}

func TestTailscaleCheckers_returnsExpectedChecks(t *testing.T) {
	checkers := TailscaleCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", "acme-tailnet", 9445)
	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, n := range []string{"tailscale-installed", "tailscaled-active", "tailscale-running", "tailscale-network-metadata", "bridgectl-tailscale-listener", "bridgectl-tailscale-san"} {
		if !names[n] {
			t.Errorf("TailscaleCheckers missing expected checker %q", n)
		}
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-listener", `listen: \"${TAILSCALE_IP}:9445\"`) {
		t.Error("bridgectl-tailscale-listener should accept quoted YAML listener values")
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-listener", `listen: ${TAILSCALE_IP}:9445`) {
		t.Error("bridgectl-tailscale-listener should accept unquoted YAML listener values")
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-san", `server') or {}).get('san')`) {
		t.Error("bridgectl-tailscale-san should verify server.san in config")
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-san", `openssl x509`) {
		t.Error("bridgectl-tailscale-san should verify the issued certificate SAN")
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-san", `entries=[part.strip()`) {
		t.Error("bridgectl-tailscale-san should parse certificate SAN entries")
	}
	if !checkerCommandContains(checkers, "bridgectl-tailscale-san", `want in entries`) {
		t.Error("bridgectl-tailscale-san should require an exact certificate SAN match")
	}
}

func TestStepCACheckers_emptyServer(t *testing.T) {
	checkers := StepCACheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", "")
	if len(checkers) != 0 {
		t.Errorf("expected 0 checkers for empty step-ca server, got %d", len(checkers))
	}
}

func TestStepCACheckers_returnsExpectedChecks(t *testing.T) {
	checkers := StepCACheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", "ca.tailnet.ts.net")
	names := make(map[string]bool)
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, n := range []string{"step-cli-installed", "step-ca-resolves", "step-ca-health", "bridgectl-step-ca-env", "bridgectl-step-ca-cert"} {
		if !names[n] {
			t.Errorf("StepCACheckers missing expected checker %q", n)
		}
	}
	if !checkerCommandContains(checkers, "step-ca-health", "--root /root/.step/certs/root_ca.crt") {
		t.Error("step-ca-health should pass the bootstrapped root certificate explicitly")
	}
	if !checkerCommandContains(checkers, "step-ca-health", "--root /etc/ssl/certs/ISRG_Root_X1.pem") {
		t.Error("step-ca-health should fall back to ISRG Root X1")
	}
	if !checkerCommandContains(checkers, "step-ca-health", "--root /etc/ssl/certs/ISRG_Root_X2.pem") {
		t.Error("step-ca-health should fall back to ISRG Root X2")
	}
}

func TestAVDCheckers_nilWhenEmpty(t *testing.T) {
	checkers := AVDCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", nil)
	if checkers != nil {
		t.Errorf("expected nil for empty AVD names, got %d checkers", len(checkers))
	}
}

func TestAVDCheckers_returnsExpectedChecks(t *testing.T) {
	avdNames := []string{"flutter_dev", "pixel_test"}
	checkers := AVDCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", avdNames)

	// avdmanager-installed + one per AVD
	if len(checkers) != 3 {
		t.Fatalf("expected 3 checkers (1 + 2 AVDs), got %d", len(checkers))
	}
	names := make(map[string]bool, len(checkers))
	for _, c := range checkers {
		names[c.Name()] = true
	}
	for _, want := range []string{"avdmanager-installed", "avd-flutter_dev", "avd-pixel_test"} {
		if !names[want] {
			t.Errorf("AVDCheckers missing expected checker %q; got %v", want, names)
		}
	}
}

func TestAVDCheckers_allSkippedWithNoKey(t *testing.T) {
	checkers := AVDCheckers("d-001.desktops.orchael.dev", 22, "ubuntu", "", []string{"flutter_dev"})
	for _, c := range checkers {
		result := c.Run(context.Background())
		if result.Status != StatusSkipped {
			t.Errorf("checker %q: expected skipped with empty key, got %q (msg: %s)",
				c.Name(), result.Status, result.Message)
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

func checkerCommandContains(checkers []Checker, name, want string) bool {
	for _, checker := range checkers {
		if checker.Name() != name {
			continue
		}
		switch c := checker.(type) {
		case *SSHChecker:
			return strings.Contains(c.command, want)
		case *SSHOptionalChecker:
			return strings.Contains(c.command, want)
		default:
			return false
		}
	}
	return false
}
