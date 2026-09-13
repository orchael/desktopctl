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

// SSHOptionalChecker runs a prerequisite command first; if the remote host
// executes it and it exits non-zero the check is skipped (feature not
// configured). SSH transport failures (exit code 255: DNS, auth, connection
// refused) are treated as hard failures so they are not masked as "skipped".
type SSHOptionalChecker struct {
	name    string
	host    string
	port    int
	user    string
	keyPath string
	prereq  string // must exit 0 for the check to run
	command string
	skip    string // message returned when prereq exits non-zero on the remote
	timeout time.Duration
}

// NewSSHOptionalChecker creates a checker that skips when prereq exits non-zero
// on the remote host, but fails on SSH transport errors.
func NewSSHOptionalChecker(name, host string, port int, user, keyPath, prereq, skipMsg, command string, timeout time.Duration) *SSHOptionalChecker {
	return &SSHOptionalChecker{
		name: name, host: host, port: port, user: user, keyPath: keyPath,
		prereq: prereq, command: command, skip: skipMsg, timeout: timeout,
	}
}

func (c *SSHOptionalChecker) Name() string { return c.name }

func (c *SSHOptionalChecker) Run(ctx context.Context) CheckResult {
	if c.keyPath == "" {
		return CheckResult{Name: c.name, Status: StatusSkipped, Message: "no SSH key configured"}
	}
	user := c.user
	if user == "" {
		user = "ubuntu"
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	sshArgs := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"-i", c.keyPath,
		"-p", fmt.Sprintf("%d", c.port),
		fmt.Sprintf("%s@%s", user, c.host),
	}

	prereqCmd := exec.CommandContext(ctx, "ssh", append(sshArgs, c.prereq)...) //nolint:gosec
	if err := prereqCmd.Run(); err != nil {
		// Exit code 255 means SSH itself failed (transport/auth/DNS), not the
		// remote command. Treat that as a hard failure so connectivity issues
		// are not silently reported as "not configured".
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 255 {
			return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
		}
		msg := c.skip
		if msg == "" {
			msg = "not configured"
		}
		return CheckResult{Name: c.name, Status: StatusSkipped, Message: msg}
	}

	checkCmd := exec.CommandContext(ctx, "ssh", append(sshArgs, c.command)...) //nolint:gosec
	out, err := checkCmd.CombinedOutput()
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

// SystemCheckers returns checks for system health (disk, memory, services, certificate,
// swap file, and CloudWatch agent).
func SystemCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("disk-space", hostname, sshPort, user, keyPath,
			"[ $(df /workspace | tail -1 | awk '{print $4}') -gt 1048576 ]", t), // >1GB free
		NewSSHChecker("memory-available", hostname, sshPort, user, keyPath,
			"[ $(free -m | grep Mem | awk '{print $7}') -gt 512 ]", t), // >512MB free
		NewSSHChecker("certbot-cert-valid", hostname, sshPort, user, keyPath,
			`sudo bash -c 'found=0; for cert in /etc/letsencrypt/live/*/fullchain.pem; do [ -f "$cert" ] || continue; found=1; openssl x509 -in "$cert" -noout -checkend 604800 || exit 1; done; [ $found -eq 1 ] || exit 1'`, t), // 604800 = 7 days; fails if no certs exist
		NewSSHChecker("certbot-timer-enabled", hostname, sshPort, user, keyPath,
			"systemctl is-enabled certbot.timer", t),
		// Swap: skipped when no swapfile was configured at create time.
		NewSSHOptionalChecker("swap-active", hostname, sshPort, user, keyPath,
			"test -f /swapfile", "no swap configured",
			"swapon --show --noheadings | grep -q '^/swapfile'", t),
		// CloudWatch agent is always installed by cloud-init.
		NewSSHChecker("cloudwatch-agent-active", hostname, sshPort, user, keyPath,
			"systemctl is-active amazon-cloudwatch-agent", t),
	}
}

// BridgectlCheckers returns checks for the bridgectl user service running as ubuntu.
//
// bridgectl replaces the ai-agent-bridge system daemon: it runs as a systemd
// user service under the ubuntu account and starts in the /workspace/<repo>
// working directory. Checks verify the CLI is present, config and credentials
// are in place, and the user service is active.
func BridgectlCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	ubuntuUID := `$(id -u ubuntu)`
	return []Checker{
		// CLI availability and version
		NewSSHChecker("bridgectl-installed", hostname, sshPort, user, keyPath,
			"command -v bridgectl >/dev/null 2>&1", t),
		NewSSHChecker("bridgectl-version", hostname, sshPort, user, keyPath,
			"bridgectl --version >/dev/null 2>&1", t),
		// User-level config and credentials
		NewSSHChecker("bridgectl-config-exists", hostname, sshPort, user, keyPath,
			"test -f /home/ubuntu/.config/bridgectl/config.yaml", t),
		NewSSHChecker("bridgectl-credentials-env", hostname, sshPort, user, keyPath,
			"test -f /home/ubuntu/.config/bridgectl/agents.env", t),
		NewSSHChecker("codex-home-private", hostname, sshPort, user, keyPath,
			`test ! -L /home/ubuntu/.codex && test -d /home/ubuntu/.codex && test "$(stat -c '%U:%G:%a' /home/ubuntu/.codex)" = "ubuntu:ubuntu:700"`, t),
		// Provider configuration (claude and codex must be present)
		NewSSHChecker("bridgectl-claude-configured", hostname, sshPort, user, keyPath,
			"grep -q 'claude:' /home/ubuntu/.config/bridgectl/config.yaml", t),
		NewSSHChecker("bridgectl-codex-configured", hostname, sshPort, user, keyPath,
			"grep -q 'codex:' /home/ubuntu/.config/bridgectl/config.yaml", t),
		// DISPLAY env file written by ExecStartPre (bridgectl detected or defaulted to an X display)
		NewSSHChecker("bridgectl-display-env", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/bridgectl/display.env && grep -qE '^DISPLAY=:[0-9]+$' /home/ubuntu/.config/bridgectl/display.env", t),
		// Systemd user service status (checked as ubuntu via XDG_RUNTIME_DIR)
		NewSSHChecker("bridgectl-service-active", hostname, sshPort, user, keyPath,
			fmt.Sprintf(
				`sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/%s systemctl --user is-active bridgectl`,
				ubuntuUID,
			), t),
	}
}

// WorkspaceCheckers returns checks for workspace integrity.
func WorkspaceCheckers(hostname string, sshPort int, user, keyPath, workspaceMode string) []Checker {
	t := 20 * time.Second
	checkers := []Checker{
		NewSSHChecker("workspace-mounted", hostname, sshPort, user, keyPath,
			"[ -d /workspace ] && [ -w /workspace ]", t),
	}
	if workspaceMode == "efs" {
		checkers = append(checkers, NewSSHChecker("workspace-efs-mounted", hostname, sshPort, user, keyPath,
			"mountpoint -q /workspace && findmnt -n -o FSTYPE /workspace | grep -Eq '^(efs|nfs4?)$'", t))
	}
	return checkers
}

// DesktopWebCheckers returns checks for the desktop-web Express server.
func DesktopWebCheckers(hostname string, sshPort int, user, keyPath string) []Checker {
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("desktop-web-active", hostname, sshPort, user, keyPath,
			"systemctl is-active ai-desktops-web", t),
		NewSSHChecker("desktop-web-version", hostname, sshPort, user, keyPath,
			`node -e "process.stdout.write(require('/opt/ai-desktops/web/node_modules/@markcallen/desktop-web/package.json').version)"`, t),
	}
}

// SecretsCheckers returns checks that confirm desktop secrets were injected into
// the ubuntu environment at boot. Returns nil when no secret paths are configured
// so the group is omitted from doctor output entirely.
//
// When secrets are configured, checks verify that the systemd environment file
// (/home/ubuntu/.config/environment.d/desktop-secrets.conf) and the bash
// sourced file (/home/ubuntu/.desktop-secrets) were written and are non-empty,
// confirming cloud-init fetched at least one secret and wrote its key-value pairs.
func SecretsCheckers(hostname string, sshPort int, user, keyPath string, secretPaths []string) []Checker {
	if len(secretPaths) == 0 {
		return nil
	}
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("desktop-secrets-env-file", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/environment.d/desktop-secrets.conf", t),
		NewSSHChecker("desktop-secrets-bash-file", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.desktop-secrets", t),
	}
}

// TailscaleCheckers returns checks for desktops attached to a Tailscale network.
// Returns nil when no network was configured so doctor omits the group.
func TailscaleCheckers(hostname string, sshPort int, user, keyPath string, network string, bridgePort int) []Checker {
	if network == "" {
		return nil
	}
	if bridgePort == 0 {
		bridgePort = 9445
	}
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("tailscale-installed", hostname, sshPort, user, keyPath,
			"command -v tailscale >/dev/null 2>&1", t),
		NewSSHChecker("tailscaled-active", hostname, sshPort, user, keyPath,
			"systemctl is-active tailscaled", t),
		NewSSHChecker("tailscale-running", hostname, sshPort, user, keyPath,
			`tailscale status --json | python3 -c "import json,sys; exit(0 if json.load(sys.stdin).get('BackendState') == 'Running' else 1)"`, t),
		NewSSHChecker("tailscale-network-metadata", hostname, sshPort, user, keyPath,
			fmt.Sprintf("grep -qxF %s /opt/ai-desktops/tailscale.env", shellQuote(`TAILSCALE_NETWORK="`+network+`"`)), t),
		NewSSHOptionalChecker("bridgectl-tailscale-listener", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/bridgectl/step-ca.env", "step-ca not configured",
			fmt.Sprintf(`TAILSCALE_IP=$(tailscale ip -4 | head -n 1) && test -n "$TAILSCALE_IP" && (grep -qxF "  listen: \"${TAILSCALE_IP}:%[1]d\"" /home/ubuntu/.config/bridgectl/config.yaml || grep -qxF "  listen: ${TAILSCALE_IP}:%[1]d" /home/ubuntu/.config/bridgectl/config.yaml) && (ss -tln | awk '{print $4}' | grep -qx "${TAILSCALE_IP}:%[1]d" || ss -tln | awk '{print $4}' | grep -qx "[::ffff:${TAILSCALE_IP}]:%[1]d")`, bridgePort),
			t),
		NewSSHOptionalChecker("bridgectl-tailscale-san", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/bridgectl/step-ca.env", "step-ca not configured",
			`TAILSCALE_DNS=$(tailscale status --json | python3 -c "import json,sys; print(((json.load(sys.stdin).get('Self') or {}).get('DNSName') or '').rstrip('.'))") && test -n "$TAILSCALE_DNS" && python3 -c "import sys,yaml; cfg=yaml.safe_load(open('/home/ubuntu/.config/bridgectl/config.yaml')) or {}; san=(cfg.get('server') or {}).get('san') or []; sys.exit(0 if sys.argv[1] in san else 1)" "$TAILSCALE_DNS" && openssl x509 -in /home/ubuntu/.config/bridgectl/tls/server.crt -noout -ext subjectAltName | TAILSCALE_DNS="$TAILSCALE_DNS" python3 -c "import os,sys; want='DNS:' + os.environ['TAILSCALE_DNS']; entries=[part.strip() for line in sys.stdin for part in line.split(',')]; sys.exit(0 if want in entries else 1)"`,
			t),
	}
}

// StepCACheckers returns checks for desktops bootstrapped against a step-ca
// server. Returns nil when no step-ca server was configured.
func StepCACheckers(hostname string, sshPort int, user, keyPath string, serverDNS string) []Checker {
	if serverDNS == "" {
		return nil
	}
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("step-cli-installed", hostname, sshPort, user, keyPath,
			"command -v step >/dev/null 2>&1", t),
		NewSSHChecker("step-ca-resolves", hostname, sshPort, user, keyPath,
			fmt.Sprintf("getent hosts %s >/dev/null", shellQuote(serverDNS)), t),
		NewSSHChecker("step-ca-health", hostname, sshPort, user, keyPath,
			fmt.Sprintf("(sudo step ca health --ca-url %[1]s --root /root/.step/certs/root_ca.crt || sudo step ca health --ca-url %[1]s --root /etc/ssl/certs/ISRG_Root_X1.pem || sudo step ca health --ca-url %[1]s --root /etc/ssl/certs/ISRG_Root_X2.pem)", shellQuote("https://"+serverDNS)), t),
		NewSSHChecker("bridgectl-step-ca-env", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/bridgectl/step-ca.env", t),
		NewSSHChecker("bridgectl-step-ca-cert", hostname, sshPort, user, keyPath,
			"test -s /home/ubuntu/.config/bridgectl/tls/server.crt && test -s /home/ubuntu/.config/bridgectl/tls/server.key", t),
	}
}

// NestedVirtCheckers returns checks that verify KVM nested virtualization is
// functional on the desktop. Returns nil when nestedVirt is false so the group
// is omitted from doctor output entirely.
//
// Nested virtualization on AWS requires a supported Nitro instance type
// (c7i, c8i, m7i, m8i, r7i, r8i) launched with CpuOptions.NestedVirtualization=enabled.
// On Intel instances the VMX flag is exposed; on AMD instances the SVM flag is exposed.
func NestedVirtCheckers(hostname string, sshPort int, user, keyPath string, nestedVirt bool) []Checker {
	if !nestedVirt {
		return nil
	}
	t := 20 * time.Second
	return []Checker{
		NewSSHChecker("kvm-device", hostname, sshPort, user, keyPath,
			"test -c /dev/kvm", t),
		NewSSHChecker("kvm-ok", hostname, sshPort, user, keyPath,
			"sudo kvm-ok 2>&1 | grep -q 'KVM acceleration can be used'", t),
		NewSSHChecker("cpu-virt-flag", hostname, sshPort, user, keyPath,
			"grep -qE 'vmx|svm' /proc/cpuinfo", t),
	}
}

// avdmanagerPrereq is the prereq command used by AVD checkers: passes when avdmanager
// is present in the expected SDK location, fails when the AMI has no Android SDK.
const avdmanagerPrereq = "test -x /opt/android-sdk/cmdline-tools/latest/bin/avdmanager"

// AVDCheckers returns checks that verify the Android SDK and named AVDs are present
// on the desktop. Returns nil when no AVD names are configured so the group is omitted
// from doctor output entirely.
//
// All checks are optional: they are skipped (not failed) when avdmanager is absent,
// which indicates the desktop was provisioned without the Android SDK AMI.
func AVDCheckers(hostname string, sshPort int, user, keyPath string, avdNames []string) []Checker {
	if len(avdNames) == 0 {
		return nil
	}
	t := 30 * time.Second
	skipMsg := "Android SDK not installed in this AMI"
	checkers := make([]Checker, 0, 1+len(avdNames))
	checkers = append(checkers, NewSSHOptionalChecker(
		"avdmanager-installed", hostname, sshPort, user, keyPath,
		avdmanagerPrereq, skipMsg,
		"test -x /opt/android-sdk/cmdline-tools/latest/bin/avdmanager", t,
	))
	for _, name := range avdNames {
		checkers = append(checkers, NewSSHOptionalChecker(
			"avd-"+name, hostname, sshPort, user, keyPath,
			avdmanagerPrereq, skipMsg,
			fmt.Sprintf(
				`sudo -u ubuntu env HOME=/home/ubuntu ANDROID_HOME=/opt/android-sdk /opt/android-sdk/cmdline-tools/latest/bin/avdmanager list avd | grep -qF %s`,
				shellQuote("Name: "+name),
			),
			t,
		))
	}
	return checkers
}
