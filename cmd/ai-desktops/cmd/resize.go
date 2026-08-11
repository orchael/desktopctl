package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/awsx"
	configpkg "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var (
	resizeInstanceType string
	resizeStart        bool
	resizeNoStart      bool
)

var resizeCmd = &cobra.Command{
	Use:   "resize <desktop-id> --instance-type <type>",
	Short: "Change an existing on-demand desktop's EC2 instance type",
	Long: `resize changes the EC2 instance type for an existing on-demand desktop.

The command preserves the root EBS volume and desktop fleet identity, but it
must power-stop the instance before resizing. RAM hibernation state is not
preserved during resize.

Running desktops are restarted by default after resize. Stopped desktops remain
stopped unless --start is set.`,
	Args: cobra.ExactArgs(1),
	RunE: runResize,
}

func init() {
	resizeCmd.Flags().StringVar(&resizeInstanceType, "instance-type", "", "target EC2 instance type (for example m8i.xlarge, c7i.xlarge, m7i.large)")
	resizeCmd.Flags().BoolVar(&resizeStart, "start", false, "start the desktop after resizing when it was stopped")
	resizeCmd.Flags().BoolVar(&resizeNoStart, "no-start", false, "leave the desktop stopped after resizing even if it was running")
	rootCmd.AddCommand(resizeCmd)
}

func runResize(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	if resizeStart && resizeNoStart {
		return fmt.Errorf("--start and --no-start cannot be used together")
	}
	ctx := context.Background()
	id := args[0]
	targetType := strings.TrimSpace(resizeInstanceType)

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
	if err := validateResizeTarget(d, targetType); err != nil {
		return err
	}
	if d.InstanceID == "" {
		return fmt.Errorf("desktop %q has no instance ID", id)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}
	status, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return fmt.Errorf("check instance state before resize: %w", err)
	}
	if status.InstanceLifecycle == store.MarketSpot {
		return fmt.Errorf("resize is not yet supported for Spot desktops")
	}
	state := status.State
	if !canResizeFromInstanceState(state) {
		return fmt.Errorf("cannot resize desktop %q while instance %s is %q; wait until it is running or stopped", id, d.InstanceID, state)
	}

	if err := requireBackend(ctx); err != nil {
		return err
	}
	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	ref := pulumi.DesktopStackRef(backendURL, id, workDir)
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}
	mgr := desktop.NewManager(s)

	fmt.Fprintln(os.Stderr, "Updating Pulumi stack config ...")
	if err := runner.SetConfig(ctx, ref, "instanceType", targetType, os.Stderr); err != nil {
		_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
		return err
	}

	wasRunning := state == "running"
	shouldStart := (wasRunning && !resizeNoStart) || (!wasRunning && resizeStart)

	if wasRunning {
		fmt.Fprintf(os.Stderr, "Stopping instance %s for resize ...\n", d.InstanceID)
		if err := awsx.HaltInstance(ctx, awsCfg, d.InstanceID); err != nil {
			rollbackResizeConfig(ctx, runner, ref, d.InstanceType)
			_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
			return err
		}
		if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
			return err
		}
	} else if !d.NestedVirt {
		fmt.Fprintf(os.Stderr, "Normalizing stopped instance %s before resize ...\n", d.InstanceID)
		if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
			rollbackResizeConfig(ctx, runner, ref, d.InstanceType)
			_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
			return fmt.Errorf("start hibernated instance before resize: %w", err)
		}
		if err := awsx.HaltInstance(ctx, awsCfg, d.InstanceID); err != nil {
			rollbackResizeConfig(ctx, runner, ref, d.InstanceType)
			_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
			return err
		}
		if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "Changing instance %s type from %s to %s ...\n", d.InstanceID, d.InstanceType, targetType)
	if err := awsx.ModifyInstanceType(ctx, awsCfg, d.InstanceID, targetType); err != nil {
		rollbackResizeConfig(ctx, runner, ref, d.InstanceType)
		_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
		return err
	}

	finalState := store.StateStopped
	d.InstanceType = targetType
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("update store record: %w", err)
	}

	if shouldStart {
		fmt.Fprintf(os.Stderr, "Starting resized instance %s ...\n", d.InstanceID)
		if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
			_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
			return err
		}
		fmt.Fprintln(os.Stderr, "Refreshing DNS record for resized desktop ...")
		outputs, err := runner.RefreshAndUp(ctx, ref, os.Stderr)
		if err != nil {
			_ = mgr.RecordFailure(ctx, id, "resize", err.Error())
			return fmt.Errorf("pulumi refresh+up: %w (instance is running; DNS may be stale)", err)
		}
		updateDesktopFromPulumiOutputs(d, outputs)
		d.InstanceType = targetType
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("update store record: %w", err)
		}
		if err := mgr.MarkRunning(ctx, id, "resized"); err != nil {
			return err
		}
		finalState = store.StateReady
	} else if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
		return err
	}

	result := map[string]string{
		"desktop_id":      d.DesktopID,
		"instance_id":     d.InstanceID,
		"instance_type":   targetType,
		"lifecycle_state": string(finalState),
		"region":          region,
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID    : %s\n", result["desktop_id"])
	fmt.Printf("Instance ID   : %s\n", result["instance_id"])
	fmt.Printf("Instance type : %s\n", result["instance_type"])
	fmt.Printf("State         : %s\n", result["lifecycle_state"])
	fmt.Printf("Region        : %s\n", result["region"])
	return nil
}

func validateResizeTarget(d *store.Desktop, targetType string) error {
	if targetType == "" {
		return fmt.Errorf("--instance-type is required")
	}
	if d == nil {
		return fmt.Errorf("desktop is required")
	}
	if d.State == store.StateTerminated || d.State == store.StateTerminating {
		return fmt.Errorf("cannot resize desktop %q in state %q", d.DesktopID, d.State)
	}
	if d.MarketType == store.MarketSpot {
		return fmt.Errorf("resize is not yet supported for Spot desktops")
	}
	if d.InstanceType == targetType {
		return fmt.Errorf("desktop %q already uses instance type %s", d.DesktopID, targetType)
	}
	if d.NestedVirt && !configpkg.SupportsNestedVirt(targetType) {
		return fmt.Errorf("nested virtualization requires a supported Intel Nitro instance type (c8i, m8i, r8i, c7i, m7i, r7i, i7i); got %q", targetType)
	}
	return nil
}

func canResizeFromInstanceState(state string) bool {
	return state == "running" || state == "stopped"
}

func rollbackResizeConfig(ctx context.Context, runner *pulumi.Runner, ref *pulumi.StackRef, instanceType string) {
	if instanceType == "" {
		return
	}
	if err := runner.SetConfig(ctx, ref, "instanceType", instanceType, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to restore Pulumi instanceType config to %s: %v\n", instanceType, err)
	}
}
