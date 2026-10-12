package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <desktop-id>",
	Short: "Stop a running desktop (hibernates when supported, otherwise powers off)",
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

	mgr := desktop.NewManager(s)

	if d.NestedVirt {
		fmt.Printf("Stopping instance %s (nested virtualization enabled; hibernation not supported) ...\n", d.InstanceID)
		if err := awsx.HaltInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
		if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
			return err
		}
		fmt.Printf("Desktop %s stopped.\n", id)
	} else {
		fmt.Printf("Hibernating instance %s ...\n", d.InstanceID)
		if err := awsx.StopInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
		if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
			return err
		}
		fmt.Printf("Desktop %s hibernated.\n", id)
	}
	return nil
}
