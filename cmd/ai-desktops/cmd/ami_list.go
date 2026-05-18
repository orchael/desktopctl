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
	ctx := context.Background()

	amiStore, err := openAMIStore(ctx)
	if err != nil {
		return fmt.Errorf("open AMI store: %w", err)
	}

	activeAMIs := make(map[string]string)
	if cfg.Desktop.ActiveAMI != nil {
		activeAMIs = cfg.Desktop.ActiveAMI
	}

	// Collect all regions from the configured region and any active AMI regions.
	regionSet := map[string]bool{cfg.AWS.Region: true}
	for region := range activeAMIs {
		regionSet[region] = true
	}

	var regions []string
	for r := range regionSet {
		regions = append(regions, r)
	}
	sort.Strings(regions)

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "REGION\tAMI ID\tSTATUS\tCREATED")
	fmt.Fprintln(w, "------\t------\t------\t-------")

	for _, region := range regions {
		records, err := amiStore.ListAMIs(ctx, region)
		if err != nil {
			return fmt.Errorf("list AMIs for region %s: %w", region, err)
		}
		if len(records) == 0 {
			continue
		}

		awsCfg, awsErr := config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithSharedConfigProfile(cfg.AWS.Profile),
		)
		var ec2Client *ec2.Client
		if awsErr == nil {
			ec2Client = ec2.NewFromConfig(awsCfg)
		}

		sort.Slice(records, func(i, j int) bool {
			return records[i].CreatedAt < records[j].CreatedAt
		})

		for _, rec := range records {
			status := "inactive"
			if activeAMIs[region] == rec.AMIID {
				status = "active"
			}

			createdTime := rec.CreatedAt
			if ec2Client != nil {
				result, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
					ImageIds: []string{rec.AMIID},
				})
				if err == nil && len(result.Images) > 0 && result.Images[0].CreationDate != nil {
					createdTime = *result.Images[0].CreationDate
				}
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", region, rec.AMIID, status, createdTime)
		}
	}
	w.Flush()

	return nil
}
