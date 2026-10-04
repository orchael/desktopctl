package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/spf13/cobra"
)

type identityClient interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

func validateAWSIdentity(ctx context.Context, client identityClient) (string, string, error) {
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", "", fmt.Errorf("AWS STS identity check failed: %w", err)
	}
	if out == nil || aws.ToString(out.Account) == "" || aws.ToString(out.Arn) == "" {
		return "", "", fmt.Errorf("AWS STS returned an incomplete identity")
	}
	return aws.ToString(out.Account), aws.ToString(out.Arn), nil
}

var awsValidateAccountID string

var awsCmd = &cobra.Command{Use: "aws", Short: "Inspect the effective AWS credentials"}

var awsValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Verify credentials with AWS STS GetCallerIdentity",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("load AWS configuration: %w", err)
		}
		account, arn, err := validateAWSIdentity(ctx, sts.NewFromConfig(awsCfg))
		if err != nil {
			return err
		}
		if awsValidateAccountID != "" && account != awsValidateAccountID {
			return fmt.Errorf("AWS account mismatch: expected %s, got %s", awsValidateAccountID, account)
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"account_id": account, "arn": arn, "region": cfg.AWS.Region})
		}
		fmt.Fprintf(os.Stdout, "AWS account: %s\nIdentity: %s\nRegion: %s\n", account, arn, cfg.AWS.Region)
		return nil
	},
}

func init() {
	awsValidateCmd.Flags().StringVar(&awsValidateAccountID, "account-id", "", "expected 12-digit AWS account ID")
	awsCmd.AddCommand(awsValidateCmd)
	rootCmd.AddCommand(awsCmd)
}
