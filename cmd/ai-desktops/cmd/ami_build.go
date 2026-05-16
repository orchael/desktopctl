package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/packer"
	"github.com/spf13/cobra"
)

var (
	amiRegions    string
	amiVarsFile   string
	amiPackerDir  string
)

var amiBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build pre-baked AMIs using Packer",
	Long:  `Build pre-baked AMIs for the specified regions. Packer manifest is parsed and AMI IDs are stored in config.`,
	RunE:  runAmiBuild,
}

func init() {
	amiBuildCmd.Flags().StringVar(&amiRegions, "regions", "us-east-1", "comma-separated AWS regions to build AMIs for (default: configured region)")
	amiBuildCmd.Flags().StringVar(&amiVarsFile, "vars-file", "packer/variables.pkrvars.hcl", "path to Packer variables file")
	amiBuildCmd.Flags().StringVar(&amiPackerDir, "packer-dir", "packer", "path to Packer configuration directory")
	amiCmd.AddCommand(amiBuildCmd)
}

func runAmiBuild(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Parse region list; if using default "us-east-1", use configured region instead
	regions := strings.Split(strings.TrimSpace(amiRegions), ",")
	if len(regions) == 1 && regions[0] == "us-east-1" && cfg.AWS.Region != "" && cfg.AWS.Region != "us-east-1" {
		regions = []string{cfg.AWS.Region}
	}
	if len(regions) == 0 || regions[0] == "" {
		return fmt.Errorf("no regions specified; use --regions")
	}
	for i := range regions {
		regions[i] = strings.TrimSpace(regions[i])
	}

	fmt.Fprintf(os.Stderr, "Building AMIs for regions: %v\n", regions)

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

	if err := packer.Run(ctx, absPackerDir, amiVarsFile, os.Stderr); err != nil {
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

	// Initialize or merge into config
	if cfg.Desktop.AMIs == nil {
		cfg.Desktop.AMIs = make(map[string]string)
	}
	for region, amiID := range regionAMIs {
		cfg.Desktop.AMIs[region] = amiID
		fmt.Fprintf(os.Stderr, "  %s: %s\n", region, amiID)
	}

	// Save config
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

	fmt.Fprintf(os.Stderr, "Config saved to %s\n", configPath)
	return nil
}
