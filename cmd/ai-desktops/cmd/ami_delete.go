package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/spf13/cobra"
)

var (
	amiDeleteForce bool
)

var amiDeleteCmd = &cobra.Command{
	Use:   "delete <ami-id>",
	Short: "Delete a pre-baked AMI from AWS",
	Long:  "Delete a pre-baked AMI and its snapshot from AWS. Requires confirmation unless --force is used.",
	Args:  cobra.ExactArgs(1),
	RunE:  runAmiDelete,
}

func init() {
	amiDeleteCmd.Flags().BoolVar(&amiDeleteForce, "force", false, "skip confirmation prompt")
	amiCmd.AddCommand(amiDeleteCmd)
}

func runAmiDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	amiID := args[0]

	// Check if this is the active AMI for this region (skipped when --force is set).
	if !amiDeleteForce {
		if cfg.Desktop.ActiveAMI != nil {
			if activeAMI, ok := cfg.Desktop.ActiveAMI[cfg.AWS.Region]; ok && activeAMI == amiID {
				return fmt.Errorf("cannot delete active AMI %s for region %s (set a different active_ami in config first, or use --force)", amiID, cfg.AWS.Region)
			}
		}
	}

	// Load AWS config for the current region
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.AWS.Region),
		config.WithSharedConfigProfile(cfg.AWS.Profile),
	)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	ec2Client := ec2.NewFromConfig(awsCfg)

	// Describe the AMI to get its details (snapshot ID, etc.)
	describeResult, err := ec2Client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{amiID},
	})
	if err != nil {
		return fmt.Errorf("describe AMI: %w", err)
	}

	if len(describeResult.Images) == 0 {
		return fmt.Errorf("AMI %s not found in region %s", amiID, cfg.AWS.Region)
	}

	image := describeResult.Images[0]

	// Collect snapshot IDs for deletion
	var snapshotIDs []string
	for _, bd := range image.BlockDeviceMappings {
		if bd.Ebs != nil && bd.Ebs.SnapshotId != nil {
			snapshotIDs = append(snapshotIDs, *bd.Ebs.SnapshotId)
		}
	}

	// Confirm deletion unless --force
	if !amiDeleteForce {
		fmt.Printf("About to delete:\n")
		fmt.Printf("  AMI ID:    %s\n", amiID)
		fmt.Printf("  Name:      %s\n", *image.Name)
		fmt.Printf("  Created:   %s\n", *image.CreationDate)
		if len(snapshotIDs) > 0 {
			fmt.Printf("  Snapshots: %v\n", snapshotIDs)
		}
		fmt.Printf("\nType 'yes' to confirm, or anything else to cancel: ")

		var response string
		fmt.Scanln(&response)
		if response != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	// Deregister the AMI
	fmt.Fprintf(os.Stderr, "Deregistering AMI %s...\n", amiID)
	_, err = ec2Client.DeregisterImage(ctx, &ec2.DeregisterImageInput{
		ImageId: &amiID,
	})
	if err != nil {
		return fmt.Errorf("deregister AMI: %w", err)
	}

	// Delete associated snapshots
	for _, snapshotID := range snapshotIDs {
		fmt.Fprintf(os.Stderr, "Deleting snapshot %s...\n", snapshotID)
		_, err := ec2Client.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{
			SnapshotId: &snapshotID,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete snapshot %s: %v\n", snapshotID, err)
		}
	}

	// Delete from DynamoDB history
	fmt.Fprintf(os.Stderr, "Removing from AMI history...\n")
	amiStore, err := openAMIStore(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not open AMI store to remove history: %v\n", err)
	} else {
		if err := amiStore.DeleteAMI(ctx, cfg.AWS.Region, amiID); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to delete from AMI history: %v\n", err)
		}
	}

	fmt.Fprintf(os.Stderr, "Successfully deleted AMI %s\n", amiID)
	return nil
}
