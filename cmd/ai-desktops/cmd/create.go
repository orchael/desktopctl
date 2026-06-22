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
	createOwner      string
	createRepos      []string
	createPreview    bool
	createEnv        string
	createAMI        string
	createVolumeSize int
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new persistent AI coding desktop",
	Long: `create provisions a remote EC2 instance configured as an AI coding desktop
with novnc-desktop (Elementary), ai-agent-bridge, and developer tooling.

The --github-owner flag sets the owner boundary for all repositories on this
desktop. When at least one --repo is provided the owner is inferred from the
first repository URL and --github-owner becomes optional. --github-owner is
required only when no --repo flags are given. Mixed-owner repositories are
rejected before any infrastructure is changed.`,
	Args: cobra.NoArgs,
	RunE: runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createOwner, "github-owner", "", "GitHub organization or username (inferred from --repo when omitted)")
	createCmd.Flags().StringArrayVar(&createRepos, "repo", nil, "GitHub repository to clone (repeatable)")
	createCmd.Flags().BoolVar(&createPreview, "preview", false, "preview infrastructure changes without applying")
	createCmd.Flags().StringVar(&createEnv, "env", "", "environment (prod|dev), overrides config")
	createCmd.Flags().StringVar(&createAMI, "ami", "", "override active AMI ID for this region (optional)")
	createCmd.Flags().IntVar(&createVolumeSize, "volume-size", 0, "root EBS volume size in GiB (default 100)")
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := context.Background()

	// Fall back to config file owner when --github-owner not explicitly set.
	if createOwner == "" && cfg.GitHub.Owner != "" {
		createOwner = cfg.GitHub.Owner
	}

	// Validate repo inputs. Owner may be inferred from repos when createOwner is empty.
	repos, owner, err := parseAndValidateRepos(createOwner, createRepos)
	if err != nil {
		return err
	}

	// Owner is required; it must come from --github-owner, config, or be inferred from --repo.
	if owner == "" {
		return fmt.Errorf("--github-owner is required when no --repo is specified (or set github.owner in config)")
	}

	// Verify every repo is reachable before touching any infrastructure.
	for _, r := range repos {
		fmt.Fprintf(os.Stderr, "Checking repository %s ...\n", r)
		if err := r.CheckAccessible(ctx); err != nil {
			return err
		}
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

	// Detect pre-baked AMI for this region (used for both request and cloud-init)
	amiID := ""
	if createAMI != "" {
		amiID = createAMI
	} else if cfg.Desktop.ActiveAMI != nil {
		if ami, ok := cfg.Desktop.ActiveAMI[cfg.AWS.Region]; ok {
			amiID = ami
		}
	}
	if amiID == "" {
		return fmt.Errorf("no AMI configured for region %s: run `ai-desktops ami build` first or supply --ami", cfg.AWS.Region)
	}

	// cfg.GitHub.GitHubSecret defaults to /ai-desktops/github/pat when github.owner
	// is absent from the config file. Re-derive from the effective owner so that
	// --github-owner on the CLI resolves to the correct path.
	gitHubSecret := cfg.GitHub.GitHubSecret
	if cfg.GitHub.Owner == "" {
		gitHubSecret = "/ai-desktops/" + owner + "/github"
	}

	req := &desktop.CreateRequest{
		GitHubOwner:   owner,
		Repos:         repoStrings(repos),
		InstanceType:  cfg.Desktop.InstanceType,
		Zone:          zone,
		OperatorCIDR:  cfg.Desktop.OperatorCIDR,
		SSHKeyPath:    cfg.Desktop.SSHKeyPath,
		GitHubSecret:  gitHubSecret,
		BackendBucket: cfg.Pulumi.BackendBucket,
		Region:        cfg.AWS.Region,
		Profile:       cfg.AWS.Profile,
		AMIID:         amiID,
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

	// Render cloud-init with PackagesPreInstalled set based on whether we have a pre-baked AMI.
	userData := ""
	hostname := desktop.Hostname(desktopID, zone)

	// Read the SSH public key so cloud-init can inject it into the ubuntu user's
	// authorized_keys.  A missing or unreadable key is non-fatal; the desktop
	// will still boot but SSH key-based login won't work.
	sshPubKey := ""
	if cfg.Desktop.SSHKeyPath != "" {
		if pubBytes, err := os.ReadFile(cfg.Desktop.SSHKeyPath + ".pub"); err == nil {
			sshPubKey = strings.TrimSpace(string(pubBytes))
		}
	}

	bootCfg := &provision.BootstrapConfig{
		DesktopID:            desktopID,
		Hostname:             hostname,
		GitHubOwner:          owner,
		Repos:                req.Repos,
		WorkspacePath:        "/workspace",
		BridgePort:           cfg.Agent.BridgePort,
		NoVNCHTTPPort:        provision.DefaultNoVNCHTTPPort,
		NoVNCHTTPSPort:       provision.DefaultNoVNCHTTPSPort,
		CertbotEmail:         "admin@orchael.ai",
		GitHubSecretPath:     gitHubSecret,
		AgentSecretPath:      cfg.GitHub.AgentSecret,
		AWSRegion:            cfg.AWS.Region,
		Environment:          env,
		PackagesPreInstalled: amiID != "",
		SSHPublicKey:         sshPubKey,
		GitUserName:          cfg.GitHub.GitUserName,
		GitUserEmail:         cfg.GitHub.GitUserEmail,
	}
	var renderErr error
	userData, renderErr = provision.RenderCloudInit(bootCfg)
	if renderErr != nil {
		return fmt.Errorf("render cloud-init: %w", renderErr)
	}

	volumeSize := createVolumeSize
	if volumeSize == 0 {
		volumeSize = cfg.Desktop.VolumeSize
	}

	stackCfg := pulumi.DesktopConfig(
		cfg.AWS.Region, desktopID, owner, zone, cfg.Desktop.InstanceType,
		foundationOutputs[pulumi.OutputSubnetID],
		foundationOutputs[pulumi.OutputSGID],
		foundationOutputs[pulumi.OutputInstanceProfile],
		cfg.Desktop.SSHKeyName,
		req.Repos,
		cfg.Agent.BridgePort,
		volumeSize,
		amiID,
		userData,
		env,
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
		"ami_id":     amiID,
		"region":     cfg.AWS.Region,
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID  : %s\n", result["desktop_id"])
	fmt.Printf("Hostname    : %s\n", result["hostname"])
	fmt.Printf("noVNC URL   : %s\n", result["novnc_url"])
	fmt.Printf("SSH target  : %s\n", result["ssh_target"])
	fmt.Printf("AMI ID      : %s\n", result["ami_id"])
	fmt.Printf("Region      : %s\n", result["region"])
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
	if owner == "" {
		// Infer owner from the first repo URL when --github-owner was not provided.
		return repos, detectedOwner, nil
	}
	if !strings.EqualFold(detectedOwner, owner) {
		return nil, "", fmt.Errorf("repository owner %q does not match --github-owner %q", detectedOwner, owner)
	}
	// Return the canonical owner from the repo URL so downstream values
	// (secret paths, config) are consistent regardless of flag casing.
	return repos, detectedOwner, nil
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
