package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/spf13/cobra"
)

var (
	destroyFoundationEnv string
	destroyFoundationYes bool
)

var destroyFoundationCmd = &cobra.Command{
	Use:   "destroy-foundation",
	Short: "Destroy the shared AWS foundation resources for an environment",
	Long: `destroy-foundation tears down the foundation Pulumi stack (VPC, IAM,
security groups, DynamoDB fleet table) for the given environment.

This is primarily used by the integration test suite to clean up test
resources after a run.  Use with care in dev/prod environments.`,
	Args: cobra.NoArgs,
	RunE: runDestroyFoundation,
}

func init() {
	destroyFoundationCmd.Flags().StringVar(&destroyFoundationEnv, "env", "", "environment to destroy (prod|dev|test), overrides config")
	destroyFoundationCmd.Flags().BoolVar(&destroyFoundationYes, "yes", false, "skip confirmation prompt")
	rootCmd.AddCommand(destroyFoundationCmd)
}

func runDestroyFoundation(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := context.Background()

	env := destroyFoundationEnv
	if env == "" {
		env = cfg.Fleet.Environment
	}

	if !destroyFoundationYes {
		fmt.Fprintf(os.Stderr, "This will destroy the foundation stack for environment %q.\n", env)
		fmt.Fprintf(os.Stderr, "Re-run with --yes to confirm.\n")
		return fmt.Errorf("aborted: use --yes to confirm destruction")
	}

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	ref := pulumi.FoundationStackRef(backendURL, env, workDir)

	fmt.Fprintf(os.Stderr, "Destroying foundation stack %s...\n", ref.FullName())

	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}
	if err := runner.Destroy(ctx, ref, os.Stderr); err != nil {
		return fmt.Errorf("foundation destroy: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Foundation stack destroyed.")
	return nil
}
