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
  - EC2 instance running (via AWS API)
  - SSH port reachable
  - noVNC HTTPS endpoint responding
  - Docker service active (via SSH)
  - ai-agent-bridge service active (via SSH)
  - Required tools on PATH (git, docker, nvim, tmux)
  - Workspace repositories present`,
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

	checkers := health.StandardCheckers(d.Hostname, 22, cfg.Agent.BridgePort)
	runner := health.NewRunner(id, checkers...)

	timeout := 60 * time.Second
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
		if c.Status != health.StatusPass {
			mark = "✗"
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
