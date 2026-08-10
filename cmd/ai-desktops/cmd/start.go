package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
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
	if err := requireTools("pulumi"); err != nil {
		return err
	}
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

	fmt.Fprintf(os.Stderr, "Starting instance %s ...\n", d.InstanceID)
	if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
		return err
	}

	mgr := desktop.NewManager(s)

	// After hibernation the instance gets a new public IP. Refresh the Pulumi
	// stack state from AWS then run pulumi up so the Route53 A record is
	// updated to point at the new IP before we mark the desktop ready.
	if err := requireBackend(ctx); err != nil {
		return err
	}
	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	ref := pulumi.DesktopStackRef(backendURL, id, workDir)
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}

	fmt.Fprintln(os.Stderr, "Updating DNS record for new public IP ...")
	outputs, err := runner.RefreshAndUp(ctx, ref, os.Stderr)
	if err != nil {
		_ = mgr.RecordFailure(ctx, id, "start", err.Error())
		return fmt.Errorf("pulumi refresh+up: %w (instance is running; DNS may be stale)", err)
	}

	// Persist updated outputs (new public IP reflected in hostname/SSH/noVNC)
	// back to the store so subsequent commands see current values.
	updateDesktopFromPulumiOutputs(d, outputs)
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("update store record: %w", err)
	}

	if err := mgr.MarkRunning(ctx, id, "started"); err != nil {
		return err
	}

	result := map[string]string{
		"desktop_id": d.DesktopID,
		"hostname":   d.Hostname,
		"novnc_url":  d.NoVNCURL,
		"ssh_target": d.SSHTarget,
		"ami_id":     d.AMIID,
		"region":     d.Region,
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID  : %s\n", result["desktop_id"])
	fmt.Printf("Hostname    : %s\n", result["hostname"])
	fmt.Printf("Desktop URL : %s\n", result["novnc_url"])
	fmt.Printf("SSH target  : %s\n", result["ssh_target"])
	fmt.Printf("AMI ID      : %s\n", result["ami_id"])
	fmt.Printf("Region      : %s\n", result["region"])
	return nil
}
