package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var (
	terminateForce            bool
	terminateReconcileMissing bool
)

var terminateCmd = &cobra.Command{
	Use:     "terminate <desktop-id>",
	Aliases: []string{"destroy"},
	Short:   "Permanently destroy a desktop and its disk state",
	Long: `terminate runs pulumi destroy on the desktop stack, which permanently removes
the EC2 instance and root EBS volume. This operation is IRREVERSIBLE.

The fleet record is marked terminated only after pulumi destroy succeeds.
If destroy fails, the instance is left running for diagnosis and the record
is marked failed. Use --force to attempt termination from a failed state.`,
	Args: cobra.ExactArgs(1),
	RunE: runTerminate,
}

func init() {
	terminateCmd.Flags().BoolVar(&terminateForce, "force", false, "terminate even if in a failed or unhealthy state")
	terminateCmd.Flags().BoolVar(&terminateReconcileMissing, "reconcile-missing", false, "destroy a desktop stack even when its fleet record is absent")
	rootCmd.AddCommand(terminateCmd)
}

func runTerminate(cmd *cobra.Command, args []string) error {
	id := args[0]
	if err := desktop.ValidateID(id); err != nil {
		return err
	}
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := context.Background()

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if terminateReconcileMissing {
				if err := requireBackend(ctx); err != nil {
					return err
				}
				backendURL := "s3://" + cfg.Pulumi.BackendBucket
				workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
				ref := pulumi.DesktopStackRef(backendURL, id, workDir)
				return (&pulumi.Runner{AWSProfile: cfg.AWS.Profile}).Destroy(ctx, ref, os.Stderr)
			}
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	if d.State == store.StateTerminated {
		if terminateReconcileMissing {
			return nil
		}
		return fmt.Errorf("desktop %q is already terminated", id)
	}

	if !terminateForce && (d.State == store.StateFailed || d.State == store.StateProvisioningFailed) {
		return fmt.Errorf("desktop %q is in state %q; use --force to terminate anyway", id, d.State)
	}

	mgr := desktop.NewManager(s)
	if err := mgr.MarkTerminating(ctx, id); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Terminating desktop %s (stack %s) ...\n", id, d.StackName)
	fmt.Fprintln(os.Stderr, terminateWarning(d))

	if err := requireBackend(ctx); err != nil {
		return err
	}

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	ref := pulumi.DesktopStackRef(backendURL, id, workDir)
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}

	if d.TailscaleNet != "" {
		fmt.Fprintf(os.Stderr, "Removing Tailscale machine %s from %s ...\n", id, d.TailscaleNet)
		tailscaleAPIKey, err := resolveTailscaleAPIKey(ctx, d.GitHubOwner)
		if err == nil {
			err = removeTailscaleDesktopDevice(ctx, d.TailscaleNet, id, tailscaleAPIKey)
		}
		if err != nil {
			if errors.Is(err, errTailscaleAPIKeyMissing) {
				fmt.Fprintf(os.Stderr, "WARNING: TAILSCALE_API_KEY is not set and was not found in the operator secret; skipping Tailscale machine cleanup for %s.\n", id)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: could not remove Tailscale machine %s: %v\n", id, err)
			}
		}
	}

	if d.Region == "" {
		d.Region = cfg.AWS.Region
	}
	if d.Environment == "" {
		d.Environment = cfg.Fleet.Environment
	}
	if err := destroyDesktopResources(ctx, runner, ref, d, cfg.AWS.Profile, listNestedDesktopInstanceIDs, terminatePrelaunchedInstance); err != nil {
		_ = mgr.RecordFailure(ctx, id, "terminate", err.Error())
		return fmt.Errorf("destroy resources: %w (record marked failed for reconciliation)", err)
	}

	if err := s.MarkTerminated(ctx, id); err != nil {
		return fmt.Errorf("mark terminated: %w", err)
	}
	if d.WorkspaceMode == workspaceModeEFS && d.WorkspaceName != "" {
		if ws, ok := s.(store.WorkspaceStore); ok {
			env := d.Environment
			if env == "" {
				env = cfg.Fleet.Environment
			}
			if err := ws.DetachWorkspace(ctx, env, d.WorkspaceName, d.DesktopID); err != nil {
				return fmt.Errorf("desktop terminated, but failed to detach workspace %q: %w; verify no live desktop is using the mount, then run `%s`", d.WorkspaceName, err, workspaceDetachRecoveryCommand(env, d.WorkspaceName))
			}
		}
	}

	fmt.Printf("Desktop %s terminated.\n", id)
	return nil
}

func destroyDesktopResources(ctx context.Context, runner createDestroyer, ref *pulumi.StackRef, d *store.Desktop, profile string, listInstances func(context.Context, string, string, *store.Desktop) ([]string, error), terminateInstance func(context.Context, string, string, string) error) error {
	if err := runner.Destroy(ctx, ref, os.Stderr); err != nil {
		return fmt.Errorf("pulumi destroy: %w", err)
	}
	if d.NestedVirt {
		ids, err := listInstances(ctx, d.Region, profile, d)
		if err != nil {
			return fmt.Errorf("list nested instances for desktop %s: %w", d.DesktopID, err)
		}
		seen := make(map[string]bool, len(ids)+1)
		if d.InstanceID != "" {
			ids = append(ids, d.InstanceID)
		}
		if len(ids) == 0 {
			return fmt.Errorf("cannot confirm absence of nested instance for desktop %s after unknown launch outcome", d.DesktopID)
		}
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if err := terminateInstance(ctx, d.Region, profile, id); err != nil {
				return fmt.Errorf("verify nested instance %s terminated: %w", id, err)
			}
		}
	}
	return nil
}

func listNestedDesktopInstanceIDs(ctx context.Context, region, profile string, d *store.Desktop) ([]string, error) {
	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return nil, err
	}
	client := ec2sdk.NewFromConfig(awsCfg)
	paginator := ec2sdk.NewDescribeInstancesPaginator(client, &ec2sdk.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:managed-by"), Values: []string{"ai-desktops"}},
			{Name: aws.String("tag:desktop-id"), Values: []string{d.DesktopID}},
			{Name: aws.String("tag:environment"), Values: []string{d.Environment}},
		},
	})
	var ids []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				if id := aws.ToString(instance.InstanceId); id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids, nil
}

func terminateWarning(d *store.Desktop) string {
	if d.WorkspaceMode == workspaceModeEFS && d.WorkspaceName != "" {
		return fmt.Sprintf("WARNING: This will permanently destroy the EC2 instance and EBS volume. EFS workspace %q will not be deleted.", d.WorkspaceName)
	}
	return "WARNING: This will permanently destroy the EC2 instance and EBS volume."
}

func workspaceDetachRecoveryCommand(env, workspaceName string) string {
	if env == "" {
		return fmt.Sprintf("ai-desktops workspace detach %s --force", workspaceName)
	}
	return fmt.Sprintf("ai-desktops workspace detach %s --env %s --force", workspaceName, env)
}
