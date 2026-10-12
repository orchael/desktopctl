package cmd

import (
	"fmt"

	"github.com/orchael/desktopctl/internal/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the desktopctl version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("desktopctl", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
