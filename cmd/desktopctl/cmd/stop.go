package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop <desktop-id>",
	Short: "Stop a running desktop (hibernates when supported, otherwise powers off)",
	Args:  cobra.ExactArgs(1),
	RunE:  runStop,
}

var stopRetry bool

func init() {
	stopCmd.Flags().BoolVar(&stopRetry, "retry", false, "resume an interrupted stop after confirming the previous process exited")
	rootCmd.AddCommand(stopCmd)
}

func runStop(cmd *cobra.Command, args []string) (retErr error) {
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
	if d.State == store.StateStopping && stopRetry {
		if err := requireStaleLifecycle(d, "stop", 30*time.Minute, time.Now()); err != nil {
			return err
		}
	} else if !(d.State == store.StateFailed && d.FailurePhase == "stop" && stopRetry) && d.State != store.StateReady && d.State != store.StateUnhealthy {
		return fmt.Errorf("desktop %q is %s; stop requires a running desktop", id, d.State)
	}

	if d.InstanceID == "" {
		return fmt.Errorf("desktop %q has no instance ID — it may not be fully provisioned", id)
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	d, token, err := claimLifecycleOwner(ctx, s, d, store.StateStopping)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = recordLifecycleFailure(ctx, s, id, token, "stop", retErr.Error())
		}
	}()

	status, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return err
	}
	if status.State == "stopping" {
		if err := ec2.NewInstanceStoppedWaiter(ec2.NewFromConfig(awsCfg)).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{d.InstanceID}}, 10*time.Minute); err != nil {
			return fmt.Errorf("wait for interrupted stop: %w", err)
		}
	} else if status.State == "running" && d.NestedVirt {
		fmt.Printf("Stopping instance %s (nested virtualization enabled; hibernation not supported) ...\n", d.InstanceID)
		if err := awsx.HaltInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
	} else if status.State == "running" {
		fmt.Printf("Hibernating instance %s ...\n", d.InstanceID)
		if err := awsx.StopInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
	} else if status.State != "stopped" {
		return fmt.Errorf("cannot stop desktop %s while instance is %s", id, status.State)
	}
	if err := completeLifecycle(ctx, s, id, token, store.StateStopping, store.StateStopped, "", store.StopReasonUserRequest); err != nil {
		return err
	}
	if d.NestedVirt {
		fmt.Printf("Desktop %s stopped.\n", id)
	} else {
		fmt.Printf("Desktop %s hibernated.\n", id)
	}
	return nil
}
