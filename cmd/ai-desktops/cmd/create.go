package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/repo"
)

var (
	createOwner     string
	createRepos     []string
	createPreview   bool
	createEnv       string
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

	if cfg.Pulumi.BackendBucket == "" {
		return fmt.Errorf("pulumi.backend_bucket must be set; run bootstrap first")
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

	if createPreview {
		fmt.Printf("Desktop ID  : %s\n", desktopID)
		fmt.Printf("Zone        : %s\n", zone)
		fmt.Printf("Hostname    : %s\n", desktop.Hostname(desktopID, zone))
		fmt.Printf("Repos       : %v\n", createRepos)
		fmt.Println("[preview mode — no changes applied]")
		return nil
	}

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	mgr := desktop.NewManager(s)

	if err := mgr.CreateRecord(ctx, desktopID, req); err != nil {
		return fmt.Errorf("create fleet record: %w", err)
	}

	hostname := desktop.Hostname(desktopID, zone)
	result := map[string]string{
		"desktop_id": desktopID,
		"hostname":   hostname,
		"novnc_url":  desktop.NoVNCURL(hostname),
		"ssh_target": desktop.SSHTarget(hostname),
		"stack":      desktop.StackName(desktopID),
	}

	fmt.Fprintf(os.Stderr, "Fleet record created. Run Pulumi to provision infrastructure:\n")
	fmt.Fprintf(os.Stderr, "  cd infra/pulumi/desktop && pulumi stack select %s && pulumi up\n",
		desktop.StackName(desktopID))

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID  : %s\n", result["desktop_id"])
	fmt.Printf("Hostname    : %s\n", result["hostname"])
	fmt.Printf("noVNC URL   : %s\n", result["novnc_url"])
	fmt.Printf("SSH target  : %s\n", result["ssh_target"])
	fmt.Printf("Pulumi stack: %s\n", result["stack"])
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
