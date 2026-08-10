package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <desktop-id>",
	Short: "Show the status of a desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
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

	if err := reconcileSpotDesktopState(ctx, s, d); err != nil {
		return err
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(d)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}

	printDesktopStatus(os.Stdout, d, region, fetchNoVNCDesktopURL(d))
	return nil
}

// printDesktopStatus writes the human-readable status block to w.
func printDesktopStatus(w io.Writer, d *store.Desktop, region, liveURL string) {
	fmt.Fprintf(w, "Desktop ID   : %s\n", d.DesktopID)
	fmt.Fprintf(w, "State        : %s\n", d.State)
	fmt.Fprintf(w, "Owner        : %s\n", d.GitHubOwner)
	fmt.Fprintf(w, "Region       : %s\n", region)
	fmt.Fprintf(w, "Hostname     : %s\n", d.Hostname)
	fmt.Fprintf(w, "Desktop URL  : %s\n", d.NoVNCURL)
	if liveURL != "" {
		fmt.Fprintf(w, "NoVNC URL    : %s\n", liveURL)
	}
	fmt.Fprintf(w, "SSH target   : %s\n", d.SSHTarget)
	fmt.Fprintf(w, "Instance ID  : %s\n", d.InstanceID)
	if d.InstanceType != "" {
		fmt.Fprintf(w, "Instance type: %s\n", d.InstanceType)
	}
	fmt.Fprintf(w, "Market type  : %s\n", effectiveMarketType(d))
	if d.StopReason != "" {
		fmt.Fprintf(w, "Stop reason  : %s\n", d.StopReason)
	}
	if d.StoppedAt != "" {
		fmt.Fprintf(w, "Stopped at   : %s\n", d.StoppedAt)
	}
	if d.AMIID != "" {
		fmt.Fprintf(w, "AMI ID       : %s\n", d.AMIID)
	}
	fmt.Fprintf(w, "Pulumi stack : %s\n", d.StackName)
	fmt.Fprintf(w, "Readiness    : %s\n", d.Readiness)
	if len(d.Repos) > 0 {
		fmt.Fprintf(w, "Repos        : %s\n", strings.Join(d.Repos, ", "))
	}
	if len(d.Secrets) > 0 {
		fmt.Fprintf(w, "Secrets      : %s\n", strings.Join(d.Secrets, ", "))
	}
	if d.TailscaleNet != "" {
		fmt.Fprintf(w, "Tailscale    : %s\n", d.TailscaleNet)
	}
	if d.StepCAServer != "" {
		fmt.Fprintf(w, "step-ca      : %s\n", d.StepCAServer)
	}
	fmt.Fprintf(w, "Created      : %s\n", d.CreatedAt)
	fmt.Fprintf(w, "Updated      : %s\n", d.UpdatedAt)
	if d.FailurePhase != "" {
		fmt.Fprintf(w, "Failure phase: %s\n", d.FailurePhase)
		fmt.Fprintf(w, "Failure msg  : %s\n", d.FailureMsg)
	}
}

func reconcileSpotDesktopState(ctx context.Context, s store.Store, d *store.Desktop) error {
	if effectiveMarketType(d) != store.MarketSpot || d.InstanceID == "" || !shouldReconcileSpotState(d.State) {
		return nil
	}
	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config for spot status reconciliation: %w", err)
	}
	status, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return fmt.Errorf("reconcile spot instance state: %w", err)
	}
	if !isStoppedOrStopping(status.State) {
		return nil
	}

	reason := d.StopReason
	if reason == "" {
		reason = store.StopReasonAWSStopped
		if isSpotInterruptionReason(status.StateTransitionReason) {
			reason = store.StopReasonSpotInterruption
		}
	}
	d.State = store.StateStopped
	d.StopReason = reason
	if d.StoppedAt == "" {
		d.StoppedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("update spot desktop state: %w", err)
	}
	return nil
}

func isStoppedOrStopping(state string) bool {
	return state == "stopped" || state == "stopping"
}

func shouldReconcileSpotState(state store.LifecycleState) bool {
	switch state {
	case store.StateReady, store.StateUnhealthy, store.StateCreating, store.StateFailed, store.StateProvisioningFailed:
		return true
	default:
		return false
	}
}

func isSpotInterruptionReason(reason string) bool {
	normalized := strings.ToLower(reason)
	return strings.Contains(normalized, "spotinstancetermination") ||
		strings.Contains(normalized, "spot instance") ||
		strings.Contains(normalized, "spot-instance") ||
		strings.Contains(normalized, "service initiated")
}

func effectiveMarketType(d *store.Desktop) string {
	if d.MarketType == store.MarketSpot {
		return store.MarketSpot
	}
	return store.MarketOnDemand
}

// parseNoVNCOutput extracts the URL from the output of novnc-desktop-url.
// The command emits a multi-line block; this picks the "Desktop URL :" line.
func parseNoVNCOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Desktop URL :"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// fetchNoVNCDesktopURL SSHes into the desktop and runs novnc-desktop-url to
// retrieve the live URL. Returns empty string on any error so callers can
// treat it as optional.
func fetchNoVNCDesktopURL(d *store.Desktop) string {
	if d.SSHTarget == "" || cfg.Desktop.SSHKeyPath == "" {
		return ""
	}
	if d.State != store.StateReady && d.State != store.StateUnhealthy {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "ssh", //nolint:gosec
		"-i", cfg.Desktop.SSHKeyPath,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"--",
		d.SSHTarget,
		"novnc-desktop-url",
	).CombinedOutput()
	return parseNoVNCOutput(string(out))
}
