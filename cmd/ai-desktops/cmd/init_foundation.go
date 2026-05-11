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
	foundationPreview bool
	foundationEnv     string
)

var initFoundationCmd = &cobra.Command{
	Use:   "init-foundation",
	Short: "Deploy shared AWS foundation resources (VPC, IAM, DNS, security groups)",
	Long: `init-foundation deploys the foundation Pulumi stack that owns shared
AWS resources: VPC/subnet, IAM instance profile, security group, Route53
hosted zone integration, and the DynamoDB fleet table.

The foundation stack must be initialized before any desktop can be created.
Run with --preview to describe what would be applied without making changes.`,
	Args: cobra.NoArgs,
	RunE: runInitFoundation,
}

func init() {
	initFoundationCmd.Flags().BoolVar(&foundationPreview, "preview", false, "preview changes without applying")
	initFoundationCmd.Flags().StringVar(&foundationEnv, "env", "", "environment (prod|dev), overrides config")
	rootCmd.AddCommand(initFoundationCmd)
}

func runInitFoundation(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

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

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	workDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	ref := pulumi.FoundationStackRef(backendURL, env, workDir)
	stackCfg := pulumi.FoundationConfig(cfg.AWS.Region, zone, cfg.Fleet.TableName, cfg.Desktop.OperatorCIDR)

	fmt.Fprintf(os.Stderr, "Foundation environment : %s\n", env)
	fmt.Fprintf(os.Stderr, "DNS zone               : %s\n", zone)
	fmt.Fprintf(os.Stderr, "Pulumi backend         : %s\n", backendURL)
	fmt.Fprintf(os.Stderr, "Stack                  : %s\n", ref.FullName())
	fmt.Fprintf(os.Stderr, "Work dir               : %s\n", workDir)

	runner := pulumi.NewRunner()

	if foundationPreview {
		if err := runner.Preview(ctx, ref, stackCfg, os.Stderr); err != nil {
			return fmt.Errorf("foundation preview: %w", err)
		}
		return nil
	}

	outputs, err := runner.Up(ctx, ref, stackCfg, os.Stderr)
	if err != nil {
		return fmt.Errorf("foundation stack: %w", err)
	}

	if err := pulumi.ValidateFoundationOutputs(outputs); err != nil {
		return err
	}

	fmt.Printf("Subnet ID        : %s\n", outputs[pulumi.OutputSubnetID])
	fmt.Printf("Security Group ID: %s\n", outputs[pulumi.OutputSGID])
	fmt.Printf("Instance Profile : %s\n", outputs[pulumi.OutputInstanceProfile])
	fmt.Printf("Zone ID          : %s\n", outputs[pulumi.OutputZoneID])
	fmt.Printf("Fleet Table      : %s\n", outputs[pulumi.OutputFleetTable])
	fmt.Println("Foundation stack applied.")
	return nil
}
