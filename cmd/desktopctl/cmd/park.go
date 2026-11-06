package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/smithy-go"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/health"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var parkDryRun, parkForce, unparkRetry bool

var (
	parkCmd   = &cobra.Command{Use: "park <desktop-id>", Short: "Release an EFS desktop's instance and root disk while retaining its workspace", Args: cobra.ExactArgs(1), RunE: runPark}
	unparkCmd = &cobra.Command{Use: "unpark <desktop-id>", Short: "Restore a parked EFS desktop from its saved launch configuration", Args: cobra.ExactArgs(1), RunE: runUnpark}
)

func init() {
	parkCmd.Flags().BoolVar(&parkDryRun, "dry-run", false, "show the instance, root volume, workspace, and data loss without writes")
	parkCmd.Flags().BoolVar(&parkForce, "force", false, "confirm loss of RAM and machine-local data")
	unparkCmd.Flags().BoolVar(&unparkRetry, "retry", false, "reconcile an interrupted restoring state after confirming no operation is active")
	rootCmd.AddCommand(parkCmd, unparkCmd)
}

type rootVolume struct {
	ID   string
	Size int32
}

func instanceRootVolume(ctx context.Context, client *ec2.Client, instanceID string) (rootVolume, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return rootVolume{}, err
	}
	if len(out.Reservations) != 1 || len(out.Reservations[0].Instances) != 1 {
		return rootVolume{}, fmt.Errorf("instance %s not found", instanceID)
	}
	inst := out.Reservations[0].Instances[0]
	for _, mapping := range inst.BlockDeviceMappings {
		if aws.ToString(mapping.DeviceName) == aws.ToString(inst.RootDeviceName) && mapping.Ebs != nil {
			id := aws.ToString(mapping.Ebs.VolumeId)
			volumes, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{id}})
			if err != nil {
				return rootVolume{}, err
			}
			if len(volumes.Volumes) != 1 {
				return rootVolume{}, fmt.Errorf("root volume %s not found", id)
			}
			return rootVolume{ID: id, Size: aws.ToInt32(volumes.Volumes[0].Size)}, nil
		}
	}
	return rootVolume{}, fmt.Errorf("instance %s has no identifiable root EBS volume", instanceID)
}

func volumeGone(ctx context.Context, client *ec2.Client, id string) (bool, error) {
	_, err := client.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{id}})
	if err == nil {
		return false, nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidVolume.NotFound" {
		return true, nil
	}
	return false, err
}

func instanceGone(ctx context.Context, client *ec2.Client, id string) (bool, error) {
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidInstanceID.NotFound" {
			return true, nil
		}
		return false, err
	}
	if len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
		return true, nil
	}
	state := out.Reservations[0].Instances[0].State
	return state != nil && string(state.Name) == "terminated", nil
}

func verifyWorkspaceAccessPoint(ctx context.Context, awsCfg aws.Config, w *store.Workspace) error {
	accessPoints, err := efs.NewFromConfig(awsCfg).DescribeAccessPoints(ctx, &efs.DescribeAccessPointsInput{AccessPointId: aws.String(w.EFSAccessPointID)})
	if err != nil {
		return fmt.Errorf("inspect EFS access point %s: %w", w.EFSAccessPointID, err)
	}
	if len(accessPoints.AccessPoints) != 1 || aws.ToString(accessPoints.AccessPoints[0].FileSystemId) != w.EFSFileSystemID || string(accessPoints.AccessPoints[0].LifeCycleState) != "available" {
		return fmt.Errorf("EFS workspace %s access point is missing, unavailable, or changed", w.WorkspaceName)
	}
	return nil
}

func parkEligible(d *store.Desktop, w *store.Workspace) error {
	if d.WorkspaceMode != workspaceModeEFS || d.WorkspaceName == "" {
		return fmt.Errorf("desktop %s uses a local workspace; park requires a retained EFS workspace", d.DesktopID)
	}
	if d.NestedVirt || len(d.AVDNames) > 0 {
		return fmt.Errorf("desktop %s has nested virtualization or AVD guests with machine-local state; terminate and recreate it manually after saving that state", d.DesktopID)
	}
	if w == nil || w.State == store.WorkspaceStateDeleted || w.EFSFileSystemID == "" || w.EFSAccessPointID == "" {
		return fmt.Errorf("EFS workspace %q is missing or incomplete; restore it before parking", d.WorkspaceName)
	}
	if w.AttachedDesktopID != d.DesktopID || w.State != store.WorkspaceStateAttached {
		return fmt.Errorf("EFS workspace %q is not exclusively claimed by desktop %s", d.WorkspaceName, d.DesktopID)
	}
	return nil
}

func desktopStackRef(d *store.Desktop) *pulumi.StackRef {
	return pulumi.DesktopStackRef("s3://"+cfg.Pulumi.BackendBucket, d.DesktopID, filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop"))
}

func runPark(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	id := args[0]
	if err := desktop.ValidateID(id); err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.WorkspaceMode != workspaceModeEFS || d.WorkspaceName == "" {
		return fmt.Errorf("desktop %s uses a local workspace; park requires a retained EFS workspace", id)
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("fleet store does not support EFS workspaces")
	}
	w, err := ws.GetWorkspace(ctx, d.Environment, d.WorkspaceName)
	if err != nil {
		return fmt.Errorf("read workspace: %w", err)
	}
	if err := parkEligible(d, w); err != nil {
		return err
	}
	if d.State != store.StateReady && d.State != store.StateStopped && !(d.State == store.StateFailed && d.FailurePhase == "park") && !(d.State == store.StateParking && parkForce) {
		return fmt.Errorf("desktop %s is %s; park requires ready, stopped, or a prior failed park", id, d.State)
	}
	if d.InstanceID == "" {
		return fmt.Errorf("desktop %s has no instance to park", id)
	}
	awsCfg, err := awsx.LoadConfig(ctx, d.Region, cfg.AWS.Profile)
	if err != nil {
		return err
	}
	if err := verifyWorkspaceAccessPoint(ctx, awsCfg, w); err != nil {
		return err
	}
	ec2Client := ec2.NewFromConfig(awsCfg)
	volume, err := instanceRootVolume(ctx, ec2Client, d.InstanceID)
	if err != nil && d.State != store.StateParking && !(d.State == store.StateFailed && d.FailurePhase == "park") {
		return fmt.Errorf("inspect root volume: %w", err)
	}
	if volume.ID == "" {
		volume.ID = d.ParkRootVolumeID
	}
	if volume.ID == "" {
		return fmt.Errorf("cannot identify the root volume for deletion confirmation")
	}
	if d.ParkRootVolumeID != "" && volume.ID != d.ParkRootVolumeID {
		return fmt.Errorf("root volume changed from %s to %s during park; refusing to reconcile", d.ParkRootVolumeID, volume.ID)
	}
	fmt.Printf("Desktop      : %s\nInstance     : %s\nRoot volume  : %s (%d GiB)\nEFS workspace: %s (%s / %s)\n", id, d.InstanceID, volume.ID, volume.Size, w.WorkspaceName, w.EFSFileSystemID, w.EFSAccessPointID)
	fmt.Println("Lost: RAM, home-directory files, installed packages, and every change outside /workspace. Restoring provisions a new instance and takes longer than start.")
	if parkDryRun {
		return nil
	}
	if !parkForce {
		fmt.Fprintf(os.Stderr, "Type %s to park: ", id)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != id {
			return fmt.Errorf("park not confirmed")
		}
	}
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	if err := requireBackend(ctx); err != nil {
		return err
	}
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: d.Region}
	spec := d.RestoreSpec
	if d.RestoreSpec == nil {
		config, err := runner.Config(ctx, desktopStackRef(d))
		if err != nil {
			return fmt.Errorf("capture restore spec: %w", err)
		}
		spec = &store.RestoreSpec{Version: 1, Config: config}
	}
	if err := validateRestoreSpec(d, spec, w); err != nil {
		return err
	}
	if volume.Size > 0 {
		spec.Config["volumeSize"] = fmt.Sprintf("%d", volume.Size)
	}
	if err := completePark(ctx, s, d, spec, volume.ID, func() error { return runner.Destroy(ctx, desktopStackRef(d), os.Stderr) }, func() error {
		waitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		for {
			gone, err := volumeGone(waitCtx, ec2Client, volume.ID)
			if err != nil {
				return err
			}
			instanceDeleted, err := instanceGone(waitCtx, ec2Client, d.InstanceID)
			if err != nil {
				return err
			}
			if gone && instanceDeleted {
				return nil
			}
			select {
			case <-waitCtx.Done():
				return fmt.Errorf("instance %s or root volume %s deletion not confirmed", d.InstanceID, volume.ID)
			case <-time.After(5 * time.Second):
			}
		}
	}); err != nil {
		return err
	}
	fmt.Printf("Desktop %s parked. Workspace %s remains claimed.\n", id, w.WorkspaceName)
	return nil
}

func completePark(ctx context.Context, s store.Store, original *store.Desktop, spec *store.RestoreSpec, volumeID string, destroy, awaitGone func() error) error {
	id := original.DesktopID
	if original.State != store.StateParking {
		if err := s.Transition(ctx, id, original.State, store.StateParking); err != nil {
			return err
		}
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	d.RestoreSpec = spec
	if d.ParkRootVolumeID == "" {
		d.ParkRootVolumeID = volumeID
	}
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("persist restore metadata before destroy: %w", err)
	}
	if err := destroy(); err != nil {
		_ = s.RecordFailure(ctx, id, "park", err.Error())
		return err
	}
	if err := awaitGone(); err != nil {
		_ = s.RecordFailure(ctx, id, "park", err.Error())
		return err
	}
	d, err = s.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State, d.InstanceID, d.NoVNCURL, d.SSHTarget, d.Readiness = store.StateParked, "", "", "", ""
	d.StopReason, d.StoppedAt, d.FailurePhase, d.FailureMsg = "", "", "", ""
	d.ParkedAt = time.Now().UTC().Format(time.RFC3339)
	d.ParkRootVolumeID = ""
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("resources destroyed; record needs park reconciliation: %w", err)
	}
	return nil
}

func runUnpark(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	id := args[0]
	if err := desktop.ValidateID(id); err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("fleet store does not support EFS workspaces")
	}
	w, err := ws.GetWorkspace(ctx, d.Environment, d.WorkspaceName)
	if err != nil {
		return fmt.Errorf("read workspace: %w", err)
	}
	if err := parkEligible(d, w); err != nil {
		return err
	}
	if err := validateRestoreSpec(d, d.RestoreSpec, w); err != nil {
		return err
	}
	if d.State != store.StateParked && !(d.State == store.StateFailed && d.FailurePhase == "unpark") && !(d.State == store.StateRestoring && unparkRetry) {
		return fmt.Errorf("desktop %s is %s; unpark requires parked or a prior failed unpark", id, d.State)
	}
	if d.RestoreSpec.Config["workspaceEFSAccessPointId"] != w.EFSAccessPointID || d.RestoreSpec.Config["workspaceEFSFileSystemId"] != w.EFSFileSystemID {
		return fmt.Errorf("workspace EFS identity changed; refusing to restore against another workspace")
	}
	awsCfg, err := awsx.LoadConfig(ctx, d.Region, cfg.AWS.Profile)
	if err != nil {
		return err
	}
	if err := verifyWorkspaceAccessPoint(ctx, awsCfg, w); err != nil {
		return err
	}
	if cfg.Desktop.SSHKeyPath == "" {
		return fmt.Errorf("desktop.ssh_key_path is required to verify restored workspace and services")
	}
	if _, err := os.Stat(cfg.Desktop.SSHKeyPath); err != nil {
		return fmt.Errorf("SSH key for restore readiness: %w", err)
	}
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	if err := requireBackend(ctx); err != nil {
		return err
	}
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: d.Region}
	if err := completeUnpark(ctx, s, d, func() (map[string]string, error) {
		return runner.Up(ctx, desktopStackRef(d), pulumi.StackConfig(d.RestoreSpec.Config), os.Stderr)
	}, func(restored *store.Desktop) error { return waitUnparkReady(ctx, restored) }); err != nil {
		return err
	}
	restored, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	fmt.Printf("Desktop %s restored on instance %s with workspace %s.\n", id, restored.InstanceID, restored.WorkspaceName)
	return nil
}

func completeUnpark(ctx context.Context, s store.Store, original *store.Desktop, up func() (map[string]string, error), ready func(*store.Desktop) error) error {
	id := original.DesktopID
	if original.State != store.StateRestoring {
		if err := s.Transition(ctx, id, original.State, store.StateRestoring); err != nil {
			return err
		}
	}
	outputs, err := up()
	if err != nil {
		_ = s.RecordFailure(ctx, id, "unpark", err.Error())
		return err
	}
	mgr := desktop.NewManager(s)
	if err := mgr.UpdateFromOutputs(ctx, id, outputs); err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ready(d); err != nil {
		_ = s.RecordFailure(ctx, id, "unpark", err.Error())
		return err
	}
	return mgr.MarkReady(ctx, id, "restored and checked")
}

func validateRestoreSpec(d *store.Desktop, spec *store.RestoreSpec, w *store.Workspace) error {
	if spec == nil || spec.Version != 1 {
		return fmt.Errorf("desktop %s has no supported restore specification", d.DesktopID)
	}
	for _, key := range []string{"amiId", "volumeSize", "userDataBase64", "instanceType", "subnetId", "securityGroupId", "instanceProfile"} {
		if spec.Config[key] == "" {
			return fmt.Errorf("desktop %s restore spec lacks %s", d.DesktopID, key)
		}
	}
	if spec.Config["desktopId"] != d.DesktopID || spec.Config["workspaceMode"] != workspaceModeEFS || spec.Config["workspaceName"] != d.WorkspaceName || spec.Config["workspaceEFSAccessPointId"] != w.EFSAccessPointID || spec.Config["workspaceEFSFileSystemId"] != w.EFSFileSystemID {
		return fmt.Errorf("desktop %s restore spec does not match its logical ID or EFS workspace", d.DesktopID)
	}
	return nil
}

func waitUnparkReady(ctx context.Context, d *store.Desktop) error {
	if d.InstanceID == "" {
		return fmt.Errorf("Pulumi returned no instance ID")
	}
	awsCfg, err := awsx.LoadConfig(ctx, d.Region, cfg.AWS.Profile)
	if err != nil {
		return err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	for {
		status, err := awsx.InstanceStatus(waitCtx, awsCfg, d.InstanceID)
		if err == nil && status.State == "running" {
			host := d.Hostname
			if d.NoDNS {
				host = status.PublicIP
			}
			if host != "" {
				checkers := health.StandardCheckers(host, 22)
				if !d.NoDNS {
					checkers = append(checkers, health.NoVNCCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath)...)
				}
				checkers = append(checkers, health.NewSSHChecker("docker-active", host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, "systemctl is-active docker", 20*time.Second))
				checkers = append(checkers, health.NewSSHChecker("bridgectl-active", host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, `sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user is-active bridgectl`, 20*time.Second))
				checkers = append(checkers, health.WorkspaceCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.WorkspaceMode)...)
				checkers = append(checkers, health.RepoCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.Repos)...)
				if health.NewRunner(d.DesktopID, checkers...).Run(waitCtx).Passed {
					return nil
				}
			}
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("desktop %s did not pass restore readiness checks: %w", d.DesktopID, waitCtx.Err())
		case <-time.After(15 * time.Second):
		}
	}
}
