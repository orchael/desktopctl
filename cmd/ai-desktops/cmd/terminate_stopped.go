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

	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var (
	terminateStoppedOlderThan string
	terminateStoppedDryRun    bool
	terminateStoppedForce     bool
)

var terminateStoppedCmd = &cobra.Command{
	Use:   "terminate-stopped",
	Short: "Terminate stopped desktops that have been idle longer than a threshold",
	Long: `Terminates stopped desktops older than --older-than. This permanently
destroys the EC2 instance and root EBS volume for each matching desktop.

Use --dry-run to preview which desktops would be terminated without making
any changes. Requires --force or interactive confirmation to proceed.

This operation is IRREVERSIBLE.`,
	Args: cobra.NoArgs,
	RunE: runTerminateStopped,
}

func init() {
	terminateStoppedCmd.Flags().StringVar(&terminateStoppedOlderThan, "older-than", "14d", "terminate desktops stopped for longer than this duration (e.g. 7d, 14d, 48h)")
	terminateStoppedCmd.Flags().BoolVar(&terminateStoppedDryRun, "dry-run", false, "show what would be terminated without making changes")
	terminateStoppedCmd.Flags().BoolVar(&terminateStoppedForce, "force", false, "skip interactive confirmation")
	rootCmd.AddCommand(terminateStoppedCmd)
}

func runTerminateStopped(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}

	threshold, err := parseStaleDuration(terminateStoppedOlderThan)
	if err != nil {
		return fmt.Errorf("--older-than: %w", err)
	}

	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	desktops, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("list desktops: %w", err)
	}

	now := time.Now().UTC()
	stale := filterStaleDesktops(desktops, threshold, now)

	if len(stale) == 0 {
		fmt.Printf("No stopped desktops older than %s.\n", terminateStoppedOlderThan)
		return nil
	}

	ids := buildTerminateStoppedDryRunLines(stale, now)

	fmt.Fprintf(os.Stderr, "The following %d desktop(s) would be terminated (IRREVERSIBLE):\n", len(stale))
	for _, id := range ids {
		fmt.Fprintf(os.Stderr, "  %s\n", id)
	}

	if terminateStoppedDryRun {
		fmt.Fprintln(os.Stderr, "\nDry run — no changes made.")
		return nil
	}

	if !terminateStoppedForce {
		fmt.Fprint(os.Stderr, "\nType \"yes\" to confirm termination of all listed desktops: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.TrimSpace(scanner.Text()) != "yes" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	if err := requireBackend(ctx); err != nil {
		return err
	}

	var errs []string
	for _, d := range stale {
		if err := terminateOneStoppedDesktop(ctx, s, d); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", d.DesktopID, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to terminate %d desktop(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}
	fmt.Printf("Terminated %d desktop(s).\n", len(stale))
	return nil
}

func terminateOneStoppedDesktop(ctx context.Context, s store.Store, d *store.Desktop) error {
	// Re-fetch the current record to guard against races where the desktop was
	// started between the initial list and this termination attempt.
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q no longer exists", d.DesktopID)
		}
		return err
	}
	if current.State != store.StateStopped {
		return fmt.Errorf("desktop %q is no longer stopped (state: %s); skipping", d.DesktopID, current.State)
	}

	mgr := desktop.NewManager(s)
	if err := mgr.MarkTerminating(ctx, d.DesktopID); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Terminating %s (stopped %s) ...\n", d.DesktopID, formatStoppedAge(d.StoppedAt, time.Now().UTC()))
	fmt.Fprintln(os.Stderr, terminateWarning(current))

	if current.TailscaleNet != "" {
		fmt.Fprintf(os.Stderr, "Removing Tailscale machine %s from %s ...\n", d.DesktopID, current.TailscaleNet)
		tailscaleAPIKey, err := resolveTailscaleAPIKey(ctx, current.GitHubOwner)
		if err == nil {
			err = removeTailscaleDesktopDevice(ctx, current.TailscaleNet, d.DesktopID, tailscaleAPIKey)
		}
		if err != nil {
			if errors.Is(err, errTailscaleAPIKeyMissing) {
				fmt.Fprintf(os.Stderr, "WARNING: TAILSCALE_API_KEY is not set and was not found in the operator secret; skipping Tailscale machine cleanup for %s.\n", d.DesktopID)
			} else {
				fmt.Fprintf(os.Stderr, "WARNING: could not remove Tailscale machine %s: %v\n", d.DesktopID, err)
			}
		}
	}

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	ref := pulumi.DesktopStackRef(backendURL, d.DesktopID, workDir)
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}

	if err := runner.Destroy(ctx, ref, os.Stderr); err != nil {
		_ = mgr.RecordFailure(ctx, d.DesktopID, "terminate-stopped", err.Error())
		return fmt.Errorf("pulumi destroy: %w", err)
	}

	if err := s.MarkTerminated(ctx, d.DesktopID); err != nil {
		return fmt.Errorf("mark terminated: %w", err)
	}

	if current.WorkspaceMode == workspaceModeEFS && current.WorkspaceName != "" {
		if ws, ok := s.(store.WorkspaceStore); ok {
			env := current.Environment
			if env == "" {
				env = cfg.Fleet.Environment
			}
			if err := ws.DetachWorkspace(ctx, env, current.WorkspaceName, current.DesktopID); err != nil {
				return fmt.Errorf("desktop terminated, but failed to detach workspace %q: %w; run `%s`", current.WorkspaceName, err, workspaceDetachRecoveryCommand(env, current.WorkspaceName))
			}
		}
	}

	return nil
}

// buildTerminateStoppedDryRunLines returns the desktop IDs that would be
// terminated, in the order they would be processed.
func buildTerminateStoppedDryRunLines(desktops []*store.Desktop, now time.Time) []string {
	ids := make([]string, len(desktops))
	for i, d := range desktops {
		ids[i] = d.DesktopID
	}
	return ids
}
