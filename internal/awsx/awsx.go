// Package awsx provides thin wrappers around the AWS SDK v2 for operations
// used by the ai-desktops CLI. All functions accept a context.Context.
package awsx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretsmanagertypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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

// BucketExists reports whether the S3 bucket exists and is accessible with the
// current credentials. It returns false (not an error) when the bucket is
// absent; it returns an error only when the check itself fails unexpectedly.
// A 301 redirect means the bucket exists in a different region than the client's
// configured region — this is treated as "exists" so state buckets in a fixed
// region work regardless of which environment region the caller configures.
func BucketExists(ctx context.Context, cfg aws.Config, bucket string) (bool, error) {
	c := s3.NewFromConfig(cfg)
	_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err == nil {
		return true, nil
	}
	var notFound *s3types.NotFound
	var noSuchBucket *s3types.NoSuchBucket
	if errors.As(err, &notFound) || errors.As(err, &noSuchBucket) {
		return false, nil
	}
	// A 301 redirect means the bucket exists but was created in a different
	// region than the one the client is configured for.
	var httpErr *smithyhttp.ResponseError
	if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 301 {
		return true, nil
	}
	return false, fmt.Errorf("check bucket %s: %w", bucket, err)
}

// EnsureBucket creates the S3 bucket if it does not already exist, then
// configures versioning, SSE, and public-access-block.
func EnsureBucket(ctx context.Context, cfg aws.Config, bucket, region string) error {
	c := s3.NewFromConfig(cfg)

	_, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
	if err != nil {
		var notFound *s3types.NotFound
		var noSuchBucket *s3types.NoSuchBucket
		var httpErr *smithyhttp.ResponseError
		if errors.As(err, &httpErr) && httpErr.HTTPStatusCode() == 301 {
			// Bucket exists in a different region — nothing to create.
			return nil
		}
		if !errors.As(err, &notFound) && !errors.As(err, &noSuchBucket) {
			return fmt.Errorf("check bucket %s: %w", bucket, err)
		}
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

	// Merge desired tags into the existing tag set so we don't overwrite tags
	// applied by other tools (e.g. cost allocation tags set by Pulumi or AWS).
	desiredTags := map[string]string{
		"managed-by":  "ai-desktops",
		"ai-desktops": "true",
	}
	existing, err := c.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	var mergedTags []s3types.Tag
	if err != nil {
		// NoSuchTagSet is normal for a freshly created bucket — start from empty.
		var apiErr interface{ ErrorCode() string }
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchTagSet" {
			return fmt.Errorf("get tags for bucket %s: %w", bucket, err)
		}
	} else {
		for _, t := range existing.TagSet {
			if _, owned := desiredTags[aws.ToString(t.Key)]; !owned {
				mergedTags = append(mergedTags, t)
			}
		}
	}
	for k, v := range desiredTags {
		k, v := k, v
		mergedTags = append(mergedTags, s3types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	if _, err := c.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket:  aws.String(bucket),
		Tagging: &s3types.Tagging{TagSet: mergedTags},
	}); err != nil {
		return fmt.Errorf("tag bucket %s: %w", bucket, err)
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

	var notFound *dynamodbtypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe DynamoDB table %s: %w", tableName, err)
	}

	_, err = c.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(tableName),
		AttributeDefinitions: []dynamodbtypes.AttributeDefinition{{
			AttributeName: aws.String("desktop_id"),
			AttributeType: dynamodbtypes.ScalarAttributeTypeS,
		}},
		KeySchema: []dynamodbtypes.KeySchemaElement{{
			AttributeName: aws.String("desktop_id"),
			KeyType:       dynamodbtypes.KeyTypeHash,
		}},
		BillingMode: dynamodbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		return fmt.Errorf("create DynamoDB table %s: %w", tableName, err)
	}
	return nil
}

// --- EC2 ---

// AMIInfo holds basic descriptive metadata for an EC2 AMI.
type AMIInfo struct {
	ID          string
	Name        string
	Description string
	CreatedAt   string
}

// DescribeAMI returns metadata for the given AMI ID.
func DescribeAMI(ctx context.Context, cfg aws.Config, amiID string) (*AMIInfo, error) {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		ImageIds: []string{amiID},
	})
	if err != nil {
		return nil, fmt.Errorf("describe AMI %s: %w", amiID, err)
	}
	if len(out.Images) == 0 {
		return nil, fmt.Errorf("AMI %s not found", amiID)
	}
	img := out.Images[0]
	return &AMIInfo{
		ID:          aws.ToString(img.ImageId),
		Name:        aws.ToString(img.Name),
		Description: aws.ToString(img.Description),
		CreatedAt:   aws.ToString(img.CreationDate),
	}, nil
}

// FindLatestAMI returns the most-recent AMI matching the given name pattern and
// owner. namePattern supports the EC2 wildcard syntax (e.g. "my-ami-v1.2.*").
func FindLatestAMI(ctx context.Context, cfg aws.Config, namePattern, owner string) (string, error) {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{owner},
		Filters: []ec2types.Filter{
			{Name: aws.String("name"), Values: []string{namePattern}},
			{Name: aws.String("root-device-type"), Values: []string{"ebs"}},
			{Name: aws.String("virtualization-type"), Values: []string{"hvm"}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe images: %w", err)
	}
	if len(out.Images) == 0 {
		return "", fmt.Errorf("no AMI found matching pattern %q owned by %s", namePattern, owner)
	}
	// Pick most recent by CreationDate.
	best := out.Images[0]
	for _, img := range out.Images[1:] {
		if aws.ToString(img.CreationDate) > aws.ToString(best.CreationDate) {
			best = img
		}
	}
	return aws.ToString(best.ImageId), nil
}

// FindSubnetByTag returns the subnet ID of the first subnet tagged with
// Name=ai-desktops-subnet and environment=<environment>. This locates the
// subnet created by the foundation Pulumi stack so Packer can launch its
// build instance inside the project VPC rather than requiring a default VPC.
// Returns ("", nil) when no matching subnet is found so the caller can decide
// whether to treat that as a fatal error or fall back to AWS defaults.
func FindSubnetByTag(ctx context.Context, cfg aws.Config, environment string) (string, error) {
	c := ec2.NewFromConfig(cfg)
	out, err := c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:Name"), Values: []string{"ai-desktops-subnet"}},
			{Name: aws.String("tag:environment"), Values: []string{environment}},
			{Name: aws.String("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe subnets: %w", err)
	}
	if len(out.Subnets) == 0 {
		return "", nil
	}
	return aws.ToString(out.Subnets[0].SubnetId), nil
}

// StopInstance hibernates the given EC2 instance, saving RAM to the encrypted
// root EBS volume, and waits until it reaches the stopped state (up to 10 minutes).
// The instance must have been launched with HibernationOptions.Configured = true
// and an encrypted root volume, which the desktop Pulumi stack ensures.
func StopInstance(ctx context.Context, cfg aws.Config, instanceID string) error {
	c := ec2.NewFromConfig(cfg)
	_, err := c.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
		Hibernate:   aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("hibernate instance %s: %w", instanceID, err)
	}
	waiter := ec2.NewInstanceStoppedWaiter(c)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 10*time.Minute); err != nil {
		return fmt.Errorf("wait for instance %s to reach stopped state after hibernation request: %w", instanceID, err)
	}
	return nil
}

// StartInstance starts the given EC2 instance and waits until it is running
// (up to 10 minutes).
func StartInstance(ctx context.Context, cfg aws.Config, instanceID string) error {
	c := ec2.NewFromConfig(cfg)
	_, err := c.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("start instance %s: %w", instanceID, err)
	}
	waiter := ec2.NewInstanceRunningWaiter(c)
	if err := waiter.Wait(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 10*time.Minute); err != nil {
		return fmt.Errorf("wait for instance %s to reach running state: %w", instanceID, err)
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

// GetCallerIdentity returns a human-readable identity string for the current
// AWS credentials in the form "account/arn". It is used to verify that
// credentials are valid and functional.
func GetCallerIdentity(ctx context.Context, cfg aws.Config) (string, error) {
	stsClient := sts.NewFromConfig(cfg)
	out, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s (%s)", aws.ToString(out.Account), aws.ToString(out.Arn)), nil
}

// SecretExists reports whether the named secret exists and is accessible in
// AWS Secrets Manager. It returns false (not an error) when the secret is
// absent or not found; it returns an error only for unexpected failures such
// as permission errors.
func SecretExists(ctx context.Context, cfg aws.Config, secretID string) (bool, error) {
	smClient := secretsmanager.NewFromConfig(cfg)
	_, err := smClient.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(secretID),
	})
	if err == nil {
		return true, nil
	}
	var notFound *secretsmanagertypes.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, fmt.Errorf("describe secret %q: %w", secretID, err)
}

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
