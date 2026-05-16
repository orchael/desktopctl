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
	if cfg.Desktop.AMIs == nil || len(cfg.Desktop.AMIs) == 0 {
		fmt.Println("No pre-baked AMIs configured.")
		return nil
	}

	// Sort regions for consistent output
	regions := make([]string, 0, len(cfg.Desktop.AMIs))
	for region := range cfg.Desktop.AMIs {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// Print table with aligned columns
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "REGION\tAMI ID\tCREATED")
	fmt.Fprintln(w, "------\t------\t-------")

	for _, region := range regions {
		amiID := cfg.Desktop.AMIs[region]

		// Query AWS for AMI details (creation time)
		awsCfg, err := config.LoadDefaultConfig(context.Background(),
			config.WithRegion(region),
			config.WithSharedConfigProfile(cfg.AWS.Profile),
		)
		if err != nil {
			fmt.Fprintf(w, "%s\t%s\t(error: %v)\n", region, amiID, err)
			continue
		}

		ec2Client := ec2.NewFromConfig(awsCfg)
		result, err := ec2Client.DescribeImages(context.Background(), &ec2.DescribeImagesInput{
			ImageIds: []string{amiID},
		})
		if err != nil || len(result.Images) == 0 {
			fmt.Fprintf(w, "%s\t%s\t(not found in AWS)\n", region, amiID)
			continue
		}

		createdTime := ""
		if result.Images[0].CreationDate != nil {
			createdTime = *result.Images[0].CreationDate
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", region, amiID, createdTime)
	}
	w.Flush()

	return nil
}
