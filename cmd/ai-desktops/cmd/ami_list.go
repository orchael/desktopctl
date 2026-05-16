package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
	// Get all regions with AMIs (from either new or legacy config structure)
	regionAMIs := cfg.Desktop.AMIHistory
	if len(regionAMIs) == 0 && len(cfg.Desktop.AMIs) > 0 {
		// Fallback for backward compatibility with legacy config
		regionAMIs = make(map[string][]string)
		for region, amiID := range cfg.Desktop.AMIs {
			regionAMIs[region] = []string{amiID}
		}
	}

	if len(regionAMIs) == 0 {
		fmt.Println("No pre-baked AMIs configured.")
		return nil
	}

	// Sort regions for consistent output
	regions := make([]string, 0, len(regionAMIs))
	for region := range regionAMIs {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// Print table with aligned columns
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "REGION\tAMI ID\tSTATUS\tCREATED")
	fmt.Fprintln(w, "------\t------\t------\t-------")

	for _, region := range regions {
		amiList := regionAMIs[region]
		activeAMI := ""
		if cfg.Desktop.ActiveAMI != nil {
			activeAMI = cfg.Desktop.ActiveAMI[region]
		}

		for _, amiID := range amiList {
			// Query AWS for AMI details (creation time)
			awsCfg, err := config.LoadDefaultConfig(context.Background(),
				config.WithRegion(region),
				config.WithSharedConfigProfile(cfg.AWS.Profile),
			)
			if err != nil {
				status := "inactive"
				if amiID == activeAMI {
					status = "active"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t(error: %v)\n", region, amiID, status, err)
				continue
			}

			ec2Client := ec2.NewFromConfig(awsCfg)
			result, err := ec2Client.DescribeImages(context.Background(), &ec2.DescribeImagesInput{
				ImageIds: []string{amiID},
			})

			createdTime := "(not found in AWS)"
			if err == nil && len(result.Images) > 0 && result.Images[0].CreationDate != nil {
				createdTime = *result.Images[0].CreationDate
			}

			status := "inactive"
			if amiID == activeAMI {
				status = "active"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", region, amiID, status, createdTime)
		}
	}
	w.Flush()

	return nil
}
