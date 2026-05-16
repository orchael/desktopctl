package cmd

import (
	"github.com/spf13/cobra"
)

var amiCmd = &cobra.Command{
	Use:   "ami",
	Short: "Manage pre-baked AMIs",
	Long:  "Build and list pre-baked AMIs for faster desktop provisioning.",
	RunE:  func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

func init() {
	rootCmd.AddCommand(amiCmd)
}
