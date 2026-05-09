// Package awsx provides thin wrappers around the AWS SDK v2 for operations
// used by the ai-desktops CLI. All functions accept a context.Context.
package awsx

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// LoadConfig loads AWS configuration for the given region and optional profile.
func LoadConfig(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

// --- S3 ---

// EnsureBucket creates the S3 bucket if it does not already exist, then
// configures versioning, SSE, and public-access-block.
func EnsureBucket(ctx context.Context, cfg aws.Config, bucket, region string) error {
	c := s3.NewFromConfig(cfg)

	_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		// Bucket does not exist — create it.
		input := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
		if region != "us-east-1" {
			input.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(region),
			}
		}
		if _, err := c.CreateBucket(ctx, input); err != nil {
			return fmt.Errorf("create bucket %s: %w", bucket, err)
		}
	}

	if _, err := c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		return fmt.Errorf("enable versioning on %s: %w", bucket, err)
	}

	if _, err := c.PutBucketEncryption(ctx, &s3.PutBucketEncryptionInput{
		Bucket: aws.String(bucket),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{{
				ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
					SSEAlgorithm: s3types.ServerSideEncryptionAes256,
				},
			}},
		},
	}); err != nil {
		return fmt.Errorf("enable SSE on %s: %w", bucket, err)
	}

	t := true
	if _, err := c.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       &t,
			BlockPublicPolicy:     &t,
			IgnorePublicAcls:      &t,
			RestrictPublicBuckets: &t,
		},
	}); err != nil {
		return fmt.Errorf("block public access on %s: %w", bucket, err)
	}

	return nil
}

// --- DynamoDB ---

// EnsureTable creates the DynamoDB table if it does not exist.
func EnsureTable(ctx context.Context, cfg aws.Config, tableName string) error {
	c := dynamodb.NewFromConfig(cfg)

	_, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	})
	if err == nil {
		return nil // table already exists
	}

	_, err = c.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []types.AttributeDefinition{{
			AttributeName: aws.String("desktop_id"),
			AttributeType: types.ScalarAttributeTypeS,
		}},
		KeySchema: []types.KeySchemaElement{{
			AttributeName: aws.String("desktop_id"),
			KeyType:       types.KeyTypeHash,
		}},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		return fmt.Errorf("create DynamoDB table %s: %w", tableName, err)
	}
	return nil
}

// --- EC2 ---

// StopInstance stops the given EC2 instance.
func StopInstance(ctx context.Context, cfg aws.Config, instanceID string) error {
	c := ec2.NewFromConfig(cfg)
	_, err := c.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("stop instance %s: %w", instanceID, err)
	}
	return nil
}

// StartInstance starts the given EC2 instance.
func StartInstance(ctx context.Context, cfg aws.Config, instanceID string) error {
	c := ec2.NewFromConfig(cfg)
	_, err := c.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("start instance %s: %w", instanceID, err)
	}
	return nil
}

// InstanceState returns the current state of the EC2 instance.
func InstanceState(ctx context.Context, cfg aws.Config, instanceID string) (string, error) {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return "", fmt.Errorf("describe instance %s: %w", instanceID, err)
	}
	if len(out.Reservations) == 0 || len(out.Reservations[0].Instances) == 0 {
		return "", fmt.Errorf("instance %s not found", instanceID)
	}
	state := out.Reservations[0].Instances[0].State
	if state == nil {
		return "", nil
	}
	return string(state.Name), nil
}

// InstanceRunning reports whether the EC2 instance is in the running state.
func InstanceRunning(ctx context.Context, cfg aws.Config, instanceID string) (bool, error) {
	state, err := InstanceState(ctx, cfg, instanceID)
	if err != nil {
		return false, err
	}
	return state == string(ec2types.InstanceStateNameRunning), nil
}

// --- SSM / Secrets Manager ---

// GetSecret retrieves a secret value from AWS SSM Parameter Store (with
// decryption) or Secrets Manager, trying SSM first.
func GetSecret(ctx context.Context, cfg aws.Config, secretRef string) (string, error) {
	// Try SSM Parameter Store first.
	ssmClient := ssm.NewFromConfig(cfg)
	t := true
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(secretRef),
		WithDecryption: &t,
	})
	if err == nil {
		return aws.ToString(out.Parameter.Value), nil
	}

	// Fall back to Secrets Manager.
	smClient := secretsmanager.NewFromConfig(cfg)
	smOut, err := smClient.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretRef),
	})
	if err != nil {
		return "", fmt.Errorf("get secret %q from SSM and Secrets Manager: %w", secretRef, err)
	}
	return aws.ToString(smOut.SecretString), nil
}
