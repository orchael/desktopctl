package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/packer"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var (
	amiRegions   string
	amiVarsFile  string
	amiPackerDir string
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
	amiBuildCmd.Flags().BoolVar(&amiPublic, "public", false, "make the built AMI publicly accessible")
	amiCmd.AddCommand(amiBuildCmd)
}

func runAmiBuild(cmd *cobra.Command, args []string) error {
	if err := requireTools("packer"); err != nil {
		return err
	}
	ctx := context.Background()

	regions, err := parseAMIRegions(amiRegions, cfg.AWS.Region)
	if err != nil {
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
		amiID, err := buildAMIForRegion(ctx, absPackerDir, absVarsFile, region)
		if err != nil {
			return err
		}
		record := &store.AMIRecord{
			Region: region,
			AMIID:  amiID,
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

func buildAMIForRegion(ctx context.Context, packerDir, varsFile, region string) (string, error) {
	fmt.Fprintf(os.Stderr, "Building AMI for region: %s\n", region)

	if err := packer.Run(ctx, packerDir, varsFile, region, amiPublic, os.Stderr); err != nil {
		return "", fmt.Errorf("packer build for %s: %w", region, err)
	}
	manifest, err := packer.ParseManifest(filepath.Join(packerDir, "manifest.json"))
	if err != nil {
		return "", fmt.Errorf("parse manifest for %s: %w", region, err)
	}
	amiID := packer.RegionAMIs(manifest)[region]
	if amiID == "" {
		return "", fmt.Errorf("no AMI found in manifest for %s", region)
	}
	return amiID, nil
}
