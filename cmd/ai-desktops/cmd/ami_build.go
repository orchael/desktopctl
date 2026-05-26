package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/packer"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

const novncAMIOwner = "819363892004"

var (
	amiRegions   string
	amiVarsFile  string
	amiPackerDir string
	amiBaseAMI   string
	amiPublic    bool
)

var amiBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build pre-baked AMIs using Packer",
	Long:  `Build pre-baked AMIs for the specified regions. Packer manifest is parsed and AMI IDs are stored in config.`,
	RunE:  runAmiBuild,
}

func init() {
	amiBuildCmd.Flags().StringVar(&amiRegions, "regions", "", "AWS region to build AMI in (defaults to configured region)")
	amiBuildCmd.Flags().StringVar(&amiVarsFile, "vars-file", "variables.pkrvars.hcl", "path to Packer variables file (relative to --packer-dir)")
	amiBuildCmd.Flags().StringVar(&amiPackerDir, "packer-dir", "packer", "path to Packer configuration directory")
	amiBuildCmd.Flags().StringVar(&amiBaseAMI, "base-ami", "", "explicit source AMI ID (skips auto-lookup from novnc_desktop_version)")
	amiBuildCmd.Flags().BoolVar(&amiPublic, "public", false, "make the built AMI publicly accessible")
	amiCmd.AddCommand(amiBuildCmd)
}

func runAmiBuild(cmd *cobra.Command, args []string) error {
	if err := requireTools("packer"); err != nil {
		return err
	}
	ctx := context.Background()

	// Determine target region: flag > configured region (required).
	region := strings.TrimSpace(amiRegions)
	if region == "" {
		region = cfg.AWS.Region
	}
	if region == "" {
		return fmt.Errorf("AWS region is required: set aws.region in config or pass --regions")
	}

	fmt.Fprintf(os.Stderr, "Building AMI for region: %s\n", region)

	// Run Packer
	absPackerDir, err := filepath.Abs(amiPackerDir)
	if err != nil {
		return fmt.Errorf("resolve packer dir: %w", err)
	}

	absVarsFile := amiVarsFile
	if !filepath.IsAbs(absVarsFile) {
		absVarsFile = filepath.Join(absPackerDir, amiVarsFile)
	}

	// Check that vars file exists
	if _, err := os.Stat(absVarsFile); err != nil {
		return fmt.Errorf("vars file not found: %w", err)
	}

	// Resolve the source AMI. Priority: --base-ami flag > EC2 lookup by novnc_desktop_version + region.
	// source_ami in the vars file is intentionally ignored: it is region-specific and would produce
	// an InvalidAMIID error when building in a different region than it was originally recorded for.
	baseAMI := amiBaseAMI

	if baseAMI == "" {
		namePattern := "novnc-desktop-ubuntu-24.04-elementary-*"
		fmt.Fprintf(os.Stderr, "Looking up novnc-desktop AMI (%s) in %s...\n", namePattern, region)

		awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}
		baseAMI, err = awsx.FindLatestAMI(ctx, awsCfg, namePattern, novncAMIOwner)
		if err != nil {
			return fmt.Errorf("find novnc-desktop AMI: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Resolved novnc-desktop base AMI: %s\n", baseAMI)
	} else {
		fmt.Fprintf(os.Stderr, "Using explicit base AMI: %s\n", baseAMI)
	}

	// Initialize Packer to download required plugins
	fmt.Fprintf(os.Stderr, "Initializing Packer plugins...\n")
	if err := packer.Init(ctx, absPackerDir, os.Stderr); err != nil {
		return fmt.Errorf("packer init failed: %w", err)
	}

	if err := packer.Run(ctx, absPackerDir, absVarsFile, region, baseAMI, amiPublic, os.Stderr); err != nil {
		return fmt.Errorf("packer build failed: %w", err)
	}

	// Parse manifest
	manifestPath := filepath.Join(absPackerDir, "manifest.json")
	manifest, err := packer.ParseManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	// Extract AMI IDs per region
	regionAMIs := packer.RegionAMIs(manifest)
	if len(regionAMIs) == 0 {
		return fmt.Errorf("no AMIs found in manifest")
	}

	// Open AMI store to save history
	amiStore, err := openAMIStore(ctx)
	if err != nil {
		return fmt.Errorf("open AMI store: %w", err)
	}

	// Initialize ActiveAMI in config if needed
	if cfg.Desktop.ActiveAMI == nil {
		cfg.Desktop.ActiveAMI = make(map[string]string)
	}

	// Save AMIs to store and update active_ami in config
	for r, amiID := range regionAMIs {
		record := &store.AMIRecord{
			Region: r,
			AMIID:  amiID,
		}

		if err := amiStore.SaveAMI(ctx, record); err != nil {
			return fmt.Errorf("save AMI to store: %w", err)
		}

		// Set as active AMI for this region in config
		cfg.Desktop.ActiveAMI[r] = amiID
		fmt.Fprintf(os.Stderr, "  %s: %s (active)\n", r, amiID)
	}

	// Save config (only active_ami now, history is in DynamoDB)
	configPath := cfgFile
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("get home dir: %w", err)
		}
		configPath = filepath.Join(home, ".ai-desktops", "config.yaml")
	}

	if err := cfg.Save(configPath); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Config and AMI history saved\n")
	return nil
}
