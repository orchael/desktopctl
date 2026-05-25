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

	// Determine target region: flag > configured region > us-east-1
	region := strings.TrimSpace(amiRegions)
	if region == "" {
		if cfg.AWS.Region != "" {
			region = cfg.AWS.Region
		} else {
			region = "us-east-1"
		}
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

	// Resolve the source AMI. Priority: --base-ami flag > source_ami in vars file > EC2 lookup.
	baseAMI := amiBaseAMI

	if baseAMI == "" {
		// Check the vars file for an explicit source_ami.
		vars, err := packer.ParseVarsFile(absVarsFile)
		if err != nil {
			return fmt.Errorf("parse vars file: %w", err)
		}
		if v := vars["source_ami"]; v != "" {
			baseAMI = v
			fmt.Fprintf(os.Stderr, "Using source_ami from vars file: %s\n", baseAMI)
		} else {
			// No explicit AMI — look up the latest novnc-desktop elementary AMI.
			novncVersion := vars["novnc_desktop_version"]
			if novncVersion == "" {
				return fmt.Errorf("novnc_desktop_version not set in vars file and --base-ami not provided")
			}
			namePattern := fmt.Sprintf("novnc-desktop-ubuntu-24.04-elementary-%s-*", novncVersion)
			fmt.Fprintf(os.Stderr, "Looking up novnc-desktop AMI: %s\n", namePattern)

			awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
			if err != nil {
				return fmt.Errorf("load AWS config: %w", err)
			}
			baseAMI, err = awsx.FindLatestAMI(ctx, awsCfg, namePattern, novncAMIOwner)
			if err != nil {
				return fmt.Errorf("find novnc-desktop AMI: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Resolved novnc-desktop base AMI: %s\n", baseAMI)
		}
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
