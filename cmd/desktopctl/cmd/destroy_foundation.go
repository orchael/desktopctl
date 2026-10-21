package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/pulumi"
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
security groups, DynamoDB fleet table), then removes the setup-created EC2
key pair and schedules deletion of its GitHub and agent secrets.

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
	var targets setupResources
	if env == cfg.Environment() {
		var err error
		targets, err = setupCleanupTargets(cfg, env)
		if err != nil {
			return err
		}
		for _, skipped := range targets.skipped {
			fmt.Fprintf(os.Stderr, "Skipping setup resource: %s\n", skipped)
		}
	} else {
		fmt.Fprintf(os.Stderr, "Skipping setup key pair and secret cleanup: environment %q differs from configured environment %q.\n", env, cfg.Environment())
	}

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	ref := pulumi.FoundationStackRef(backendURL, env, cfg.AWS.Region, workDir)

	fmt.Fprintf(os.Stderr, "Destroying foundation stack %s...\n", ref.FullName())

	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile, AWSRegion: cfg.AWS.Region}
	if err := runner.Destroy(ctx, ref, os.Stderr); err != nil {
		return fmt.Errorf("foundation destroy: %w", err)
	}

	if targets.keyPair != "" || len(targets.secrets) != 0 {
		awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("load AWS config for setup cleanup: %w", err)
		}
		if err := cleanupSetupResources(ctx, ec2.NewFromConfig(awsCfg), secretsmanager.NewFromConfig(awsCfg), targets); err != nil {
			return err
		}
	}

	fmt.Fprintln(os.Stderr, "Foundation stack destroyed; configured setup resources cleaned up where applicable.")
	return nil
}
