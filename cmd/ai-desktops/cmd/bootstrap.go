package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/backend"
)

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Create or verify the S3 Pulumi backend and DynamoDB fleet table",
	Long: `bootstrap ensures the S3 bucket for Pulumi state and the DynamoDB fleet
table both exist and are correctly configured. The command is idempotent.`,
	Args: cobra.NoArgs,
	RunE: runBootstrap,
}

func init() {
	rootCmd.AddCommand(bootstrapCmd)
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	bcfg := &backend.Config{
		Region:        cfg.AWS.Region,
		BackendBucket: cfg.Pulumi.BackendBucket,
		FleetTable:    cfg.Fleet.TableName,
	}

	if err := bcfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Ensuring S3 bucket %s ...\n", bcfg.BackendBucket)
	if err := awsx.EnsureBucket(ctx, awsCfg, bcfg.BackendBucket, bcfg.Region); err != nil {
		return fmt.Errorf("S3 bucket: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Ensuring DynamoDB table %s ...\n", bcfg.FleetTable)
	if err := awsx.EnsureTable(ctx, awsCfg, bcfg.FleetTable); err != nil {
		return fmt.Errorf("DynamoDB table: %w", err)
	}

	result := map[string]string{
		"backend_url": bcfg.BackendURL(),
		"fleet_table": bcfg.FleetTable,
		"region":      bcfg.Region,
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Backend URL : %s\n", result["backend_url"])
	fmt.Printf("Fleet table : %s\n", result["fleet_table"])
	fmt.Printf("Region      : %s\n", result["region"])
	fmt.Println("Bootstrap complete.")
	return nil
}
