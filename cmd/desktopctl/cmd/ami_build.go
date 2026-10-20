package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/packer"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/orchael/desktopctl/internal/version"
	"github.com/spf13/cobra"
)

const novncAMIOwner = "819363892004"

var (
	amiRegions   string
	amiVarsFile  string
	amiPackerDir string
	amiBaseAMI   string
	amiSubnetID  string
	amiPublic    bool
)

var amiBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build pre-baked AMIs using Packer",
	Long:  `Build a pre-baked AMI in the first specified region and copy it to the remaining regions. Public builds print the resulting AMI IDs without requiring a desktopctl foundation; an explicit --config also receives active AMI pins. Private builds store AMI history and update config.`,
	RunE:  runAmiBuild,
}

func init() {
	amiBuildCmd.Flags().StringVar(&amiRegions, "regions", "", "comma-separated AWS regions; build in the first and copy to the rest (defaults to configured region)")
	amiBuildCmd.Flags().StringVar(&amiVarsFile, "vars-file", "variables.pkrvars.hcl", "path to Packer variables file (relative to --packer-dir)")
	amiBuildCmd.Flags().StringVar(&amiPackerDir, "packer-dir", "packer", "path to Packer configuration directory")
	amiBuildCmd.Flags().StringVar(&amiBaseAMI, "base-ami", "", "explicit source AMI ID in the first build region (skips noVNC AMI lookup)")
	amiBuildCmd.Flags().StringVar(&amiSubnetID, "subnet-id", "", "subnet for the build instance in the first region (defaults to a foundation subnet or default VPC)")
	amiBuildCmd.Flags().BoolVar(&amiPublic, "public", false, "make the built AMI publicly accessible")
	amiCmd.AddCommand(amiBuildCmd)
}

func runAmiBuild(cmd *cobra.Command, args []string) error {
	if err := requireTools("packer"); err != nil {
		return err
	}
	ctx := context.Background()
	if os.Getenv("GITHUB_NPM_TOKEN") == "" {
		if cfg.GitHub.GitHubSecret == "" {
			return fmt.Errorf("run desktopctl setup or set GITHUB_NPM_TOKEN with read:packages scope before building an AMI")
		}
		awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("load AWS config for GitHub Packages token: %w", err)
		}
		values, err := fetchSecretMap(ctx, awsCfg, cfg.GitHub.GitHubSecret)
		if err != nil {
			return fmt.Errorf("read setup GitHub secret: %w", err)
		}
		token := values["github_token"]
		if token == "" {
			return fmt.Errorf("GitHub secret %q has no github_token; run desktopctl setup or set GITHUB_NPM_TOKEN with read:packages scope", cfg.GitHub.GitHubSecret)
		}
		if err := os.Setenv("GITHUB_NPM_TOKEN", token); err != nil {
			return fmt.Errorf("set Packer token environment: %w", err)
		}
		defer os.Unsetenv("GITHUB_NPM_TOKEN")
		fmt.Fprintf(os.Stderr, "Using GitHub Packages token from setup secret %s\n", cfg.GitHub.GitHubSecret)
	}

	regions, err := parseAMIRegions(amiRegions, cfg.AWS.Region)
	if err != nil {
		return err
	}
	if amiPublic {
		for _, region := range regions {
			if err := checkPublicAMISharing(ctx, region); err != nil {
				return err
			}
		}
	}
	amiStore, err := openBuildAMIStore(ctx, amiPublic)
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

	primaryRegion := regions[0]
	amiIDs, novncVersion, err := buildAMIForRegions(ctx, absPackerDir, absVarsFile, regions)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Built in %s and copied to %d additional region(s)\n", primaryRegion, len(regions)-1)
	// Public publisher builds do not need a desktopctl foundation or AMI store.
	if amiPublic {
		return finishAMIBuild(ctx, regions, amiIDs, novncVersion, true, nil, cfg, cfgFile, os.Stdout)
	}
	// Private builds retain the existing AMI history and active config pins.
	configPath := cfgFile
	if configPath == "" {
		var err error
		configPath, err = config.DefaultPath()
		if err != nil {
			return err
		}
	}

	return finishAMIBuild(ctx, regions, amiIDs, novncVersion, false, amiStore, cfg, configPath, os.Stderr)
}

func openBuildAMIStore(ctx context.Context, public bool) (store.AMIStore, error) {
	if public {
		return nil, nil
	}
	if err := preflightAMIStore(ctx); err != nil {
		return nil, err
	}
	amiStore, err := openAMIStore(ctx)
	if err != nil {
		return nil, fmt.Errorf("open AMI store: %w", err)
	}
	return amiStore, nil
}

func finishAMIBuild(ctx context.Context, regions []string, amiIDs map[string]string, novncVersion string, public bool, amiStore store.AMIStore, buildCfg *config.Config, configPath string, out io.Writer) error {
	if public {
		for _, region := range regions {
			fmt.Fprintf(out, "%s: %s (public)\n", region, amiIDs[region])
		}
		if configPath != "" {
			if buildCfg.Desktop.ActiveAMI == nil {
				buildCfg.Desktop.ActiveAMI = make(map[string]string)
			}
			for _, region := range regions {
				buildCfg.Desktop.ActiveAMI[region] = amiIDs[region]
			}
			if err := buildCfg.Save(configPath); err != nil {
				return fmt.Errorf("save public AMI pins to config: %w", err)
			}
			fmt.Fprintln(out, "Active AMI pins saved to config")
		}
		return nil
	}
	if buildCfg.Desktop.ActiveAMI == nil {
		buildCfg.Desktop.ActiveAMI = make(map[string]string)
	}
	for _, region := range regions {
		amiID := amiIDs[region]
		record := &store.AMIRecord{Region: region, AMIID: amiID, NovncVersion: novncVersion}
		if err := amiStore.SaveAMI(ctx, record); err != nil {
			return fmt.Errorf("save AMI to store: %w", err)
		}
		buildCfg.Desktop.ActiveAMI[region] = amiID
		fmt.Fprintf(out, "  %s: %s (active)\n", region, amiID)
	}
	if err := buildCfg.Save(configPath); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(out, "Config and AMI history saved")
	return nil
}

func checkPublicAMISharing(ctx context.Context, region string) error {
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("load AWS config for AMI public access check in %s: %w", region, err)
	}
	out, err := ec2.NewFromConfig(awsCfg).GetImageBlockPublicAccessState(ctx, &ec2.GetImageBlockPublicAccessStateInput{})
	if err != nil {
		return fmt.Errorf("check AMI block public access in %s before building: %w", region, err)
	}
	return requirePublicAMISharing(region, aws.ToString(out.ImageBlockPublicAccessState), string(out.ManagedBy))
}

func requirePublicAMISharing(region, state, managedBy string) error {
	if state == "unblocked" {
		return nil
	}
	if state == "block-new-sharing" {
		return fmt.Errorf("public AMI build in %s cannot proceed: AMI Block Public Access is enabled (managed by %s); review the regional account setting before publishing", region, managedBy)
	}
	return fmt.Errorf("public AMI build in %s cannot proceed: unexpected AMI Block Public Access state %q (managed by %s)", region, state, managedBy)
}

func preflightAMIStore(ctx context.Context) error {
	if cfg.Fleet.AMITableName == "" {
		return nil
	}
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config for AMI store preflight: %w", err)
	}
	exists, err := awsx.DynamoTableExists(ctx, awsCfg, cfg.Fleet.AMITableName)
	if err != nil {
		return fmt.Errorf("check AMI history table %q in AWS control region %s: %w", cfg.Fleet.AMITableName, cfg.AWS.Region, err)
	}
	if !exists {
		return missingAMITableError(cfg.Fleet.AMITableName, cfg.AWS.Region, cfg.Fleet.Environment)
	}
	return nil
}

func missingAMITableError(tableName, region, environment string) error {
	return fmt.Errorf("AMI history table %q does not exist in AWS control region %s; run `desktopctl init-foundation` for environment %q or set fleet.ami_table_name to the existing AMI history table", tableName, region, environment)
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

func buildAMIForRegions(ctx context.Context, packerDir, varsFile string, regions []string) (map[string]string, string, error) {
	region := regions[0]
	fmt.Fprintf(os.Stderr, "Building AMI in %s; copying to %v\n", region, regions[1:])

	// Resolve source_ami in the primary region; any vars-file value is ignored.
	baseAMI := amiBaseAMI
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return nil, "", fmt.Errorf("load AWS config for %s: %w", region, err)
	}

	if baseAMI == "" {
		namePattern := "novnc-desktop-ubuntu-24.04-elementary-*"
		fmt.Fprintf(os.Stderr, "Looking up novnc-desktop AMI (%s) in %s...\n", namePattern, region)
		baseAMI, err = awsx.FindLatestAMI(ctx, awsCfg, namePattern, novncAMIOwner)
		if err != nil {
			return nil, "", fmt.Errorf("find novnc-desktop AMI in %s: %w", region, err)
		}
		fmt.Fprintf(os.Stderr, "Resolved novnc-desktop base AMI: %s\n", baseAMI)
	} else {
		fmt.Fprintf(os.Stderr, "Using explicit base AMI: %s\n", baseAMI)
	}

	info, err := awsx.DescribeAMI(ctx, awsCfg, baseAMI)
	if err != nil {
		return nil, "", fmt.Errorf("describe base AMI %s: %w", baseAMI, err)
	}
	fmt.Fprintf(os.Stderr, "Base AMI name:        %s\n", info.Name)
	fmt.Fprintf(os.Stderr, "Base AMI description: %s\n", info.Description)
	fmt.Fprintf(os.Stderr, "Base AMI created:     %s\n", info.CreatedAt)
	novncDesktopVersion := novncVersionFromAMIName(info.Name)
	if novncDesktopVersion == "" {
		return nil, "", fmt.Errorf("could not extract novnc-desktop version from base AMI name %q (expected prefix %q)", info.Name, novncAMINamePrefix)
	}
	fmt.Fprintf(os.Stderr, "novnc-desktop version: %s\n", novncDesktopVersion)

	subnetID := amiSubnetID
	if subnetID == "" {
		subnetID, err = awsx.FindSubnetByTag(ctx, awsCfg, cfg.Fleet.Environment)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: subnet lookup failed for %s: %v; Packer will rely on the default VPC\n", region, err)
			subnetID = ""
		}
	}
	if subnetID != "" {
		fmt.Fprintf(os.Stderr, "Packer build subnet: %s\n", subnetID)
	}

	if err := packer.Run(ctx, packerDir, varsFile, region, cfg.AWS.Profile, regions[1:], baseAMI, version.Version, version.DesktopWebVersion, novncDesktopVersion, subnetID, amiPublic, os.Stderr); err != nil {
		return nil, "", fmt.Errorf("packer build in %s: %w", region, err)
	}
	manifest, err := packer.ParseManifest(filepath.Join(packerDir, "manifest.json"))
	if err != nil {
		return nil, "", fmt.Errorf("parse manifest for %s: %w", region, err)
	}
	amiIDs, err := selectedRegionAMIs(packer.RegionAMIs(manifest), regions)
	if err != nil {
		return nil, "", err
	}
	return amiIDs, novncDesktopVersion, nil
}

func selectedRegionAMIs(manifestAMIs map[string]string, regions []string) (map[string]string, error) {
	selected := make(map[string]string, len(regions))
	for _, requestedRegion := range regions {
		if manifestAMIs[requestedRegion] == "" {
			return nil, fmt.Errorf("no AMI found in Packer manifest for %s", requestedRegion)
		}
		selected[requestedRegion] = manifestAMIs[requestedRegion]
	}
	return selected, nil
}
