package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var (
	terminateForce bool
)

var terminateCmd = &cobra.Command{
	Use:   "terminate <desktop-id>",
	Short: "Permanently destroy a desktop and its disk state",
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
	rootCmd.AddCommand(terminateCmd)
}

func runTerminate(cmd *cobra.Command, args []string) error {
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

	if d.State == store.StateTerminated {
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

	if err := runner.Destroy(ctx, ref, os.Stderr); err != nil {
		_ = mgr.RecordFailure(ctx, id, "terminate", err.Error())
		return fmt.Errorf("pulumi destroy: %w (desktop left running for diagnosis; record marked failed)", err)
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
				return fmt.Errorf("desktop terminated, but failed to detach workspace %q: %w", d.WorkspaceName, err)
			}
		}
	}

	fmt.Printf("Desktop %s terminated.\n", id)
	return nil
}

func terminateWarning(d *store.Desktop) string {
	if d.WorkspaceMode == workspaceModeEFS && d.WorkspaceName != "" {
		return fmt.Sprintf("WARNING: This will permanently destroy the EC2 instance and EBS volume. EFS workspace %q will not be deleted.", d.WorkspaceName)
	}
	return "WARNING: This will permanently destroy the EC2 instance and EBS volume."
}
