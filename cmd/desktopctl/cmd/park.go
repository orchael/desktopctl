package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/health"
	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var (
	parkDryRun, parkForce, parkRetry, parkRecreateAVDs, unparkRetry bool
	unparkBridgeEnrollmentSecret                                    string
)

var (
	parkCmd   = &cobra.Command{Use: "park <desktop-id>", Short: "Release an EFS desktop's instance and root disk while retaining its workspace", Args: cobra.ExactArgs(1), RunE: runPark}
	unparkCmd = &cobra.Command{Use: "unpark <desktop-id>", Short: "Restore a parked EFS desktop from its saved launch configuration", Args: cobra.ExactArgs(1), RunE: runUnpark}
)

func init() {
	parkCmd.Flags().BoolVar(&parkDryRun, "dry-run", false, "show the instance, root volume, workspace, and data loss without writes")
	parkCmd.Flags().BoolVar(&parkForce, "force", false, "confirm loss of RAM and machine-local data")
	parkCmd.Flags().BoolVar(&parkRetry, "retry", false, "resume a stale interrupted park after confirming the prior process exited")
	parkCmd.Flags().BoolVar(&parkRecreateAVDs, "recreate-avds", false, "discard AVD guest data and recreate configured AVDs when unparked")
	unparkCmd.Flags().BoolVar(&unparkRetry, "retry", false, "reconcile an interrupted restoring state after confirming no operation is active")
	unparkCmd.Flags().StringVar(&unparkBridgeEnrollmentSecret, "bridge-enrollment-secret", "", "replace the saved Bridge enrollment secret path for this restore; empty disables enrollment")
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
	return parkEligibleWithAVDs(d, w, false)
}

func parkEligibleWithAVDs(d *store.Desktop, w *store.Workspace, recreateAVDs bool) error {
	if d.WorkspaceMode != workspaceModeEFS || d.WorkspaceName == "" {
		return fmt.Errorf("desktop %s uses a local workspace; park requires a retained EFS workspace", d.DesktopID)
	}
	if recreateAVDs && len(d.AVDNames) == 0 {
		return fmt.Errorf("desktop %s has no configured AVDs to recreate", d.DesktopID)
	}
	if (d.NestedVirt || len(d.AVDNames) > 0) && (!recreateAVDs || len(d.AVDNames) == 0) {
		return fmt.Errorf("desktop %s has nested virtualization or AVD guests with machine-local state; use --recreate-avds only if guest data may be discarded", d.DesktopID)
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
	if err := parkEligibleWithAVDs(d, w, parkRecreateAVDs); err != nil {
		return err
	}
	spec := d.RestoreSpec
	if err := validateRestoreSpec(d, spec, w); err != nil {
		return err
	}
	if parkRecreateAVDs {
		if err := validateAVDRestore(d); err != nil {
			return err
		}
	}
	if d.State == store.StateParking && parkRetry {
		if err := requireStaleLifecycle(d, "park", 2*time.Hour, time.Now()); err != nil {
			return err
		}
	} else if d.State != store.StateReady && d.State != store.StateStopped && !(d.State == store.StateFailed && d.FailurePhase == "park") {
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
	if parkRecreateAVDs && len(d.AVDNames) > 0 {
		fmt.Printf("AVDs recreated from scratch: %s. Installed apps, device settings, and snapshots on these guests will be lost.\n", strings.Join(d.AVDNames, ", "))
	}
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
	if volume.Size > 0 {
		spec.Config["volumeSize"] = fmt.Sprintf("%d", volume.Size)
	}
	if err := completePark(ctx, s, d, spec, volume.ID, parkRecreateAVDs, func() error { return runner.DestroyNoCancel(ctx, desktopStackRef(d), os.Stderr) }, func() error {
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

func completePark(ctx context.Context, s store.Store, original *store.Desktop, spec *store.RestoreSpec, volumeID string, recreateAVDs bool, destroy, awaitGone func() error) error {
	id := original.DesktopID
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return fmt.Errorf("generate park ownership token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes[:])
	if original.State != store.StateParking {
		if err := s.Transition(ctx, id, original.State, store.StateParking); err != nil {
			return err
		}
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateParking || (original.State == store.StateParking && d.StateVersion != original.StateVersion) {
		return store.ErrStateConflict
	}
	d.RestoreSpec = spec
	d.ParkOperationToken = token
	if d.ParkRootVolumeID == "" {
		d.ParkRootVolumeID = volumeID
	}
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("persist restore metadata before destroy: %w", err)
	}
	if err := requireParkOwner(ctx, s, id, token); err != nil {
		return err
	}
	if err := destroy(); err != nil {
		_ = recordParkFailure(ctx, s, id, token, err.Error())
		return err
	}
	if err := requireParkOwner(ctx, s, id, token); err != nil {
		return err
	}
	if err := awaitGone(); err != nil {
		_ = recordParkFailure(ctx, s, id, token, err.Error())
		return err
	}
	d, err = s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateParking || d.ParkOperationToken != token {
		return store.ErrStateConflict
	}
	d.State, d.InstanceID, d.NoVNCURL, d.SSHTarget, d.Readiness = store.StateParked, "", "", "", ""
	d.RecreateAVDsOnUnpark = recreateAVDs
	d.ParkOperationToken = ""
	d.StopReason, d.StoppedAt, d.FailurePhase, d.FailureMsg = "", "", "", ""
	d.ParkedAt = time.Now().UTC().Format(time.RFC3339)
	d.ParkRootVolumeID = ""
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("resources destroyed; record needs park reconciliation: %w", err)
	}
	return nil
}

func requireParkOwner(ctx context.Context, s store.Store, id, token string) error {
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateParking || d.ParkOperationToken != token {
		return store.ErrStateConflict
	}
	return nil
}

func recordParkFailure(ctx context.Context, s store.Store, id, token, message string) error {
	recorder, ok := s.(interface {
		RecordParkFailure(context.Context, string, string, string) error
	})
	if !ok {
		return fmt.Errorf("fleet store lacks fenced park failure support")
	}
	return recorder.RecordParkFailure(ctx, id, token, message)
}

func runUnpark(cmd *cobra.Command, args []string) error {
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
	if err := parkEligibleWithAVDs(d, w, d.RecreateAVDsOnUnpark); err != nil {
		return err
	}
	if err := validateRestoreSpec(d, d.RestoreSpec, w); err != nil {
		return err
	}
	if d.RecreateAVDsOnUnpark {
		if err := validateAVDRestore(d); err != nil {
			return err
		}
	}
	if d.State == store.StateRestoring && unparkRetry {
		if err := requireStaleLifecycle(d, "unpark", 2*time.Hour, time.Now()); err != nil {
			return err
		}
	}
	if d.State != store.StateParked && !(d.State == store.StateFailed && d.FailurePhase == "unpark") && !(d.State == store.StateRestoring && unparkRetry) {
		return fmt.Errorf("desktop %s is %s; unpark requires parked or a prior failed unpark", id, d.State)
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
	var bridgeOverride *string
	if cmd.Flags().Changed("bridge-enrollment-secret") {
		bridgeOverride = &unparkBridgeEnrollmentSecret
	}
	restoreCfg, err := restoreStackConfigWithBridgeSecret(d, bridgeOverride)
	if err != nil {
		return err
	}
	bridgePath, otherPaths, err := restoreBridgeSecretPaths(d, bridgeOverride)
	if err != nil {
		return err
	}
	bridgeARN := ""
	if bridgePath != "" {
		bridgeARN, err = validateUnparkBridgeSecret(ctx, awsCfg, bridgePath, otherPaths)
		if err != nil {
			return err
		}
	}
	if shouldReplaceFailedUnparkInstance(d) {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return fmt.Errorf("generate restore retry nonce: %w", err)
		}
		restoreCfg, err = restoreStackConfigForRetry(d, bridgeOverride, hex.EncodeToString(nonce[:]))
		if err != nil {
			return err
		}
	}
	retryNested := d.NestedVirt && shouldReplaceFailedUnparkInstance(d)
	var prepare func(*store.Desktop) error
	if bridgeOverride != nil {
		prepare = func(claimed *store.Desktop) error {
			return saveRestoredBridgeSecret(ctx, s, claimed, *bridgeOverride, bridgeARN)
		}
	}
	if err := completeUnparkWithPrepare(ctx, s, d, prepare, func() (map[string]string, error) {
		if d.NestedVirt {
			if retryNested {
				if err := resetFailedNestedUnpark(ctx, s, d,
					func() error { return runner.DestroyNoCancel(ctx, desktopStackRef(d), os.Stderr) },
					func(id string) error { return terminatePrelaunchedInstance(ctx, d.Region, cfg.AWS.Profile, id) }); err != nil {
					return nil, err
				}
			}
			if err := prepareNestedUnpark(ctx, s, d, restoreCfg); err != nil {
				return nil, err
			}
		}
		return runner.Up(ctx, desktopStackRef(d), restoreCfg, os.Stderr)
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

func resetFailedNestedUnpark(ctx context.Context, s store.Store, d *store.Desktop, destroy func() error, terminate func(string) error) error {
	if err := requireUnparkOwner(ctx, s, d.DesktopID, d.UnparkOperationToken); err != nil {
		return err
	}
	// The nested virtualization workaround imports a prelaunched EC2 instance.
	// Clear its old stack before launching a replacement; otherwise it reuses
	// the instance with failed cloud-init despite the new user data.
	if err := destroy(); err != nil {
		return fmt.Errorf("destroy failed nested restore stack: %w", err)
	}
	if d.InstanceID != "" {
		if err := terminate(d.InstanceID); err != nil {
			return fmt.Errorf("terminate failed nested restore instance: %w", err)
		}
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		return err
	}
	if current.State != store.StateRestoring || current.UnparkOperationToken != d.UnparkOperationToken || current.InstanceID != d.InstanceID {
		return store.ErrStateConflict
	}
	current.InstanceID = ""
	if err := s.Update(ctx, current); err != nil {
		return err
	}
	d.InstanceID = ""
	return nil
}

func completeUnpark(ctx context.Context, s store.Store, original *store.Desktop, up func() (map[string]string, error), ready func(*store.Desktop) error) error {
	return completeUnparkWithPrepare(ctx, s, original, nil, up, ready)
}

func completeUnparkWithPrepare(ctx context.Context, s store.Store, original *store.Desktop, prepare func(*store.Desktop) error, up func() (map[string]string, error), ready func(*store.Desktop) error) error {
	id := original.DesktopID
	if original.State == store.StateRestoring {
		if err := requireStaleLifecycle(original, "unpark", 2*time.Hour, time.Now()); err != nil {
			return err
		}
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return fmt.Errorf("generate unpark ownership token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes[:])
	if original.State != store.StateRestoring {
		if err := s.Transition(ctx, id, original.State, store.StateRestoring); err != nil {
			return err
		}
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateRestoring || (original.State == store.StateRestoring && d.StateVersion != original.StateVersion) {
		return store.ErrStateConflict
	}
	d.UnparkOperationToken = token
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("claim unpark: %w", err)
	}
	*original = *d
	if prepare != nil {
		if err := prepare(original); err != nil {
			_ = recordUnparkFailure(ctx, s, id, token, err.Error())
			return err
		}
	}
	outputs, err := up()
	if err != nil {
		_ = recordUnparkFailure(ctx, s, id, token, err.Error())
		return err
	}
	if err := requireUnparkOwner(ctx, s, id, token); err != nil {
		return err
	}
	mgr := desktop.NewManager(s)
	if err := mgr.UpdateFromOutputsOwned(ctx, id, outputs, token); err != nil {
		return err
	}
	d, err = s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := ready(d); err != nil {
		_ = recordUnparkFailure(ctx, s, id, token, err.Error())
		return err
	}
	d, err = s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateRestoring || d.UnparkOperationToken != token {
		return store.ErrStateConflict
	}
	d.State, d.Readiness, d.UnparkOperationToken = store.StateReady, "restored and checked", ""
	d.FailurePhase, d.FailureMsg = "", ""
	return s.Update(ctx, d)
}

func requireUnparkOwner(ctx context.Context, s store.Store, id, token string) error {
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != store.StateRestoring || d.UnparkOperationToken != token {
		return store.ErrStateConflict
	}
	return nil
}

func recordUnparkFailure(ctx context.Context, s store.Store, id, token, message string) error {
	recorder, ok := s.(interface {
		RecordUnparkFailure(context.Context, string, string, string) error
	})
	if !ok {
		return fmt.Errorf("fleet store lacks fenced unpark failure support")
	}
	return recorder.RecordUnparkFailure(ctx, id, token, message)
}

func validateRestoreSpec(d *store.Desktop, spec *store.RestoreSpec, w *store.Workspace) error {
	if spec == nil || (spec.Version != 1 && spec.Version != 2) {
		return fmt.Errorf("desktop %s has no supported restore specification; run `desktopctl migrate-restore-spec` before parking", d.DesktopID)
	}
	if spec.Version == 1 && spec.BootstrapJSON == "" {
		return fmt.Errorf("desktop %s lacks saved bootstrap settings; cannot safely restore changed secret paths", d.DesktopID)
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

// restoreStackConfig regenerates user data from the saved non-secret boot
// settings and the current fleet secret list. A removed path is never replayed.
func restoreStackConfig(d *store.Desktop) (pulumi.StackConfig, error) {
	return restoreStackConfigWithBridgeSecret(d, nil)
}

func restoreStackConfigWithBridgeSecret(d *store.Desktop, bridgeOverride *string) (pulumi.StackConfig, error) {
	return renderRestoreStackConfig(d, bridgeOverride, "")
}

func restoreStackConfigForRetry(d *store.Desktop, bridgeOverride *string, nonce string) (pulumi.StackConfig, error) {
	return renderRestoreStackConfig(d, bridgeOverride, nonce)
}

func shouldReplaceFailedUnparkInstance(d *store.Desktop) bool {
	// Pulumi can create an instance before returning an error, leaving the fleet
	// record without its ID. Treat every failed/interrupted restore as a retry.
	return (d.State == store.StateFailed && d.FailurePhase == "unpark") || d.State == store.StateRestoring
}

func renderRestoreStackConfig(d *store.Desktop, bridgeOverride *string, retryNonce string) (pulumi.StackConfig, error) {
	if d.RestoreSpec != nil && d.RestoreSpec.Version == 2 {
		if bridgeOverride != nil {
			return nil, fmt.Errorf("Bridge enrollment secret override requires a version 1 restore specification")
		}
		config, err := legacyRestoreConfig(d)
		if err != nil {
			return nil, err
		}
		return applyRestoreRetry(config, retryNonce)
	}
	if d.RestoreSpec == nil || d.RestoreSpec.BootstrapJSON == "" {
		return nil, fmt.Errorf("desktop %s lacks saved bootstrap settings", d.DesktopID)
	}
	var boot provision.BootstrapConfig
	if err := json.Unmarshal([]byte(d.RestoreSpec.BootstrapJSON), &boot); err != nil {
		return nil, fmt.Errorf("decode restore bootstrap settings: %w", err)
	}
	if boot.DesktopID != d.DesktopID || boot.WorkspaceMode != workspaceModeEFS || boot.WorkspaceName != d.WorkspaceName || boot.EFSAccessPointID != d.RestoreSpec.Config["workspaceEFSAccessPointId"] {
		return nil, fmt.Errorf("saved bootstrap settings do not match desktop %s", d.DesktopID)
	}
	if bridgeOverride != nil {
		boot.BridgeEnrollmentSecretPath = *bridgeOverride
	}
	agentPath := boot.AgentSecretPath
	if agentPath == "" && cfg != nil && containsString(d.Secrets, cfg.GitHub.AgentSecret) {
		agentPath = cfg.GitHub.AgentSecret
	}
	if !containsString(d.Secrets, agentPath) {
		agentPath = ""
	}
	boot.AgentSecretPath = agentPath
	boot.DesktopSecretPaths = runtimeDesktopSecretPaths(agentPath, d.Secrets)
	if !containsString(d.Secrets, boot.ProfileSecretPath) {
		boot.ProfileSecretPath = ""
	}
	userData, err := provision.RenderCloudInit(&boot)
	if err != nil {
		return nil, fmt.Errorf("render restore cloud-init: %w", err)
	}
	compressed, err := gzipBase64UserData(userData)
	if err != nil {
		return nil, err
	}
	config := make(pulumi.StackConfig, len(d.RestoreSpec.Config))
	for key, value := range d.RestoreSpec.Config {
		config[key] = value
	}
	config["userDataBase64"] = compressed
	return applyRestoreRetry(config, retryNonce)
}

func applyRestoreRetry(config pulumi.StackConfig, retryNonce string) (pulumi.StackConfig, error) {
	config["replaceInstanceOnRestore"] = "false"
	if retryNonce != "" {
		userData, err := decodedUserData(config["userDataBase64"])
		if err != nil {
			return nil, fmt.Errorf("decode restore user data for retry: %w", err)
		}
		compressed, err := gzipBase64UserData(userData + "\n# desktopctl restore retry " + retryNonce + "\n")
		if err != nil {
			return nil, err
		}
		config["userDataBase64"] = compressed
		config["replaceInstanceOnRestore"] = "true"
	}
	return config, nil
}

func restoreBridgeSecretPaths(d *store.Desktop, override *string) (string, []string, error) {
	if d.RestoreSpec == nil || d.RestoreSpec.Version != 1 {
		return "", nil, nil
	}
	var boot provision.BootstrapConfig
	if err := json.Unmarshal([]byte(d.RestoreSpec.BootstrapJSON), &boot); err != nil {
		return "", nil, fmt.Errorf("decode saved bootstrap settings for Bridge secret validation: %w", err)
	}
	path := boot.BridgeEnrollmentSecretPath
	if override != nil {
		path = *override
	}
	other := append([]string(nil), d.Secrets...)
	if boot.GitHubSecretPath != "" && !containsString(other, boot.GitHubSecretPath) {
		other = append(other, boot.GitHubSecretPath)
	}
	return path, other, nil
}

func saveRestoredBridgeSecret(ctx context.Context, s store.Store, d *store.Desktop, path, arn string) error {
	if (path == "") != (arn == "") {
		return fmt.Errorf("Bridge enrollment path and identity must both be set or both be empty")
	}
	if err := requireUnparkOwner(ctx, s, d.DesktopID, d.UnparkOperationToken); err != nil {
		return err
	}
	if d.RestoreSpec == nil || d.RestoreSpec.Version != 1 {
		return fmt.Errorf("desktop %s lacks a version 1 restore specification", d.DesktopID)
	}
	var boot provision.BootstrapConfig
	if err := json.Unmarshal([]byte(d.RestoreSpec.BootstrapJSON), &boot); err != nil {
		return fmt.Errorf("decode saved bootstrap settings: %w", err)
	}
	boot.BridgeEnrollmentSecretPath = path
	raw, err := json.Marshal(&boot)
	if err != nil {
		return err
	}
	d.RestoreSpec.BootstrapJSON = string(raw)
	d.RestoreSpec.BridgeEnrollmentSecretARN = arn
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("save Bridge enrollment path before restore: %w", err)
	}
	return nil
}

func validateUnparkBridgeSecret(ctx context.Context, awsCfg aws.Config, path string, desktopPaths []string) (string, error) {
	client := secretsmanager.NewFromConfig(awsCfg)
	resolve := func(ctx context.Context, secretID string) (string, error) {
		return resolveSecretARN(ctx, client, secretID)
	}
	arn, err := resolve(ctx, path)
	if err != nil {
		return "", fmt.Errorf("resolve Bridge enrollment secret identity: %w", err)
	}
	if err := validateBridgeEnrollmentARNIsolation(ctx, arn, desktopPaths, resolve); err != nil {
		return "", err
	}
	return arn, nil
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
				checkers = append(checkers, health.NewRequiredBootstrapStatusChecker(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, 20*time.Second))
				if !d.NoDNS {
					checkers = append(checkers, health.NoVNCCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath)...)
				}
				checkers = append(checkers, health.NewSSHChecker("docker-active", host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, "systemctl is-active docker", 20*time.Second))
				checkers = append(checkers, health.NewSSHChecker("bridgectl-active", host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, `sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user is-active bridgectl`, 20*time.Second))
				checkers = append(checkers, health.WorkspaceCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.WorkspaceMode)...)
				checkers = append(checkers, health.RepoCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.Repos)...)
				if d.RecreateAVDsOnUnpark {
					checkers = append(checkers, health.AVDRequiredCheckers(host, 22, "ubuntu", cfg.Desktop.SSHKeyPath, d.AVDNames)...)
				}
				health.UseSSHHostKeyAlias(checkers, d.InstanceID)
				report := health.NewRunner(d.DesktopID, checkers...).Run(waitCtx)
				for _, check := range report.Checks {
					if check.Name == "bootstrap-state" && check.Status == health.StatusFail && strings.HasPrefix(check.Message, "failed with exit code") {
						return fmt.Errorf("desktop %s bootstrap failed: %s", d.DesktopID, check.Message)
					}
				}
				if report.Passed {
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
