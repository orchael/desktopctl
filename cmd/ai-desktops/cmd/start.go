package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/store"
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

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	instanceStatus, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return fmt.Errorf("instance status: %w", err)
	}
	if instanceStatus.State == "running" {
		fmt.Fprintf(os.Stderr, "Instance %s is already running; refreshing DNS ...\n", d.InstanceID)
	} else {
		fmt.Fprintf(os.Stderr, "Starting instance %s ...\n", d.InstanceID)
		if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
	}

	mgr := desktop.NewManager(s)

	if d.AMIID == "" {
		if instanceStatus.ImageID == "" {
			err := fmt.Errorf("resolve AMI for started instance: EC2 returned no image ID for %s", d.InstanceID)
			_ = mgr.RecordFailure(ctx, id, "start", err.Error())
			return err
		}
		d.AMIID = instanceStatus.ImageID
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("update store record with instance AMI: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Backfilled AMI ID from existing instance: %s\n", d.AMIID)
	}

	zone, err := cfg.DNSZone()
	if err != nil {
		_ = mgr.RecordFailure(ctx, id, "start", err.Error())
		return err
	}
	if d.Hostname == "" {
		d.Hostname = fmt.Sprintf("%s.%s", id, zone)
	}
	if instanceStatus.PublicIP == "" {
		fmt.Fprintln(os.Stderr, "Waiting for public IP assignment ...")
		publicIP, waitErr := awsx.WaitInstancePublicIP(ctx, awsCfg, d.InstanceID, 2*time.Minute)
		if waitErr != nil {
			_ = mgr.RecordFailure(ctx, id, "start", waitErr.Error())
			return waitErr
		}
		instanceStatus.PublicIP = publicIP
	}

	fmt.Fprintf(os.Stderr, "Updating DNS record %s -> %s ...\n", d.Hostname, instanceStatus.PublicIP)
	if err := awsx.UpsertARecord(ctx, awsCfg, zone, d.Hostname, instanceStatus.PublicIP); err != nil {
		_ = mgr.RecordFailure(ctx, id, "start", err.Error())
		return err
	}
	d.NoVNCURL = fmt.Sprintf("https://%s:8443/novnc/vnc.html", d.Hostname)
	d.SSHTarget = fmt.Sprintf("ubuntu@%s", d.Hostname)
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
		"region":     region,
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
