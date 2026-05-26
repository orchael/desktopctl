package backend

import (
	"fmt"
)

// Config holds the parameters needed to bootstrap the S3 Pulumi backend and
// DynamoDB fleet table.
type Config struct {
	Region        string
	BackendBucket string
	FleetTable    string
}

// BackendURL returns the Pulumi S3 DIY backend URL for the given bucket.
func (c *Config) BackendURL() string {
	return fmt.Sprintf("s3://%s", c.BackendBucket)
}

// Validate checks that all required fields are present.
func (c *Config) Validate() error {
	if c.Region == "" {
		return fmt.Errorf("region is required")
	}
	if c.BackendBucket == "" {
		return fmt.Errorf("backend_bucket is required")
	}
	if c.FleetTable == "" {
		return fmt.Errorf("fleet_table is required")
	}
	return nil
}

// TableSchema returns the DynamoDB attribute definitions and key schema for the
// fleet table.
type TableSchema struct {
	TableName string
	PKName    string
	PKType    string
}

// FleetTableSchema returns the schema for the ai-desktops fleet table.
func FleetTableSchema(tableName string) *TableSchema {
	return &TableSchema{
		TableName: tableName,
		PKName:    "desktop_id",
		PKType:    "S",
	}
}

// BucketConfig returns a description of the S3 bucket configuration that will
// be applied during bootstrap.
type BucketConfig struct {
	BucketName        string
	Region            string
	Versioning        bool
	SSEEnabled        bool
	BlockPublicAccess bool
}

// DefaultBucketConfig returns the recommended S3 bucket configuration for the
// Pulumi backend.
func DefaultBucketConfig(bucket, region string) *BucketConfig {
	return &BucketConfig{
		BucketName:        bucket,
		Region:            region,
		Versioning:        true,
		SSEEnabled:        true,
		BlockPublicAccess: true,
	}
}
