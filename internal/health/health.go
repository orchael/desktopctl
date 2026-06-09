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

// CheckGroup is a labelled collection of checkers shown as a section in output.
type CheckGroup struct {
	Label    string
	Checkers []Checker
}

// Runner executes a list of CheckGroups and aggregates results.
type Runner struct {
	desktopID  string
	groups     []CheckGroup
	OnStart    func(name string, idx, total int)
	OnComplete func(result CheckResult, idx, total int)
}

// NewRunner returns a Runner from a flat list of checkers under a single unlabelled group.
func NewRunner(desktopID string, checkers ...Checker) *Runner {
	return &Runner{
		desktopID: desktopID,
		groups:    []CheckGroup{{Checkers: checkers}},
	}
}

// NewRunnerGroups returns a Runner from an ordered list of named CheckGroups.
func NewRunnerGroups(desktopID string, groups ...CheckGroup) *Runner {
	return &Runner{desktopID: desktopID, groups: groups}
}

// Groups returns the check groups.
func (r *Runner) Groups() []CheckGroup {
	return r.groups
}

// Names returns the name of every checker across all groups in order.
func (r *Runner) Names() []string {
	var out []string
	for _, g := range r.groups {
		for _, c := range g.Checkers {
			out = append(out, c.Name())
		}
	}
	return out
}

// Run executes all checks and returns the aggregated report.
func (r *Runner) Run(ctx context.Context) *Report {
	var checkers []Checker
	for _, g := range r.groups {
		checkers = append(checkers, g.Checkers...)
	}
	results := make([]CheckResult, 0, len(checkers))
	total := len(checkers)
	for i, c := range checkers {
		if r.OnStart != nil {
			r.OnStart(c.Name(), i, total)
		}
		result := c.Run(ctx)
		results = append(results, result)
		if r.OnComplete != nil {
			r.OnComplete(result, i, total)
		}
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

// HTTPSChecker verifies that an HTTPS endpoint returns a non-4xx/5xx status.
// Redirects are not followed; 3xx responses are treated as a pass.
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
// provisioned desktop.
func StandardCheckers(hostname string, sshPort int) []Checker {
	sshAddr := fmt.Sprintf("%s:%d", hostname, sshPort)
	return []Checker{
		NewTCPChecker("ssh-port", sshAddr, 10*time.Second),
	}
}

// NoVNCCheckers returns checks for the novnc-desktop service.
func NoVNCCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	noVNCURL := fmt.Sprintf("https://%s:8443/novnc", hostname)
	return []Checker{
		NewHTTPSChecker("novnc-https", noVNCURL, 15*time.Second),
		NewSSHChecker("novnc-running", hostname, sshPort, user, keyPath,
			"systemctl is-active novnc-desktop", 20*time.Second),
	}
}

// SSHCheckers returns SSH-based checks that run commands on the desktop to
// verify that Docker, developer tools, and any expected workspace repositories
// are present and active.
//
// If keyPath is empty all checks are returned in the skipped state so they
// appear in the doctor report without blocking the overall pass/fail result.
func SSHCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("docker-active", hostname, sshPort, user, keyPath,
			"systemctl is-active docker", t),
		NewSSHChecker("nvim-installed", hostname, sshPort, user, keyPath,
			"command -v nvim >/dev/null 2>&1", t),
		NewSSHChecker("tmux-installed", hostname, sshPort, user, keyPath,
			"command -v tmux >/dev/null 2>&1", t),
		NewSSHChecker("gh-installed", hostname, sshPort, user, keyPath,
			"command -v gh >/dev/null 2>&1", t),
		NewSSHChecker("gh-auth", hostname, sshPort, user, keyPath,
			"sudo -u ubuntu gh auth status >/dev/null 2>&1", t),
		NewSSHChecker("python3-installed", hostname, sshPort, user, keyPath,
			"command -v python3 >/dev/null 2>&1", t),
		NewSSHChecker("git-identity", hostname, sshPort, user, keyPath,
			`test -n "$(sudo -u ubuntu git config --global user.name 2>/dev/null)" && `+
				`test -n "$(sudo -u ubuntu git config --global user.email 2>/dev/null)"`, t),
		NewSSHChecker("ssh-key-present", hostname, sshPort, user, keyPath,
			"test -f /home/ubuntu/.ssh/github_ed25519", t),
	}
}

// RepoCheckers returns one check per repository confirming it is cloned under /workspace.
func RepoCheckers(hostname string, sshPort int, user, keyPath string, repos []string) []Checker {
	t := 20 * time.Second
	checkers := make([]Checker, 0, len(repos))
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
		NewSSHChecker("certbot-cert-valid", hostname, sshPort, user, keyPath,
			`sudo bash -c 'found=0; for cert in /etc/letsencrypt/live/*/fullchain.pem; do [ -f "$cert" ] || continue; found=1; openssl x509 -in "$cert" -noout -checkend 604800 || exit 1; done; [ $found -eq 1 ] || exit 1'`, t), // 604800 = 7 days; fails if no certs exist
		NewSSHChecker("certbot-timer-enabled", hostname, sshPort, user, keyPath,
			"systemctl is-enabled certbot.timer", t),
	}
	return checkers
}

// BridgeCheckers returns checks for the ai-agent-bridge daemon and the ai-desktops profile.
//
// The checks mirror the on-desktop ai-desktops-doctor script so operators can
// verify bridge readiness from the CLI without manually SSHing into the desktop.
// Checks are grouped: package/service → port/config → drop-in → runtime → providers.
func BridgeCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		// Package and service state
		NewSSHChecker("bridge-package-installed", hostname, sshPort, user, keyPath,
			"dpkg -s ai-agent-bridge >/dev/null 2>&1", t),
		NewSSHChecker("bridge-service-active", hostname, sshPort, user, keyPath,
			"systemctl is-active ai-agent-bridge", t),
		// Port bound on loopback only (ProtectSystem=strict requires 127.0.0.1, not 0.0.0.0)
		NewSSHChecker("bridge-port-open", hostname, sshPort, user, keyPath,
			`ss -tlnp 2>/dev/null | grep -qE '(127\.0\.0\.1|::1):9445'`, t),
		// Configuration
		NewSSHChecker("bridge-config-exists", hostname, sshPort, user, keyPath,
			"test -f /etc/ai-agent-bridge/bridge.yaml", t),
		NewSSHChecker("bridge-workspace-allowed", hostname, sshPort, user, keyPath,
			"grep -q '/workspace' /etc/ai-agent-bridge/bridge.yaml", t),
		// systemd drop-in (written by the ai-desktops Packer playbook)
		NewSSHChecker("bridge-dropin-workspace", hostname, sshPort, user, keyPath,
			"test -f /etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf && "+
				"grep -q 'ReadWritePaths.*workspace' "+
				"/etc/systemd/system/ai-agent-bridge.service.d/ai-desktops.conf", t),
		// Provider runtime (Node.js v24 + install-provider-runtime output)
		NewSSHChecker("bridge-nodejs-version", hostname, sshPort, user, keyPath,
			`node --version 2>/dev/null | grep -q '^v24\.'`, t),
		NewSSHChecker("bridge-runtime-modules", hostname, sshPort, user, keyPath,
			"test -d /opt/ai-agent-bridge/node_modules", t),
		NewSSHChecker("bridge-claude-installed", hostname, sshPort, user, keyPath,
			"test -f /opt/ai-agent-bridge/node_modules/.bin/claude", t),
		// Providers and credentials
		NewSSHChecker("bridge-providers-configured", hostname, sshPort, user, keyPath,
			`awk '/^providers:/{p=1;next} p&&/^[^ \t]/{p=0} `+
				`p&&/^  [a-zA-Z][a-zA-Z0-9_]*:/&&!/^  #/{n++} END{exit(n>0)?0:1}' `+
				"/etc/ai-agent-bridge/bridge.yaml", t),
		NewSSHChecker("bridge-credentials-env", hostname, sshPort, user, keyPath,
			"test -f /etc/ai-agent-bridge/agents.env", t),
		// bridgectl CLI
		NewSSHChecker("bridge-bridgectl-installed", hostname, sshPort, user, keyPath,
			"command -v bridgectl >/dev/null 2>&1", t),
	}
}

// WorkspaceCheckers returns checks for workspace integrity.
func WorkspaceCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("workspace-mounted", hostname, sshPort, user, keyPath,
			"[ -d /workspace ] && [ -w /workspace ]", t),
	}
}
