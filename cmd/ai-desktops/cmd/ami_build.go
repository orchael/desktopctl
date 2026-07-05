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
	"github.com/orchael/ai-desktops/internal/version"
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
	amiBuildCmd.Flags().StringVar(&amiRegions, "regions", "", "comma-separated AWS regions to build AMIs in (defaults to configured region)")
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
	if os.Getenv("GITHUB_NPM_TOKEN") == "" {
		return fmt.Errorf("GITHUB_NPM_TOKEN is not set; a GitHub token with read:packages scope is required to install @markcallen/desktop-web during the AMI build")
	}
	ctx := context.Background()

	regions, err := parseAMIRegions(amiRegions, cfg.AWS.Region)
	if err != nil {
		return err
	}
	if err := validateAMIRegionSelection(regions, amiBaseAMI); err != nil {
		return err
	}

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

	// Initialize Packer to download required plugins
	fmt.Fprintf(os.Stderr, "Initializing Packer plugins...\n")
	if err := packer.Init(ctx, absPackerDir, os.Stderr); err != nil {
		return fmt.Errorf("packer init failed: %w", err)
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

	for _, region := range regions {
		amiID, novncVersion, err := buildAMIForRegion(ctx, absPackerDir, absVarsFile, region)
		if err != nil {
			return err
		}
		record := &store.AMIRecord{
			Region:       region,
			AMIID:        amiID,
			NovncVersion: novncVersion,
		}

		if err := amiStore.SaveAMI(ctx, record); err != nil {
			return fmt.Errorf("save AMI to store: %w", err)
		}

		// Set as active AMI for this region in config
		cfg.Desktop.ActiveAMI[region] = amiID
		fmt.Fprintf(os.Stderr, "  %s: %s (active)\n", region, amiID)
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

func validateAMIRegionSelection(regions []string, baseAMI string) error {
	if baseAMI != "" && len(regions) > 1 {
		return fmt.Errorf("--base-ami is region-specific and cannot be used with multiple --regions")
	}
	return nil
}

func parseAMIRegions(raw, fallback string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = fallback
	}
	seen := make(map[string]bool)
	var regions []string
	for _, value := range strings.Split(raw, ",") {
		region := strings.TrimSpace(value)
		if region == "" || seen[region] {
			continue
		}
		seen[region] = true
		regions = append(regions, region)
	}
	if len(regions) == 0 {
		return nil, fmt.Errorf("AWS region is required: set aws.region in config or pass --regions")
	}
	return regions, nil
}

const novncAMINamePrefix = "novnc-desktop-ubuntu-24.04-elementary-"

// novncVersionFromAMIName extracts the version stamp from a novnc-desktop AMI
// name (e.g. "novnc-desktop-ubuntu-24.04-elementary-20260525-005909" →
// "20260525-005909"). Returns an empty string when the name doesn't match.
func novncVersionFromAMIName(name string) string {
	if !strings.HasPrefix(name, novncAMINamePrefix) {
		return ""
	}
	return name[len(novncAMINamePrefix):]
}

func buildAMIForRegion(ctx context.Context, packerDir, varsFile, region string) (string, string, error) {
	fmt.Fprintf(os.Stderr, "Building AMI for region: %s\n", region)

	// source_ami in the vars file is intentionally ignored because AMI IDs are region-specific.
	baseAMI := amiBaseAMI
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return "", "", fmt.Errorf("load AWS config for %s: %w", region, err)
	}

	if baseAMI == "" {
		namePattern := "novnc-desktop-ubuntu-24.04-elementary-*"
		fmt.Fprintf(os.Stderr, "Looking up novnc-desktop AMI (%s) in %s...\n", namePattern, region)
		baseAMI, err = awsx.FindLatestAMI(ctx, awsCfg, namePattern, novncAMIOwner)
		if err != nil {
			return "", "", fmt.Errorf("find novnc-desktop AMI in %s: %w", region, err)
		}
		fmt.Fprintf(os.Stderr, "Resolved novnc-desktop base AMI: %s\n", baseAMI)
	} else {
		fmt.Fprintf(os.Stderr, "Using explicit base AMI: %s\n", baseAMI)
	}

	info, err := awsx.DescribeAMI(ctx, awsCfg, baseAMI)
	if err != nil {
		return "", "", fmt.Errorf("describe base AMI %s: %w", baseAMI, err)
	}
	fmt.Fprintf(os.Stderr, "Base AMI name:        %s\n", info.Name)
	fmt.Fprintf(os.Stderr, "Base AMI description: %s\n", info.Description)
	fmt.Fprintf(os.Stderr, "Base AMI created:     %s\n", info.CreatedAt)
	novncDesktopVersion := novncVersionFromAMIName(info.Name)
	if novncDesktopVersion == "" {
		return "", "", fmt.Errorf("could not extract novnc-desktop version from base AMI name %q (expected prefix %q)", info.Name, novncAMINamePrefix)
	}
	fmt.Fprintf(os.Stderr, "novnc-desktop version: %s\n", novncDesktopVersion)

	if err := packer.Run(ctx, packerDir, varsFile, region, baseAMI, version.Version, version.DesktopWebVersion, novncDesktopVersion, amiPublic, os.Stderr); err != nil {
		return "", "", fmt.Errorf("packer build for %s: %w", region, err)
	}
	manifest, err := packer.ParseManifest(filepath.Join(packerDir, "manifest.json"))
	if err != nil {
		return "", "", fmt.Errorf("parse manifest for %s: %w", region, err)
	}
	amiID := packer.RegionAMIs(manifest)[region]
	if amiID == "" {
		return "", "", fmt.Errorf("no AMI found in manifest for %s", region)
	}
	return amiID, novncDesktopVersion, nil
}
