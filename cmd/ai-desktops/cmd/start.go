package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/store"
)

var startCmd = &cobra.Command{
	Use:   "start <desktop-id>",
	Short: "Start a stopped desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runStart,
}

func init() {
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
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

	if d.InstanceID == "" {
		return fmt.Errorf("desktop %q has no instance ID", id)
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	fmt.Printf("Starting instance %s ...\n", d.InstanceID)
	if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
		return err
	}

	mgr := desktop.NewManager(s)
	if err := mgr.MarkRunning(ctx, id, "started"); err != nil {
		return err
	}

	fmt.Printf("Desktop %s started.\n", id)
	fmt.Printf("noVNC URL  : %s\n", d.NoVNCURL)
	fmt.Printf("SSH target : %s\n", d.SSHTarget)
	return nil
}
