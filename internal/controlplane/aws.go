package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	appconfig "github.com/orchael/desktopctl/internal/config"
)

// AWSLoader loads the AWS config used by the API server.
type AWSLoader interface {
	Load(ctx context.Context) (aws.Config, error)
}

type assumeRoleLoader struct {
	app     *appconfig.Config
	runtime RuntimeConfig
}

// NewAWSLoader returns an AWS config loader that assumes the DOKS control-plane role.
func NewAWSLoader(app *appconfig.Config, runtime RuntimeConfig) AWSLoader {
	return &assumeRoleLoader{app: app, runtime: runtime}
}

func (l *assumeRoleLoader) Load(ctx context.Context) (aws.Config, error) {
	if l.runtime.Standalone {
		options := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(l.app.AWS.Region)}
		if l.app.AWS.Profile != "" {
			options = append(options, awsconfig.WithSharedConfigProfile(l.app.AWS.Profile))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, options...)
		if err != nil {
			return aws.Config{}, fmt.Errorf("load operator AWS config: %w", err)
		}
		return cfg, nil
	}
	bootstrapCredentials := credentials.NewStaticCredentialsProvider(
		l.runtime.OperatorAccessKeyID,
		l.runtime.OperatorSecretAccessKey,
		"",
	)
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(l.app.AWS.Region),
		awsconfig.WithCredentialsProvider(bootstrapCredentials),
	)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load bootstrap AWS config: %w", err)
	}
	stsClient := sts.NewFromConfig(cfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, l.runtime.OperatorRoleARN, func(options *stscreds.AssumeRoleOptions) {
		options.RoleSessionName = "desktopctl-control-plane"
		options.ExternalID = aws.String(l.runtime.OperatorExternalID)
		options.Duration = time.Hour
	})
	cfg.Credentials = aws.NewCredentialsCache(provider)
	return cfg, nil
}
