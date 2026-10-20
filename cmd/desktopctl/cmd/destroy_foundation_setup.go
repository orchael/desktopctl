package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	"github.com/orchael/desktopctl/internal/config"
)

type setupResources struct {
	keyPair string
	secrets []string
}

// Only delete names produced by setup for this environment and region. A custom
// name may belong to another installation and must be handled explicitly.
func setupCleanupTargets(c *config.Config, env string) (setupResources, error) {
	var result setupResources
	if env != c.Environment() {
		return result, fmt.Errorf("setup cleanup requires configured environment %q; got %q", c.Environment(), env)
	}
	if c.AWS.Region == "" || c.GitHub.Owner == "" {
		return result, fmt.Errorf("setup cleanup requires an AWS region and GitHub owner")
	}
	base := "/desktopctl/" + env + "/" + c.AWS.Region + "/" + c.GitHub.Owner
	if c.Desktop.SSHKeyName != "" {
		want := "desktopctl-" + env + "-" + c.AWS.Region + "-" + c.GitHub.Owner
		if c.Desktop.SSHKeyName != want {
			return result, fmt.Errorf("EC2 key pair %q differs from setup name %q; clean it up separately", c.Desktop.SSHKeyName, want)
		}
		result.keyPair = want
	}
	for _, secret := range []struct{ actual, suffix string }{
		{c.GitHub.GitHubSecret, "/github"},
		{c.GitHub.AgentSecret, "/agents"},
	} {
		if secret.actual == "" {
			continue
		}
		want := base + secret.suffix
		if secret.actual != want {
			return setupResources{}, fmt.Errorf("secret %q differs from setup path %q; clean it up separately", secret.actual, want)
		}
		result.secrets = append(result.secrets, want)
	}
	return result, nil
}

type keyPairDeleter interface {
	DeleteKeyPair(context.Context, *ec2.DeleteKeyPairInput, ...func(*ec2.Options)) (*ec2.DeleteKeyPairOutput, error)
}

type secretDeleter interface {
	DescribeSecret(context.Context, *secretsmanager.DescribeSecretInput, ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
	DeleteSecret(context.Context, *secretsmanager.DeleteSecretInput, ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error)
}

func cleanupSetupResources(ctx context.Context, keys keyPairDeleter, secrets secretDeleter, targets setupResources) error {
	if targets.keyPair != "" {
		_, err := keys.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{KeyName: &targets.keyPair})
		if err != nil && !awsErrorCode(err, "InvalidKeyPair.NotFound") {
			return fmt.Errorf("delete EC2 key pair %q: %w", targets.keyPair, err)
		}
	}
	for _, name := range targets.secrets {
		found, err := secrets.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: &name})
		if awsErrorCode(err, "ResourceNotFoundException") {
			continue
		}
		if err != nil {
			return fmt.Errorf("describe setup secret %q: %w", name, err)
		}
		if found.DeletedDate != nil {
			continue
		}
		_, err = secrets.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{SecretId: &name, RecoveryWindowInDays: awsInt64(7)})
		if err != nil {
			return fmt.Errorf("schedule deletion of setup secret %q: %w", name, err)
		}
	}
	return nil
}

func awsErrorCode(err error, code string) bool {
	var apiErr smithy.APIError
	return errors.As(err, &apiErr) && strings.EqualFold(apiErr.ErrorCode(), code)
}

func awsInt64(n int64) *int64 { return &n }
