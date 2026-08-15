package controlplane

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	appconfig "github.com/orchael/ai-desktops/internal/config"
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
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(l.app.AWS.Region))
	if err != nil {
		return aws.Config{}, fmt.Errorf("load bootstrap AWS config: %w", err)
	}
	if l.runtime.AWSRoleARN == "" {
		return cfg, nil
	}
	stsClient := sts.NewFromConfig(cfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, l.runtime.AWSRoleARN, func(options *stscreds.AssumeRoleOptions) {
		options.ExternalID = &l.runtime.AWSExternalID
		options.RoleSessionName = "ai-desktops-control-plane"
		options.Duration = time.Hour
	})
	cfg.Credentials = aws.NewCredentialsCache(provider)
	return cfg, nil
}
