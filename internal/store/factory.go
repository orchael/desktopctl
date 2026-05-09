package store

import (
	"context"

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

// NewFromContext loads an AWS config and returns the appropriate store.
// If tableName is empty an in-memory store is returned.
func NewFromContext(ctx context.Context, awsCfg aws.Config, tableName string) (Store, error) {
	if tableName == "" {
		return NewInMemoryStore(), nil
	}
	client := dynamodb.NewFromConfig(awsCfg)
	return NewDynamoStore(client, tableName), nil
}
