package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/health"
	"github.com/orchael/ai-desktops/internal/store"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor <desktop-id>",
	Short: "Run health checks against a desktop",
	Long: `doctor runs a suite of health checks against the specified desktop:

Network checks (always run):
  - ssh-port      — SSH port reachable (TCP)
  - novnc-https   — noVNC HTTPS endpoint responding on port 8443

SSH-based checks (require desktop.ssh_key_path in config; skipped otherwise):

  Essential Services:
    - docker-active   — Docker daemon is active
    - nvim-installed  — nvim is on PATH
    - tmux-installed  — tmux is on PATH

  System Resources:
    - disk-space      — /workspace has >1GB free
    - memory-available — system has >512MB free memory
    - novnc-running   — novnc-desktop process is running

  Certificate & TLS:
    - certbot-cert-valid   — TLS certificate is valid (>7 days until expiry)
    - certbot-timer-enabled — certbot auto-renewal timer is enabled

  Workspace:
    - workspace-mounted    — /workspace is mounted and writable
    - repo-<name>         — each configured repository is cloned under /workspace`,
	Args: cobra.ExactArgs(1),
	RunE: runDoctor,
}

func init() {
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

	checkers := health.StandardCheckers(d.Hostname, 22)
	checkers = append(checkers, health.SSHCheckers(
		d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.Repos,
	)...)
	checkers = append(checkers, health.SystemCheckers(
		d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath,
	)...)
	checkers = append(checkers, health.WorkspaceCheckers(
		d.Hostname, 22, "ubuntu", cfg.Desktop.SSHKeyPath,
	)...)
	runner := health.NewRunner(id, checkers...)

	timeout := 120 * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	report := runner.Run(runCtx)

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(report)
	}

	fmt.Printf("Desktop : %s\n", report.DesktopID)
	fmt.Printf("Summary : %s\n", report.Summary)
	fmt.Println()
	for _, c := range report.Checks {
		mark := "✓"
		if c.Status == health.StatusFail {
			mark = "✗"
		} else if c.Status == health.StatusSkipped {
			mark = "–"
		}
		line := fmt.Sprintf("  %s %s", mark, c.Name)
		if c.Message != "" {
			line += " — " + c.Message
		}
		fmt.Println(line)
	}

	if !report.Passed {
		return fmt.Errorf("health checks failed")
	}
	return nil
}
