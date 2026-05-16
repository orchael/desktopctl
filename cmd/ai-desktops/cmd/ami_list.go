package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/orchael/ai-desktops/internal/store"
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

	// Open AMI store to read history
	amiStore, err := openAMIStore(ctx)
	if err != nil {
		return fmt.Errorf("open AMI store: %w", err)
	}

	// Get all configured regions
	var regions []string
	if cfg.Desktop.ActiveAMI != nil {
		for region := range cfg.Desktop.ActiveAMI {
			regions = append(regions, region)
		}
	}

	if len(regions) == 0 {
		fmt.Println("No pre-baked AMIs configured.")
		return nil
	}

	sort.Strings(regions)

	// Print table with aligned columns
	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "REGION\tAMI ID\tSTATUS\tCREATED")
	fmt.Fprintln(w, "------\t------\t------\t-------")

	for _, region := range regions {
		activeAMI := cfg.Desktop.ActiveAMI[region]

		// List all AMIs for this region from the store
		amiList, err := amiStore.ListAMIs(ctx, region)
		if err != nil {
			fmt.Fprintf(w, "%s\t(error reading history)\t\t%v\n", region, err)
			continue
		}

		// If no history in store, show the configured active AMI
		if len(amiList) == 0 {
			if activeAMI != "" {
				amiList = []*store.AMIRecord{
					{Region: region, AMIID: activeAMI},
				}
			} else {
				fmt.Fprintf(w, "%s\t(none configured)\t\t\n", region)
				continue
			}
		}

		for _, record := range amiList {
			// Query AWS for AMI details (creation time from AWS, not from store)
			awsCfg, err := config.LoadDefaultConfig(ctx,
				config.WithRegion(region),
				config.WithSharedConfigProfile(cfg.AWS.Profile),
			)
			if err != nil {
				status := "inactive"
				if record.AMIID == activeAMI {
					status = "active"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t(error: %v)\n", region, record.AMIID, status, err)
				continue
			}

			ec2Client := ec2.NewFromConfig(awsCfg)
			result, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
				ImageIds: []string{record.AMIID},
			})

			createdTime := "(not found in AWS)"
			if err == nil && len(result.Images) > 0 && result.Images[0].CreationDate != nil {
				createdTime = *result.Images[0].CreationDate
			}

			status := "inactive"
			if record.AMIID == activeAMI {
				status = "active"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", region, record.AMIID, status, createdTime)
		}
	}
	w.Flush()

	return nil
}
