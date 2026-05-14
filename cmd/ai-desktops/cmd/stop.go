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

var stopCmd = &cobra.Command{
	Use:   "stop <desktop-id>",
	Short: "Stop a running desktop (preserves disk state)",
	Args:  cobra.ExactArgs(1),
	RunE:  runStop,
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf("desktop %q has no instance ID — it may not be fully provisioned", id)
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	fmt.Printf("Stopping instance %s ...\n", d.InstanceID)
	if err := awsx.StopInstance(ctx, awsCfg, d.InstanceID); err != nil {
		return err
	}

	mgr := desktop.NewManager(s)
	if err := mgr.MarkStopped(ctx, id); err != nil {
		return err
	}

	fmt.Printf("Desktop %s stopped.\n", id)
	return nil
}
