package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

var amiListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured pre-baked AMIs",
	Long:  "Display a table of region → AMI ID for configured pre-baked AMIs.",
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

	// Print table
	fmt.Println("REGION\t\t\tAMI ID")
	fmt.Println("------\t\t\t------")
	for _, region := range regions {
		amiID := cfg.Desktop.AMIs[region]
		fmt.Printf("%s\t\t\t%s\n", region, amiID)
	}

	return nil
}
