package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the ai-desktops version",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("ai-desktops", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
