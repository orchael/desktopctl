package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <desktop-id>",
	Short: "Start a stopped desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runStart,
}

var startRetry bool

func init() {
	startCmd.Flags().BoolVar(&startRetry, "retry", false, "resume an interrupted start after confirming the previous process exited")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) (retErr error) {
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
	if d.State == store.StateParked {
		return fmt.Errorf("desktop %q is parked; run `desktopctl unpark %s` to provision a new instance", id, id)
	}
	if d.State == store.StateStarting && startRetry {
		if err := requireStaleLifecycle(d, "start", 30*time.Minute, time.Now()); err != nil {
			return err
		}
	} else if d.State == store.StateResizing && startRetry {
		if err := requireStaleLifecycle(d, "resize", 2*time.Hour, time.Now()); err != nil {
			return err
		}
	} else if !(d.State == store.StateFailed && (d.FailurePhase == "start" || d.FailurePhase == "resize") && startRetry) && d.State != store.StateStopped && d.State != store.StateReady {
		return fmt.Errorf("desktop %q is %s; start requires stopped or ready", id, d.State)
	}

	if d.InstanceID == "" {
		return fmt.Errorf("desktop %q has no instance ID", id)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	if !d.NoDNS {
		zone, err := cfg.DNSZone()
		if err != nil {
			return err
		}
		if d.Hostname != "" && !strings.HasSuffix(d.Hostname, "."+zone) {
			return fmt.Errorf("desktop hostname %q does not belong to configured DNS zone %q", d.Hostname, zone)
		}
		if err := requireDesktopDNS(ctx, region); err != nil {
			return err
		}
	}
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	instanceStatus, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return fmt.Errorf("instance status: %w", err)
	}
	recoverResize := (d.State == store.StateFailed && (d.FailurePhase == "resize" || d.ResizeTargetType != "")) || d.State == store.StateResizing
	if recoverResize {
		if err := validateResizeRecovery(d, instanceStatus); err != nil {
			return err
		}
		if err := requireTools("pulumi"); err != nil {
			return err
		}
		if err := requireBackend(ctx); err != nil {
			return err
		}
	}
	d, token, err := claimLifecycleOwner(ctx, s, d, store.StateStarting)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			_ = recordLifecycleFailure(ctx, s, id, token, "start", retErr.Error())
		}
	}()
	if recoverResize {
		runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: region}
		if err := runner.SetConfig(ctx, desktopStackRef(d), "instanceType", instanceStatus.InstanceType, os.Stderr); err != nil {
			return fmt.Errorf("reconcile resized desktop stack config: %w", err)
		}
		d.InstanceType = instanceStatus.InstanceType
		if d.RestoreSpec != nil && d.RestoreSpec.Config != nil {
			d.RestoreSpec.Config["instanceType"] = d.InstanceType
		}
		d.ResizeTargetType = ""
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("reconcile resize fleet metadata: %w", err)
		}
	}
	if instanceStatus.State == "pending" {
		if err := ec2.NewInstanceRunningWaiter(ec2.NewFromConfig(awsCfg)).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{d.InstanceID}}, 10*time.Minute); err != nil {
			return fmt.Errorf("wait for interrupted start: %w", err)
		}
		instanceStatus, err = awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
		if err != nil {
			return err
		}
	}
	if instanceStatus.State == "running" {
		if d.NoDNS {
			fmt.Fprintf(os.Stderr, "Instance %s is already running ...\n", d.InstanceID)
		} else {
			fmt.Fprintf(os.Stderr, "Instance %s is already running; refreshing DNS ...\n", d.InstanceID)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Starting instance %s ...\n", d.InstanceID)
		if err := awsx.StartInstance(ctx, awsCfg, d.InstanceID); err != nil {
			return err
		}
	}

	if d.AMIID == "" {
		if instanceStatus.ImageID == "" {
			err := fmt.Errorf("resolve AMI for started instance: EC2 returned no image ID for %s", d.InstanceID)
			_ = recordLifecycleFailure(ctx, s, id, token, "start", err.Error())
			return err
		}
		d.AMIID = instanceStatus.ImageID
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("update store record with instance AMI: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Backfilled AMI ID from existing instance: %s\n", d.AMIID)
	}
	if d.NoDNS && (d.Hostname != id || d.NoVNCURL != "" || d.SSHTarget != "") {
		d.Hostname = id
		d.NoVNCURL = ""
		d.SSHTarget = ""
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("update no-DNS desktop record: %w", err)
		}
	}

	if !d.NoDNS {
		zone, err := cfg.DNSZone()
		if err != nil {
			_ = recordLifecycleFailure(ctx, s, id, token, "start", err.Error())
			return err
		}
		if d.Hostname == "" {
			d.Hostname = fmt.Sprintf("%s.%s", id, zone)
		}
		if instanceStatus.PublicIP == "" {
			fmt.Fprintln(os.Stderr, "Waiting for public IP assignment ...")
			publicIP, waitErr := awsx.WaitInstancePublicIP(ctx, awsCfg, d.InstanceID, 2*time.Minute)
			if waitErr != nil {
				_ = recordLifecycleFailure(ctx, s, id, token, "start", waitErr.Error())
				return waitErr
			}
			instanceStatus.PublicIP = publicIP
		}

		fmt.Fprintf(os.Stderr, "Updating DNS record %s -> %s ...\n", d.Hostname, instanceStatus.PublicIP)
		if err := awsx.UpsertARecord(ctx, awsCfg, zone, cfg.DNS.HostedZoneID, d.Hostname, instanceStatus.PublicIP); err != nil {
			_ = recordLifecycleFailure(ctx, s, id, token, "start", err.Error())
			return err
		}
		d.NoVNCURL = fmt.Sprintf("https://%s:8443/novnc/vnc.html", d.Hostname)
		d.SSHTarget = fmt.Sprintf("ubuntu@%s", d.Hostname)
		if err := s.Update(ctx, d); err != nil {
			return fmt.Errorf("update store record: %w", err)
		}
	}

	if recoverResize {
		runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: region}
		if _, err := runner.RefreshAndUp(ctx, desktopStackRef(d), os.Stderr); err != nil {
			return fmt.Errorf("reconcile resized desktop stack: %w", err)
		}
	}
	if err := completeLifecycle(ctx, s, id, token, store.StateStarting, store.StateReady, "started", ""); err != nil {
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

func validateResizeRecovery(d *store.Desktop, status *awsx.EC2InstanceStatus) error {
	if status == nil || (status.State != "running" && status.State != "stopped") {
		return fmt.Errorf("failed resize recovery requires a running or stopped instance")
	}
	if d.InstanceType == "" || (status.InstanceType != d.InstanceType && status.InstanceType != d.ResizeTargetType) {
		return fmt.Errorf("EC2 instance type %q differs from fleet record %q and intended resize %q; inspect failed resize before retry", status.InstanceType, d.InstanceType, d.ResizeTargetType)
	}
	if d.RestoreSpec != nil && d.RestoreSpec.Config["instanceType"] != d.InstanceType && d.RestoreSpec.Config["instanceType"] != d.ResizeTargetType {
		return fmt.Errorf("saved restore instance type differs from fleet record; inspect failed resize before retry")
	}
	return nil
}
