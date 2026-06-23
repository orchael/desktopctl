package cmd

import (
	"fmt"
	"os"

	"github.com/orchael/ai-desktops/internal/config"
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
	Use:   "ai-desktops",
	Short: "Fleet manager for AI coding desktops",
	Long: `ai-desktops manages a fleet of remote servers that act as persistent AI coding
desktops. Each desktop is a browser-accessible Linux environment designed for
AI coding agents (Codex, Claude, Gemini) with human operator access for
supervision and debugging.`,
	SilenceUsage: true,
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

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.ai-desktops/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&profile, "profile", "", "AWS profile")
	rootCmd.PersistentFlags().StringVar(&region, "region", "", "AWS region (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "output as JSON")
}

func initConfig() {
	var err error
	if cfgFile == "" {
		cfgFile, err = config.DefaultPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error resolving config path: %v\n", err)
			os.Exit(1)
		}
	}
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
		cfg.AWS.Region = region
	}
}
