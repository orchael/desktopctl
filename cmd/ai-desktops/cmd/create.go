package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/provision"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/repo"
	"github.com/spf13/cobra"
)

var (
	createOwner   string
	createRepos   []string
	createPreview bool
	createEnv     string
	createAMI     string
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new persistent AI coding desktop",
	Long: `create provisions a remote EC2 instance configured as an AI coding desktop
with novnc-desktop (Elementary), ai-agent-bridge, and developer tooling.

The --github-owner flag sets the required owner boundary for all repositories
on this desktop. Mixed-owner repositories are rejected before any infrastructure
is changed.`,
	Args: cobra.NoArgs,
	RunE: runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createOwner, "github-owner", "", "GitHub organization or username (required)")
	createCmd.Flags().StringArrayVar(&createRepos, "repo", nil, "GitHub repository to clone (repeatable)")
	createCmd.Flags().BoolVar(&createPreview, "preview", false, "preview infrastructure changes without applying")
	createCmd.Flags().StringVar(&createEnv, "env", "", "environment (prod|dev), overrides config")
	createCmd.Flags().StringVar(&createAMI, "ami", "", "override active AMI ID for this region (optional)")
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Fall back to config file owner when --github-owner not explicitly set.
	if createOwner == "" && cfg.GitHub.Owner != "" {
		createOwner = cfg.GitHub.Owner
	}
	if createOwner == "" {
		return fmt.Errorf("--github-owner is required (or set github.owner in config)")
	}

	// Validate repo inputs.
	repos, owner, err := parseAndValidateRepos(createOwner, createRepos)
	if err != nil {
		return err
	}

	env := createEnv
	if env == "" {
		env = cfg.Fleet.Environment
	}

	zone, err := cfg.DNSZone()
	if err != nil {
		return err
	}

	if err := requireBackend(ctx); err != nil {
		return err
	}

	req := &desktop.CreateRequest{
		GitHubOwner:   owner,
		Repos:         repoStrings(repos),
		InstanceType:  cfg.Desktop.InstanceType,
		Zone:          zone,
		OperatorCIDR:  cfg.Desktop.OperatorCIDR,
		SSHKeyPath:    cfg.Desktop.SSHKeyPath,
		PATSecret:     cfg.GitHub.PATSecret,
		BackendBucket: cfg.Pulumi.BackendBucket,
		Region:        cfg.AWS.Region,
		Profile:       cfg.AWS.Profile,
	}

	if err := req.Validate(); err != nil {
		return err
	}

	desktopID, err := desktop.GenerateID()
	if err != nil {
		return fmt.Errorf("generate desktop ID: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Creating desktop %s (env=%s, owner=%s) ...\n", desktopID, env, owner)

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}

	// Read foundation stack outputs to get the subnet, SG, and instance profile
	// that the desktop stack requires.
	foundationWorkDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	foundationRef := pulumi.FoundationStackRef(backendURL, env, foundationWorkDir)
	foundationOutputs, err := runner.Outputs(ctx, foundationRef)
	if err != nil {
		return fmt.Errorf("read foundation stack outputs (run init-foundation first): %w", err)
	}
	if err := pulumi.ValidateFoundationOutputs(foundationOutputs); err != nil {
		return fmt.Errorf("foundation stack incomplete (run init-foundation first): %w", err)
	}

	desktopWorkDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	desktopRef := pulumi.DesktopStackRef(backendURL, desktopID, desktopWorkDir)

	// Detect pre-baked AMI and render cloud-init accordingly.
	amiID := ""
	userData := ""
	// Use --ami flag if provided, otherwise use config active_ami.
	if createAMI != "" {
		amiID = createAMI
	} else if cfg.Desktop.ActiveAMI != nil {
		if ami, ok := cfg.Desktop.ActiveAMI[cfg.AWS.Region]; ok {
			amiID = ami
		}
	}

	// Render cloud-init with PackagesPreInstalled set based on whether we have a pre-baked AMI.
	hostname := desktop.Hostname(desktopID, zone)
	bootCfg := &provision.BootstrapConfig{
		DesktopID:             desktopID,
		Hostname:              hostname,
		GitHubOwner:           owner,
		Repos:                 req.Repos,
		WorkspacePath:         "/workspace",
		BridgePort:            cfg.Agent.BridgePort,
		NoVNCHTTPPort:         provision.DefaultNoVNCHTTPPort,
		NoVNCHTTPSPort:        provision.DefaultNoVNCHTTPSPort,
		CertbotEmail:          "admin@orchael.ai",
		PATSecretPath:         cfg.GitHub.PATSecret,
		AWSRegion:             cfg.AWS.Region,
		Environment:           env,
		PackagesPreInstalled:  amiID != "",
	}
	var renderErr error
	userData, renderErr = provision.RenderCloudInit(bootCfg)
	if renderErr != nil {
		return fmt.Errorf("render cloud-init: %w", renderErr)
	}

	stackCfg := pulumi.DesktopConfig(
		cfg.AWS.Region, desktopID, owner, zone, cfg.Desktop.InstanceType,
		foundationOutputs[pulumi.OutputSubnetID],
		foundationOutputs[pulumi.OutputSGID],
		foundationOutputs[pulumi.OutputInstanceProfile],
		cfg.Desktop.SSHKeyName,
		cfg.GitHub.PATSecret,
		req.Repos,
		cfg.Agent.BridgePort,
		amiID,
		userData,
	)

	if createPreview {
		fmt.Printf("Desktop ID  : %s\n", desktopID)
		fmt.Printf("Zone        : %s\n", zone)
		fmt.Printf("Hostname    : %s\n", hostname)
		fmt.Printf("Repos       : %v\n", createRepos)
		if amiID != "" {
			fmt.Printf("AMI         : %s (pre-baked, ~1min boot)\n", amiID)
		} else {
			fmt.Printf("AMI         : none (cloud-init bootstrap, ~5-10min boot)\n")
		}
		return runner.Preview(ctx, desktopRef, stackCfg, os.Stderr)
	}

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	mgr := desktop.NewManager(s)

	if err := mgr.CreateRecord(ctx, desktopID, req); err != nil {
		return fmt.Errorf("create fleet record: %w", err)
	}

	// Run pulumi up and update the record with outputs.
	ref := desktopRef
	outputs, err := runner.Up(ctx, ref, stackCfg, os.Stderr)
	if err != nil {
		_ = mgr.RecordFailure(ctx, desktopID, "create", err.Error())
		return fmt.Errorf("pulumi up: %w", err)
	}

	if err := mgr.UpdateFromOutputs(ctx, desktopID, outputs); err != nil {
		return fmt.Errorf("update fleet record: %w", err)
	}
	if err := mgr.MarkReady(ctx, desktopID, "provisioned"); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}

	result := map[string]string{
		"desktop_id": desktopID,
		"hostname":   hostname,
		"novnc_url":  desktop.NoVNCURL(hostname),
		"ssh_target": desktop.SSHTarget(hostname),
		"stack":      desktop.StackName(desktopID),
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID  : %s\n", result["desktop_id"])
	fmt.Printf("Hostname    : %s\n", result["hostname"])
	fmt.Printf("noVNC URL   : %s\n", result["novnc_url"])
	fmt.Printf("SSH target  : %s\n", result["ssh_target"])
	return nil
}

func parseAndValidateRepos(owner string, rawRepos []string) ([]*repo.Repo, string, error) {
	if len(rawRepos) == 0 {
		return nil, owner, nil
	}
	repos, detectedOwner, err := repo.ParseAll(rawRepos)
	if err != nil {
		return nil, "", err
	}
	if !strings.EqualFold(detectedOwner, owner) {
		return nil, "", fmt.Errorf("repository owner %q does not match --github-owner %q", detectedOwner, owner)
	}
	return repos, owner, nil
}

func repoStrings(repos []*repo.Repo) []string {
	if len(repos) == 0 {
		return nil
	}
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.String()
	}
	return out
}
