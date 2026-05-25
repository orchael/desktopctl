package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// Status represents the outcome of a single health check.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// CheckResult is the result of one named health check.
type CheckResult struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Report collects results from all health checks.
type Report struct {
	DesktopID string        `json:"desktop_id"`
	Checks    []CheckResult `json:"checks"`
	Passed    bool          `json:"passed"`
	Summary   string        `json:"summary"`
}

// buildReport constructs a Report from a slice of results.
func buildReport(id string, results []CheckResult) *Report {
	passed := true
	failures := []string{}
	for _, r := range results {
		if r.Status == StatusFail {
			passed = false
			failures = append(failures, r.Name)
		}
	}
	summary := "all checks passed"
	if !passed {
		summary = fmt.Sprintf("failed: %s", strings.Join(failures, ", "))
	}
	return &Report{
		DesktopID: id,
		Checks:    results,
		Passed:    passed,
		Summary:   summary,
	}
}

// Checker is a single health check.
type Checker interface {
	Name() string
	Run(ctx context.Context) CheckResult
}

// Runner executes a list of Checkers and aggregates results.
type Runner struct {
	desktopID string
	checkers  []Checker
}

// NewRunner returns a Runner.
func NewRunner(desktopID string, checkers ...Checker) *Runner {
	return &Runner{desktopID: desktopID, checkers: checkers}
}

// Run executes all checks and returns the aggregated report.
func (r *Runner) Run(ctx context.Context) *Report {
	results := make([]CheckResult, 0, len(r.checkers))
	for _, c := range r.checkers {
		results = append(results, c.Run(ctx))
	}
	return buildReport(r.desktopID, results)
}

// --- built-in checkers ---

// TCPChecker verifies that a TCP port is reachable.
type TCPChecker struct {
	name    string
	address string // host:port
	timeout time.Duration
}

// NewTCPChecker creates a TCPChecker.
func NewTCPChecker(name, address string, timeout time.Duration) *TCPChecker {
	return &TCPChecker{name: name, address: address, timeout: timeout}
}

func (c *TCPChecker) Name() string { return c.name }

func (c *TCPChecker) Run(ctx context.Context) CheckResult {
	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	conn.Close()
	return CheckResult{Name: c.name, Status: StatusPass}
}

// HTTPSChecker verifies that an HTTPS endpoint returns a 2xx status.
type HTTPSChecker struct {
	name    string
	url     string
	timeout time.Duration
}

// NewHTTPSChecker creates an HTTPSChecker.
func NewHTTPSChecker(name, url string, timeout time.Duration) *HTTPSChecker {
	return &HTTPSChecker{name: name, url: url, timeout: timeout}
}

func (c *HTTPSChecker) Name() string { return c.name }

func (c *HTTPSChecker) Run(ctx context.Context) CheckResult {
	client := &http.Client{
		Timeout: c.timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse // don't follow redirects; 3xx is a pass
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return CheckResult{Name: c.name, Status: StatusFail,
			Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return CheckResult{Name: c.name, Status: StatusPass}
}

// FnChecker wraps an arbitrary function as a Checker.
type FnChecker struct {
	name string
	fn   func(ctx context.Context) error
}

// NewFnChecker creates a Checker from a function.
func NewFnChecker(name string, fn func(ctx context.Context) error) *FnChecker {
	return &FnChecker{name: name, fn: fn}
}

func (c *FnChecker) Name() string { return c.name }

func (c *FnChecker) Run(ctx context.Context) CheckResult {
	if err := c.fn(ctx); err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	return CheckResult{Name: c.name, Status: StatusPass}
}

// SSHChecker runs a command on the desktop over SSH and checks the exit code.
// If keyPath is empty the check is skipped rather than failing.
type SSHChecker struct {
	name    string
	host    string
	port    int
	user    string
	keyPath string
	command string
	timeout time.Duration
}

// NewSSHChecker creates an SSHChecker.
func NewSSHChecker(name, host string, port int, user, keyPath, command string, timeout time.Duration) *SSHChecker {
	return &SSHChecker{
		name:    name,
		host:    host,
		port:    port,
		user:    user,
		keyPath: keyPath,
		command: command,
		timeout: timeout,
	}
}

func (c *SSHChecker) Name() string { return c.name }

func (c *SSHChecker) Run(ctx context.Context) CheckResult {
	if c.keyPath == "" {
		return CheckResult{Name: c.name, Status: StatusSkipped, Message: "no SSH key configured"}
	}
	user := c.user
	if user == "" {
		user = "ubuntu"
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", //nolint:gosec
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"-i", c.keyPath,
		"-p", fmt.Sprintf("%d", c.port),
		fmt.Sprintf("%s@%s", user, c.host),
		c.command,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return CheckResult{Name: c.name, Status: StatusFail, Message: msg}
	}
	return CheckResult{Name: c.name, Status: StatusPass}
}

// StandardCheckers returns the set of network-reachability checks run against a
// provisioned desktop. noVNC is probed on HTTPS port 8443 (novnc-desktop v0.1.5+).
func StandardCheckers(hostname string, sshPort int) []Checker {
	sshAddr := fmt.Sprintf("%s:%d", hostname, sshPort)
	noVNCURL := fmt.Sprintf("https://%s:8443/novnc", hostname)

	return []Checker{
		NewTCPChecker("ssh-port", sshAddr, 10*time.Second),
		NewHTTPSChecker("novnc-https", noVNCURL, 15*time.Second),
	}
}

// SSHCheckers returns SSH-based checks that run commands on the desktop to
// verify that Docker, developer tools, and any expected workspace repositories
// are present and active.
//
// If keyPath is empty all checks are returned in the skipped state so they
// appear in the doctor report without blocking the overall pass/fail result.
func SSHCheckers(hostname string, sshPort int, user, keyPath string, repos []string) []Checker {
	t := 20 * time.Second
	checkers := []Checker{
		NewSSHChecker("docker-active", hostname, sshPort, user, keyPath,
			"systemctl is-active docker", t),
		NewSSHChecker("nvim-installed", hostname, sshPort, user, keyPath,
			"command -v nvim >/dev/null 2>&1", t),
		NewSSHChecker("tmux-installed", hostname, sshPort, user, keyPath,
			"command -v tmux >/dev/null 2>&1", t),
	}
	for _, r := range repos {
		name := repoBaseName(r)
		checkers = append(checkers, NewSSHChecker(
			"repo-"+name,
			hostname, sshPort, user, keyPath,
			fmt.Sprintf("test -d /workspace/%s/.git", shellQuote(name)),
			t,
		))
	}
	return checkers
}

func repoBaseName(repoURL string) string {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	parts := strings.Split(repoURL, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return repoURL
}

// shellQuote wraps a string in single quotes for safe shell use, escaping embedded quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// SystemCheckers returns checks for system health (disk, memory, services, certificate).
func SystemCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	checkers := []Checker{
		NewSSHChecker("disk-space", hostname, sshPort, user, keyPath,
			"[ $(df /workspace | tail -1 | awk '{print $4}') -gt 1048576 ]", t), // >1GB free
		NewSSHChecker("memory-available", hostname, sshPort, user, keyPath,
			"[ $(free -m | grep Mem | awk '{print $7}') -gt 512 ]", t), // >512MB free
		NewSSHChecker("novnc-running", hostname, sshPort, user, keyPath,
			"systemctl is-active novnc-desktop", t),
		NewSSHChecker("certbot-cert-valid", hostname, sshPort, user, keyPath,
			`sudo bash -c 'for cert in /etc/letsencrypt/live/*/fullchain.pem; do [ -f "$cert" ] || continue; openssl x509 -in "$cert" -noout -checkend 604800 || exit 1; done'`, t), // 604800 = 7 days
		NewSSHChecker("certbot-timer-enabled", hostname, sshPort, user, keyPath,
			"systemctl is-enabled certbot.timer", t),
	}
	return checkers
}

// WorkspaceCheckers returns checks for workspace integrity.
func WorkspaceCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("workspace-mounted", hostname, sshPort, user, keyPath,
			"[ -d /workspace ] && [ -w /workspace ]", t),
	}
}
