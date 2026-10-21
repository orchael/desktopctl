package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type secretDescriber interface {
	DescribeSecret(context.Context, *secretsmanager.DescribeSecretInput, ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error)
}

// A newly created foundation must not adopt an older operator secret: its
// credentials belong to an IAM user from a previous foundation installation.
// Keep the selected name in Pulumi stack config so later updates retain it.
func selectFreshOperatorSecretName(ctx context.Context, api secretDescriber, environment, region string) (string, error) {
	base := fmt.Sprintf("/desktopctl/%s-%s/control-plane/aws-operator", environment, region)
	for version := 1; version <= 20; version++ {
		name := base
		if version > 1 {
			name = fmt.Sprintf("%s-v%d", base, version)
		}
		_, err := api.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(name)})
		if err == nil {
			continue
		}
		var missing *smtypes.ResourceNotFoundException
		if errors.As(err, &missing) {
			return name, nil
		}
		return "", fmt.Errorf("check operator secret %q: %w", name, err)
	}
	return "", fmt.Errorf("all 20 operator secret names for %s-%s are occupied", environment, region)
}
