package store

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// New returns a DynamoStore for the given AWS config and table, or an
// InMemoryStore when awsCfg is the zero value (useful in tests and local dev
// without AWS credentials).
func New(awsCfg aws.Config, tableName string) Store {
	if tableName == "" {
		return NewInMemoryStore()
	}
	client := dynamodb.NewFromConfig(awsCfg)
	return NewDynamoStore(client, tableName)
}

// NewWithConfig returns the appropriate store for a pre-loaded AWS config.
// If tableName is empty an in-memory store is returned.
func NewWithConfig(awsCfg aws.Config, tableName string) (Store, error) {
	if tableName == "" {
		return NewInMemoryStore(), nil
	}
	client := dynamodb.NewFromConfig(awsCfg)
	return NewDynamoStore(client, tableName), nil
}

// NewAMIStore returns an in-memory AMI store.
func NewAMIStore() AMIStore {
	return NewInMemoryAMIStore()
}

// NewDynamoAMIStore returns a DynamoDB-backed AMI store.
func NewDynamoAMIStoreFn(awsCfg aws.Config, tableName string) AMIStore {
	if tableName == "" {
		return NewInMemoryAMIStore()
	}
	client := dynamodb.NewFromConfig(awsCfg)
	return NewDynamoAMIStore(client, tableName)
}
