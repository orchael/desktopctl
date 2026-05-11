package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	foundationPreview bool
	foundationEnv     string
)

var initFoundationCmd = &cobra.Command{
	Use:   "init-foundation",
	Short: "Deploy shared AWS foundation resources (VPC, IAM, DNS, security groups)",
	Long: `init-foundation deploys the foundation Pulumi stack that owns shared
AWS resources: VPC/subnet selection, IAM instance profile, security group
baseline, and Route53 hosted zone integration.

The foundation stack must be initialized before any desktop can be created.
Run with --preview to perform a dry-run.`,
	Args: cobra.NoArgs,
	RunE: runInitFoundation,
}

func init() {
	initFoundationCmd.Flags().BoolVar(&foundationPreview, "preview", false, "preview changes without applying")
	initFoundationCmd.Flags().StringVar(&foundationEnv, "env", "", "environment (prod|dev), overrides config")
	rootCmd.AddCommand(initFoundationCmd)
}

func runInitFoundation(cmd *cobra.Command, args []string) error {
	env := foundationEnv
	if env == "" {
		env = cfg.Fleet.Environment
	}

	zone, err := cfg.DNSZone()
	if err != nil {
		return err
	}

	if cfg.Pulumi.BackendBucket == "" {
		return fmt.Errorf("pulumi.backend_bucket must be set; run bootstrap first")
	}

	fmt.Printf("Foundation environment : %s\n", env)
	fmt.Printf("DNS zone               : %s\n", zone)
	fmt.Printf("Pulumi backend         : s3://%s\n", cfg.Pulumi.BackendBucket)

	if foundationPreview {
		fmt.Println("[preview mode — no changes applied]")
		fmt.Println("NOTE: Pulumi Automation API required to execute. Run 'pulumi' CLI separately or implement Automation API integration.")
		return nil
	}

	fmt.Println("NOTE: Pulumi Automation API integration is required to apply foundation stack.")
	fmt.Printf("Run: cd infra/pulumi/foundation && pulumi stack select ai-desktops/foundation-%s && pulumi up\n", env)
	return nil
}
