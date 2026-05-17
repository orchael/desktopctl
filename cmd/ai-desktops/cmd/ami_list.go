package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/orchael/ai-desktops/internal/packer"
	"github.com/spf13/cobra"
)

var amiListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured pre-baked AMIs",
	Long:  "Display a table of region → AMI ID and creation time for configured pre-baked AMIs.",
	RunE:  runAmiList,
}

func init() {
	amiCmd.AddCommand(amiListCmd)
}

func runAmiList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Open AMI store
	amiStore, err := openAMIStore(ctx)
	if err != nil {
		return fmt.Errorf("open AMI store: %w", err)
	}

	// Read Packer manifest to get all historical AMIs
	manifestPath := filepath.Join("packer", "manifest.json")
	manifest, err := packer.ParseManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}

	// Extract all AMIs from manifest
	regionAMIs := packer.RegionAMIs(manifest)
	if len(regionAMIs) == 0 {
		fmt.Println("No pre-baked AMIs found in manifest.")
		return nil
	}

	// Get active AMIs from config
	activeAMIs := make(map[string]string)
	if cfg.Desktop.ActiveAMI != nil {
		activeAMIs = cfg.Desktop.ActiveAMI
	}

	// Print table with aligned columns
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "REGION\tAMI ID\tSTATUS\tIN DB\tCREATED")
	fmt.Fprintln(w, "------\t------\t------\t-----\t-------")

	// Build a set of all regions
	regionSet := make(map[string]bool)
	for _, build := range manifest.Builds {
		if build.ArtifactID == "" {
			continue
		}
		// Parse region from artifact_id (format: region:ami-id or region1:ami-x,region2:ami-y)
		regionMap := packer.RegionAMIs(&packer.Manifest{Builds: []packer.ManifestBuild{build}})
		for region := range regionMap {
			regionSet[region] = true
		}
	}

	var regions []string
	for region := range regionSet {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// For each region, list all historical AMIs
	for _, region := range regions {
		// Get all builds for this region from manifest
		var amiIDsInManifest []string
		for _, build := range manifest.Builds {
			regionMap := packer.RegionAMIs(&packer.Manifest{Builds: []packer.ManifestBuild{build}})
			if amiID, exists := regionMap[region]; exists {
				amiIDsInManifest = append(amiIDsInManifest, amiID)
			}
		}

		// Show each AMI
		for _, amiID := range amiIDsInManifest {
			activeAMI := activeAMIs[region]

			// Check if in database
			_, err := amiStore.GetAMI(ctx, region, amiID)
			inDB := "yes"
			if err != nil {
				inDB = "no"
			}

			status := "inactive"
			if amiID == activeAMI {
				status = "active"
			}

			// Query AWS for creation time
			createdTime := "-"
			awsCfg, err := config.LoadDefaultConfig(ctx,
				config.WithRegion(region),
				config.WithSharedConfigProfile(cfg.AWS.Profile),
			)
			if err == nil {
				ec2Client := ec2.NewFromConfig(awsCfg)
				result, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
					ImageIds: []string{amiID},
				})
				if err == nil && len(result.Images) > 0 && result.Images[0].CreationDate != nil {
					// Parse and format the creation time nicely
					createdTime = *result.Images[0].CreationDate
				}
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", region, amiID, status, inDB, createdTime)
		}
	}
	w.Flush()

	return nil
}
