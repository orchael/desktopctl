package cmd

import (
	"fmt"
	"os"

	"github.com/orchael/desktopctl/internal/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile string
	profile string
	region  string
	jsonOut bool
	cfg     *config.Config
)

var rootCmd = &cobra.Command{
	Use:   "desktopctl",
	Short: "Fleet manager for AI coding desktops",
	Long: `desktopctl manages a fleet of remote servers that act as persistent AI coding
desktops. Each desktop is a browser-accessible Linux environment designed for
AI coding agents (Codex, Claude, Gemini) with human operator access for
supervision and debugging.`,
	SilenceUsage: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cfgFile != "" && cmd.Name() != "setup" {
			if _, err := os.Stat(cfgFile); err != nil {
				return fmt.Errorf("explicit config %q: %w", cfgFile, err)
			}
		}
		return nil
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $XDG_CONFIG_HOME/desktopctl/config.yaml, or ~/.config/desktopctl/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "AWS profile")
	rootCmd.PersistentFlags().StringVar(&region, "region", "", "AWS region (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")
}

func initConfig() {
	var err error
	cfg, err = config.LoadOrDefault(cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}
	// Apply flag overrides.
	if profile != "" {
		cfg.AWS.Profile = profile
	}
	if region != "" {
		cfg.SetRegion(region)
	}
}
