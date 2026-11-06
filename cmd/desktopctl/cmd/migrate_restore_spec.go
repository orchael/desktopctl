package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var migrateRestoreSpecApply bool

var migrateRestoreSpecCmd = &cobra.Command{
	Use:   "migrate-restore-spec",
	Short: "Backfill restore metadata for active desktops from their existing Pulumi stacks",
	Long:  "Read each active desktop and its deployed Pulumi config. By default, validate and print a dry run. --apply writes only restore metadata to the fleet record; it does not change the stack or EC2 instance.",
	Args:  cobra.NoArgs,
	RunE:  runMigrateRestoreSpec,
}

func init() {
	migrateRestoreSpecCmd.Flags().BoolVar(&migrateRestoreSpecApply, "apply", false, "write validated restore metadata to active fleet records")
	rootCmd.AddCommand(migrateRestoreSpecCmd)
}

func runMigrateRestoreSpec(cmd *cobra.Command, _ []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := cmd.Context()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	desktops, err := s.List(ctx)
	if err != nil {
		return err
	}
	desktops = filterListedDesktops(desktops, false, cfg.Fleet.OrganizationID)
	sort.Slice(desktops, func(i, j int) bool { return desktops[i].DesktopID < desktops[j].DesktopID })
	var failures []error
	ready, applied, already := 0, 0, 0
	for _, listed := range desktops {
		d, err := s.Get(ctx, listed.DesktopID)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", listed.DesktopID, err))
			continue
		}
		if d.RestoreSpec != nil {
			if d.RestoreSpec.Version == 1 && d.RestoreSpec.BootstrapJSON != "" {
				fmt.Printf("%s: already has restore metadata\n", d.DesktopID)
				already++
				continue
			}
			if d.RestoreSpec.Version == 2 {
				if _, err := legacyRestoreConfig(d); err != nil {
					failures = append(failures, fmt.Errorf("%s: existing restore metadata is invalid: %w", d.DesktopID, err))
					fmt.Printf("%s: blocked: existing restore metadata is invalid: %v\n", d.DesktopID, err)
					continue
				}
				fmt.Printf("%s: already has restore metadata\n", d.DesktopID)
				already++
				continue
			}
		}
		runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: d.Region}
		stackConfig, err := runner.Config(ctx, desktopStackRef(d))
		if err == nil {
			var awsCfgErr error
			awsCfg, loadErr := awsx.LoadConfig(ctx, d.Region, cfg.AWS.Profile)
			if loadErr != nil {
				awsCfgErr = loadErr
			} else {
				var status *awsx.EC2InstanceStatus
				status, awsCfgErr = awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
				if awsCfgErr == nil {
					awsCfgErr = reconcileDeployedInstance(d, stackConfig, status)
				}
			}
			err = awsCfgErr
		}
		if err == nil {
			err = validateLegacyRestoreCandidate(ctx, s, d, stackConfig)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", d.DesktopID, err))
			fmt.Printf("%s: blocked: %v\n", d.DesktopID, err)
			continue
		}
		d.RestoreSpec = &store.RestoreSpec{Version: 2, Config: stackConfig}
		if _, err := legacyRestoreConfig(d); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", d.DesktopID, err))
			fmt.Printf("%s: blocked: %v\n", d.DesktopID, err)
			continue
		}
		ready++
		if !migrateRestoreSpecApply {
			fmt.Printf("%s: ready to migrate\n", d.DesktopID)
			continue
		}
		if err := s.Update(ctx, d); err != nil {
			failures = append(failures, fmt.Errorf("%s: update fleet record: %w", d.DesktopID, err))
			fmt.Printf("%s: blocked: update fleet record: %v\n", d.DesktopID, err)
			continue
		}
		applied++
		fmt.Printf("%s: migrated\n", d.DesktopID)
	}
	fmt.Printf("Restore metadata: %d ready, %d applied, %d already present, %d blocked.\n", ready, applied, already, len(failures))
	return errors.Join(failures...)
}

func reconcileDeployedInstance(d *store.Desktop, config pulumi.StackConfig, status *awsx.EC2InstanceStatus) error {
	if status == nil || status.State != "running" && status.State != "stopped" || status.ImageID != d.AMIID || status.ImageID != config["amiId"] || status.InstanceType != d.InstanceType || status.SubnetID != config["subnetId"] || !slices.Contains(status.SecurityGroupIDs, config["securityGroupId"]) {
		return fmt.Errorf("live EC2 instance differs from fleet record or deployed Pulumi configuration")
	}
	// resize updates EC2 and the fleet record before a later Pulumi deployment.
	// The instance type is the sole allowed difference from deployment history.
	config["instanceType"] = status.InstanceType
	return nil
}

func validateLegacyRestoreCandidate(ctx context.Context, s store.Store, d *store.Desktop, config pulumi.StackConfig) error {
	if d.State != store.StateReady && d.State != store.StateStopped {
		return fmt.Errorf("state %s is not stable for metadata migration", d.State)
	}
	for _, key := range []string{"amiId", "volumeSize", "userDataBase64", "instanceType", "subnetId", "securityGroupId", "instanceProfile"} {
		if config[key] == "" {
			return fmt.Errorf("Pulumi config lacks %s", key)
		}
	}
	if config["desktopId"] != d.DesktopID || config["instanceType"] != d.InstanceType || config["amiId"] != d.AMIID || config["environment"] != d.Environment || config["githubOwner"] != d.GitHubOwner || config["aws:region"] != d.Region || config["workspaceMode"] != d.WorkspaceMode {
		return fmt.Errorf("Pulumi identity or launch settings differ from fleet record")
	}
	if d.WorkspaceMode == workspaceModeEFS {
		ws, ok := s.(store.WorkspaceStore)
		if !ok {
			return fmt.Errorf("fleet store does not support workspace validation")
		}
		w, err := ws.GetWorkspace(ctx, d.Environment, d.WorkspaceName)
		if err != nil {
			return fmt.Errorf("read EFS workspace: %w", err)
		}
		if config["workspaceName"] != d.WorkspaceName || config["workspaceEFSFileSystemId"] != w.EFSFileSystemID || config["workspaceEFSAccessPointId"] != w.EFSAccessPointID || w.AttachedDesktopID != d.DesktopID {
			return fmt.Errorf("Pulumi EFS workspace differs from attached fleet workspace")
		}
	}
	return nil
}
