package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/orchael/ai-desktops/internal/health"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

const (
	// initWindow is the age below which a desktop is considered still initializing.
	initWindow = 15 * time.Minute
	// waitPollInterval is how often doctor re-runs checks when --wait is set.
	waitPollInterval = 15 * time.Second
	// waitDefaultTimeout is the maximum time --wait will poll before giving up.
	waitDefaultTimeout = 10 * time.Minute
)

// ANSI escape sequences for terminal control.
const (
	ansiReset    = "\033[0m"
	ansiGreen    = "\033[32m"
	ansiRed      = "\033[31m"
	ansiYellow   = "\033[33m"
	ansiDim      = "\033[2m"
	ansiClearEOL = "\033[K"
)

var doctorWait bool

var doctorCmd = &cobra.Command{
	Use:   "doctor <desktop-id>",
	Short: "Run health checks against a desktop",
	Long: `doctor runs a suite of health checks against the specified desktop:

Network checks (always run):
  - ssh-port      — SSH port reachable (TCP)

  noVNC Desktop:
    - novnc-https   — noVNC HTTPS endpoint responding on port 8443
    - novnc-running — novnc-desktop systemd service is active

SSH-based checks (require desktop.ssh_key_path in config; skipped otherwise):

  Desktop Web:
    - desktop-web-active  — ai-desktops-web systemd service is active
    - desktop-web-version — installed @markcallen/desktop-web package version

  Essential Services:
    - docker-active   — Docker daemon is active
    - nvim-installed  — nvim is on PATH
    - tmux-installed  — tmux is on PATH

  System Resources:
    - bootstrap-state  — cloud-init lifecycle state from the persistent desktop artifact
    - disk-space       — /workspace has >1GB free
    - memory-available — system has >512MB free memory

  Certificate & TLS:
    - certbot-cert-valid    — TLS certificate is valid (>7 days until expiry)
    - certbot-timer-enabled — certbot auto-renewal timer is enabled

  Swap & Monitoring:
    - swap-active              — /swapfile is active (skipped when swap not configured)
    - cloudwatch-agent-active  — optional CloudWatch agent state (inactive is a diagnostic warning)

  bridgectl Agent Server:
    - bridgectl-installed         — bridgectl CLI is on PATH
    - bridgectl-version           — bridgectl --version exits successfully
    - bridgectl-config-exists     — ~/.config/bridgectl/config.yaml is present
    - bridgectl-credentials-env   — ~/.config/bridgectl/agents.env exists
    - codex-home-private          — ~/.codex is a real, non-symlinked directory owned by ubuntu with mode 0700
    - bridgectl-claude-configured — claude provider defined in config.yaml
    - bridgectl-codex-configured  — codex provider defined in config.yaml
    - bridgectl-service-active    — bridgectl systemd user service is active (ubuntu)

  Workspace:
    - workspace-mounted    — /workspace is mounted and writable

  Secrets (only when --secret paths were specified at create time):
    - desktop-secrets-env-file  — /home/ubuntu/.config/environment.d/desktop-secrets.conf exists and is non-empty
    - desktop-secrets-bash-file — /home/ubuntu/.desktop-secrets exists and is non-empty

  Tailscale (only when --tailscale or --tailscale-network was specified at create time):
    - tailscale-installed         — tailscale CLI is on PATH
    - tailscaled-active           — tailscaled systemd service is active
    - tailscale-running           — Tailscale backend state is Running
    - tailscale-network-metadata  — requested network name was recorded on the desktop

  step-ca (only when --step-ca was specified at create time, or --tailscale used pki.step_ca_server):
    - step-cli-installed     — Smallstep CLI is on PATH
    - step-ca-resolves       — configured step-ca DNS name resolves
    - step-ca-health         — step ca health succeeds
    - bridgectl-step-ca-env  — bridgectl step-ca environment file exists
    - bridgectl-step-ca-cert — bridge TLS certificate and key were issued

  Nested Virtualization (only when --nested-virtualization or --mobile was used at create time):
    - kvm-device      — /dev/kvm character device exists
    - kvm-ok          — sudo kvm-ok reports KVM acceleration can be used
    - cpu-virt-flag   — vmx (Intel) or svm (AMD) flag is present in /proc/cpuinfo

  Android Virtual Devices (only when --avd was specified at create time):
    - avdmanager-installed — avdmanager binary is present at /opt/android-sdk/cmdline-tools/latest/bin/avdmanager
    - avd-<name>           — each named AVD is listed by avdmanager list avd (skipped when Android SDK absent)

  Repositories:
    - repo-<name>         — each configured repository is cloned under /workspace`,
	Args: cobra.ExactArgs(1),
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorWait, "wait", false,
		fmt.Sprintf("retry checks every %s until all pass or %s elapses", waitPollInterval, waitDefaultTimeout))
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	id := args[0]

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	createdAt, _ := time.Parse(time.RFC3339, d.CreatedAt)

	groups := []health.CheckGroup{
		{Label: "Network", Checkers: health.StandardCheckers(d.Hostname, 22)},
		{Label: "noVNC Desktop", Checkers: health.NoVNCCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath)},
		{Label: "Desktop Web", Checkers: health.DesktopWebCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath)},
		{Label: "Services", Checkers: health.SSHCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath)},
		{Label: "System", Checkers: health.SystemCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath)},
		{Label: "bridgectl Agent Server", Checkers: health.BridgectlCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath)},
		{Label: "Workspace", Checkers: health.WorkspaceCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.WorkspaceMode)},
		{Label: "Secrets", Checkers: health.SecretsCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.Secrets)},
		{Label: "Tailscale", Checkers: health.TailscaleCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.TailscaleNet, cfg.Agent.BridgePort)},
		{Label: "step-ca", Checkers: health.StepCACheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.StepCAServer)},
		{Label: "Nested Virtualization", Checkers: health.NestedVirtCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.NestedVirt)},
		{Label: "Android Virtual Devices", Checkers: health.AVDCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.AVDNames)},
		{Label: "Repositories", Checkers: health.RepoCheckers(d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.Repos)},
	}
	runner := health.NewRunnerGroups(id, groups...)

	if jsonOut {
		return runDoctorJSON(ctx, runner)
	}

	if doctorWait && isTTY() {
		return runDoctorTUI(ctx, id, runner, createdAt)
	}
	return runDoctorStatic(ctx, runner, createdAt)
}

func runDoctorJSON(ctx context.Context, runner *health.Runner) error {
	checkTimeout := 120 * time.Second
	runCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	report := runner.Run(runCtx)
	cancel()
	return json.NewEncoder(os.Stdout).Encode(report)
}

// isTTY returns true when stdout is an interactive terminal.
func isTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ---- static (non-TUI) path ----

func runDoctorStatic(ctx context.Context, runner *health.Runner, createdAt time.Time) error {
	deadline := time.Now().Add(waitDefaultTimeout)

	for {
		checkTimeout := 120 * time.Second
		runCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		report := runner.Run(runCtx)
		cancel()

		age := time.Since(createdAt).Round(time.Second)
		fmt.Printf("Desktop : %s\n", report.DesktopID)
		if !createdAt.IsZero() {
			fmt.Printf("Age     : %s\n", formatAge(age))
		}
		fmt.Printf("Summary : %s\n", report.Summary)
		fmt.Println()

		// Build a name→result lookup so we can walk by group.
		resultByName := make(map[string]health.CheckResult, len(report.Checks))
		for _, c := range report.Checks {
			resultByName[c.Name] = c
		}
		for _, g := range runner.Groups() {
			if len(g.Checkers) == 0 {
				continue
			}
			if g.Label != "" {
				fmt.Printf("  %s:\n", g.Label)
			}
			for _, ch := range g.Checkers {
				c := resultByName[ch.Name()]
				mark := "✓"
				if c.Status == health.StatusFail {
					mark = "✗"
				} else if c.Status == health.StatusWarning {
					mark = "!"
				} else if c.Status == health.StatusSkipped {
					mark = "–"
				}
				line := fmt.Sprintf("    %s %s", mark, c.Name)
				if c.Message != "" {
					line += " — " + c.Message
				}
				fmt.Println(line)
			}
			fmt.Println()
		}

		if !report.Passed && !createdAt.IsZero() && age < initWindow {
			fmt.Printf("\nHint: desktop was created %s ago — cloud-init may still be running.\n", formatAge(age))
			fmt.Printf("      Re-run doctor in a few minutes, or use --wait to poll automatically.\n")
		}

		if report.Passed || !doctorWait {
			if !report.Passed {
				return fmt.Errorf("health checks failed")
			}
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("health checks still failing after %s", waitDefaultTimeout)
		}

		remaining := time.Until(deadline).Round(time.Second)
		fmt.Printf("\nRetrying in %s (%s remaining)...\n\n", waitPollInterval, remaining)
		time.Sleep(waitPollInterval)
	}
}

// ---- live TUI path ----

// tuiLine records the printed row for a single checker (0 = first row printed).
type tuiLine struct {
	name string
	row  int // absolute row within the printed check block (0-based from top)
}

type doctorTUI struct {
	groups     []health.CheckGroup
	lines      []tuiLine // one entry per checker, in flat order
	mu         sync.Mutex
	totalLines int // total rows printed in the check block (including group headers)
}

func newDoctorTUI(groups []health.CheckGroup) *doctorTUI {
	return &doctorTUI{groups: groups}
}

// buildLines computes the row for every checker given the current group layout.
func buildLines(groups []health.CheckGroup) ([]tuiLine, int) {
	var lines []tuiLine
	row := 0
	for _, g := range groups {
		if len(g.Checkers) == 0 {
			continue
		}
		if g.Label != "" {
			row++ // group header line
		}
		for _, c := range g.Checkers {
			lines = append(lines, tuiLine{name: c.Name(), row: row})
			row++
		}
		row++ // blank line after group
	}
	return lines, row
}

// printHeader prints the desktop/age/summary block.
func (t *doctorTUI) printHeader(desktopID string, age time.Duration, showAge bool, summary string) {
	if showAge {
		fmt.Printf("Desktop : %s\n", desktopID)
		fmt.Printf("Age     : %s\n", formatAge(age))
		fmt.Printf("Summary : %s\n\n", summary)
	} else {
		fmt.Printf("Desktop : %s\n", desktopID)
		fmt.Printf("Summary : %s\n\n", summary)
	}
}

// printChecks prints all group headers and checks in "pending" state.
func (t *doctorTUI) printChecks() {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines, total := buildLines(t.groups)
	t.lines = lines
	t.totalLines = total
	for _, g := range t.groups {
		if len(g.Checkers) == 0 {
			continue
		}
		if g.Label != "" {
			fmt.Printf("  %s%s:%s\n", ansiDim, g.Label, ansiReset)
		}
		for _, c := range g.Checkers {
			fmt.Printf("    %s○ %s%s\n", ansiDim, c.Name(), ansiReset)
		}
		fmt.Println()
	}
}

// resetChecks rewinds to the first check row and redraws all checks as pending.
func (t *doctorTUI) resetChecks() {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Printf("\033[%dA", t.totalLines)
	for _, g := range t.groups {
		if len(g.Checkers) == 0 {
			continue
		}
		if g.Label != "" {
			fmt.Printf("\r  %s%s:%s%s\n", ansiDim, g.Label, ansiReset, ansiClearEOL)
		}
		for _, c := range g.Checkers {
			fmt.Printf("\r    %s○ %s%s%s\n", ansiDim, c.Name(), ansiReset, ansiClearEOL)
		}
		fmt.Printf("\r%s\n", ansiClearEOL)
	}
}

// updateLine rewrites the checker row identified by its flat index.
// Cursor must be sitting one line below the last printed check-block row.
func (t *doctorTUI) updateLine(idx int, icon, color, name, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if idx >= len(t.lines) {
		return
	}
	row := t.lines[idx].row
	linesUp := t.totalLines - row
	fmt.Printf("\033[%dA\r    %s%s %s%s", linesUp, color, icon, name, ansiReset)
	if msg != "" {
		fmt.Printf(" — %s", msg)
	}
	fmt.Printf("%s\n", ansiClearEOL)
	if linesUp > 1 {
		fmt.Printf("\033[%dB", linesUp-1)
	}
}

func (t *doctorTUI) onStart(name string, idx, total int) {
	t.updateLine(idx, "⠿", ansiYellow, name, "running…")
}

func (t *doctorTUI) onComplete(result health.CheckResult, idx, total int) {
	switch result.Status {
	case health.StatusPass:
		t.updateLine(idx, "✓", ansiGreen, result.Name, "")
	case health.StatusFail:
		t.updateLine(idx, "✗", ansiRed, result.Name, result.Message)
	case health.StatusWarning:
		t.updateLine(idx, "!", ansiYellow, result.Name, result.Message)
	default:
		t.updateLine(idx, "–", ansiDim, result.Name, result.Message)
	}
}

// appendSummary prints summary below the last check line.
func (t *doctorTUI) appendSummary(report *health.Report) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if report.Passed && len(report.Warnings) == 0 {
		fmt.Printf("\n%s✓ all checks passed%s\n", ansiGreen, ansiReset)
	} else if report.Passed {
		fmt.Printf("\n%s! %s%s\n", ansiYellow, report.Summary, ansiReset)
	} else {
		fmt.Printf("\n%s✗ %s%s\n", ansiRed, report.Summary, ansiReset)
	}
	t.totalLines += 2
}

// clearSummary moves the cursor up past the 2 summary lines and clears them,
// leaving cursor at the bottom of the check list again.
func (t *doctorTUI) clearSummary() {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Printf("\033[2A\r%s\n\r%s\n", ansiClearEOL, ansiClearEOL)
	fmt.Printf("\033[2A") // park cursor at the blank line position
	// move back down so we're below checks again
	fmt.Printf("\033[2B")
	t.totalLines -= 2
}

// printCountdown overwrites the current line with a countdown, then clears it.
func (t *doctorTUI) countdown(d time.Duration, remaining time.Duration) {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		left := time.Until(end).Round(time.Second)
		fmt.Printf("\r%sRetrying in %s (%s remaining)…%s%s",
			ansiDim, left, remaining.Round(time.Second), ansiReset, ansiClearEOL)
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("\r%s\r", ansiClearEOL)
}

func runDoctorTUI(ctx context.Context, desktopID string, runner *health.Runner, createdAt time.Time) error {
	tui := newDoctorTUI(runner.Groups())
	deadline := time.Now().Add(waitDefaultTimeout)
	// headerLines: Desktop + (Age) + Summary + blank
	headerLines := 3
	if !createdAt.IsZero() {
		headerLines = 4
	}
	first := true

	for {
		age := time.Since(createdAt).Round(time.Second)

		if first {
			tui.printHeader(desktopID, age, !createdAt.IsZero(), "running…")
			tui.printChecks()
			first = false
		} else {
			tui.resetChecks()
		}

		runner.OnStart = tui.onStart
		runner.OnComplete = tui.onComplete

		checkTimeout := 120 * time.Second
		runCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		report := runner.Run(runCtx)
		cancel()

		// Update the summary line in-place.
		age = time.Since(createdAt).Round(time.Second)
		summaryStr := report.Summary
		summaryColor := ansiRed
		if report.Passed {
			summaryColor = ansiGreen
			if len(report.Warnings) > 0 {
				summaryColor = ansiYellow
			}
		}
		// Move up: check block + blank-before-block + (Age line) + Summary line
		// Header layout (from top): Desktop\n [Age\n] Summary\n \n <checks>
		// cursor is at bottom of check block; summary is headerLines-1 lines above the block top
		upToSummary := tui.totalLines + headerLines - 1
		if !createdAt.IsZero() {
			upToSummary = tui.totalLines + headerLines - 2
		}
		fmt.Printf("\033[%dA\r%sSummary : %s%s%s\n", upToSummary, summaryColor, summaryStr, ansiReset, ansiClearEOL)
		// restore cursor to bottom of check block
		fmt.Printf("\033[%dB", upToSummary-1)

		tui.appendSummary(report)

		if report.Passed {
			fmt.Println()
			return nil
		}

		hintShown := false
		if !createdAt.IsZero() && age < initWindow {
			fmt.Printf("%sHint: desktop was created %s ago — cloud-init may still be running.%s\n",
				ansiDim, formatAge(age), ansiReset)
			tui.mu.Lock()
			tui.totalLines++
			tui.mu.Unlock()
			hintShown = true
		}

		if time.Now().After(deadline) {
			fmt.Println()
			return fmt.Errorf("health checks still failing after %s", waitDefaultTimeout)
		}

		remaining := time.Until(deadline)
		if hintShown {
			// clear hint line before clearing summary
			tui.mu.Lock()
			fmt.Printf("\033[1A\r%s\033[1B", ansiClearEOL)
			tui.totalLines--
			tui.mu.Unlock()
		}
		tui.clearSummary()
		tui.countdown(waitPollInterval, remaining)
	}
}

// formatAge returns a human-readable age string (e.g. "3m 12s", "1h 5m").
func formatAge(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
