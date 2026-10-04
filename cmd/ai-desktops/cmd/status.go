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

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

var statusRefreshDNS bool

var statusCmd = &cobra.Command{
	Use:   "status <desktop-id>",
	Short: "Show the status of a desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusRefreshDNS, "refresh-dns", false, "refresh Pulumi state and update the Route53 DNS record before showing status")
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
	if statusRefreshDNS {
		if err := refreshStatusDNS(ctx, s, id, d); err != nil {
			return err
		}
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	costLabel := ""
	if d.InstanceType != "" {
		if awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile); err != nil {
			costLabel = "unavailable (" + err.Error() + ")"
		} else {
			costLabel = estimateHourlyCostLabel(ctx, awsCfg, region, d.InstanceType, effectiveMarketType(d))
		}
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(desktopStatusJSON(d, costLabel))
	}

	printDesktopStatusWithAgentSecret(os.Stdout, d, region, fetchNoVNCDesktopURL(d), costLabel, cfg.GitHub.AgentSecret)
	return nil
}

type desktopStatusOutput struct {
	*store.Desktop
	EstimatedHourlyCost string `json:"estimated_hourly_cost,omitempty"`
}

func desktopStatusJSON(d *store.Desktop, costLabel string) desktopStatusOutput {
	return desktopStatusOutput{
		Desktop:             d,
		EstimatedHourlyCost: costLabel,
	}
}

func refreshStatusDNS(ctx context.Context, s store.Store, id string, d *store.Desktop) error {
	if d.InstanceID == "" {
		return fmt.Errorf("desktop %q has no instance ID", id)
	}
	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config for DNS refresh: %w", err)
	}
	status, err := awsx.InstanceStatus(ctx, awsCfg, d.InstanceID)
	if err != nil {
		return fmt.Errorf("check instance state before DNS refresh: %w", err)
	}
	if !canRefreshDNSForInstanceState(status.State) {
		return fmt.Errorf("cannot refresh DNS for desktop %q while instance %s is %q; start the desktop first", id, d.InstanceID, status.State)
	}
	zone, err := cfg.DNSZone()
	if err != nil {
		return err
	}
	if d.Hostname == "" {
		d.Hostname = fmt.Sprintf("%s.%s", id, zone)
	}
	if status.PublicIP == "" {
		fmt.Fprintln(os.Stderr, "Waiting for public IP assignment ...")
		publicIP, err := awsx.WaitInstancePublicIP(ctx, awsCfg, d.InstanceID, 2*time.Minute)
		if err != nil {
			return err
		}
		status.PublicIP = publicIP
	}

	fmt.Fprintf(os.Stderr, "Refreshing DNS record %s -> %s ...\n", d.Hostname, status.PublicIP)
	if err := awsx.UpsertARecord(ctx, awsCfg, zone, d.Hostname, status.PublicIP); err != nil {
		return err
	}
	d.NoVNCURL = fmt.Sprintf("https://%s:8443/novnc/vnc.html", d.Hostname)
	d.SSHTarget = fmt.Sprintf("ubuntu@%s", d.Hostname)
	if d.AMIID == "" && status.ImageID != "" {
		d.AMIID = status.ImageID
	}
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("update store record: %w", err)
	}
	return nil
}

func canRefreshDNSForInstanceState(state string) bool {
	return state == "running"
}

// printDesktopStatus writes the human-readable status block to w.
func printDesktopStatus(w io.Writer, d *store.Desktop, region, liveURL, costLabel string) {
	printDesktopStatusWithAgentSecret(w, d, region, liveURL, costLabel, "")
}

func printDesktopStatusWithAgentSecret(w io.Writer, d *store.Desktop, region, liveURL, costLabel, configuredAgentPath string) {
	fmt.Fprintf(w, "Desktop ID   : %s\n", d.DesktopID)
	if d.DesktopName != "" {
		fmt.Fprintf(w, "Desktop name : %s\n", d.DesktopName)
	}
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
	if costLabel != "" {
		fmt.Fprintf(w, "Hourly cost  : %s\n", costLabel)
	}
	if d.StopReason != "" {
		fmt.Fprintf(w, "Stop reason  : %s\n", d.StopReason)
	}
	if d.StoppedAt != "" {
		fmt.Fprintf(w, "Stopped at   : %s\n", d.StoppedAt)
	}
	if d.AMIID != "" {
		fmt.Fprintf(w, "AMI ID       : %s\n", d.AMIID)
	}
	workspaceMode := d.WorkspaceMode
	if workspaceMode == "" {
		workspaceMode = workspaceModeLocal
	}
	fmt.Fprintf(w, "Workspace    : %s\n", workspaceMode)
	if d.WorkspaceName != "" {
		fmt.Fprintf(w, "Workspace name: %s\n", d.WorkspaceName)
	}
	if d.WorkspacePath != "" {
		fmt.Fprintf(w, "Workspace path: %s\n", d.WorkspacePath)
	}
	fmt.Fprintf(w, "Pulumi stack : %s\n", d.StackName)
	fmt.Fprintf(w, "Readiness    : %s\n", d.Readiness)
	if len(d.Repos) > 0 {
		fmt.Fprintf(w, "Repos        : %s\n", strings.Join(d.Repos, ", "))
	}
	agentPath, desktopPaths := runtimeSecretPaths(configuredAgentPath, d.Secrets)
	if agentPath != "" {
		fmt.Fprintf(w, "Agent secret : %s\n", agentPath)
	}
	if len(desktopPaths) > 0 {
		fmt.Fprintf(w, "Secrets      : %s\n", strings.Join(desktopPaths, ", "))
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
