package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// ErrSecretOperationChanged means the desktop disappeared or another secret
// operation replaced the caller's fencing token before its metadata commit.
var ErrSecretOperationChanged = errors.New("desktop secret operation changed")

// SecretStore optionally extends Store with fenced secret metadata operations.
// Callers must hold the desktop's remote secret-operation flock throughout both
// calls and the intervening rotation. Tokens must be unique per operation: a new
// Begin fences a stale client that lost its SSH connection and remote lock.
type SecretStore interface {
	BeginSecretOperation(ctx context.Context, id, token string) (*Desktop, error)
	CommitSecretOperation(ctx context.Context, id, token string, paths []string) error
}

var (
	_ SecretStore = (*DynamoStore)(nil)
	_ SecretStore = (*InMemoryStore)(nil)
)

func validateSecretOperation(id, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("secret operation token must not be empty")
	}
	if IsWorkspaceRecordID(id) {
		return ErrNotFound
	}
	return nil
}

// BeginSecretOperation atomically fences previous operations and reads the
// authoritative desktop snapshot, without rewriting unrelated metadata.
func (s *DynamoStore) BeginSecretOperation(ctx context.Context, id, token string) (*Desktop, error) {
	if err := validateSecretOperation(id, token); err != nil {
		return nil, err
	}
	out, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression:    aws.String("SET secret_operation_token = :token"),
		ConditionExpression: aws.String("attribute_exists(desktop_id)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":token": &types.AttributeValueMemberS{Value: token},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err != nil {
		var conditionErr *types.ConditionalCheckFailedException
		if errors.As(err, &conditionErr) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("begin desktop secret operation: %w", err)
	}
	var d Desktop
	if err := attributevalue.UnmarshalMap(out.Attributes, &d); err != nil {
		return nil, fmt.Errorf("unmarshal desktop secret operation snapshot: %w", err)
	}
	return &d, nil
}

// CommitSecretOperation updates only the secret paths and timestamp if this
// operation still owns the fence. The token remains until the next Begin.
func (s *DynamoStore) CommitSecretOperation(ctx context.Context, id, token string, paths []string) error {
	if err := validateSecretOperation(id, token); err != nil {
		return err
	}
	values := make([]types.AttributeValue, len(paths))
	for i, path := range paths {
		values[i] = &types.AttributeValueMemberS{Value: path}
	}
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression:    aws.String("SET secrets = :secrets, updated_at = :t"),
		ConditionExpression: aws.String("attribute_exists(desktop_id) AND secret_operation_token = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":secrets": &types.AttributeValueMemberL{Value: values},
			":t":       &types.AttributeValueMemberS{Value: now()},
			":token":   &types.AttributeValueMemberS{Value: token},
		},
	})
	if err != nil {
		var conditionErr *types.ConditionalCheckFailedException
		if errors.As(err, &conditionErr) {
			return ErrSecretOperationChanged
		}
		return fmt.Errorf("commit desktop secret operation: %w", err)
	}
	return nil
}

func (s *InMemoryStore) BeginSecretOperation(_ context.Context, id, token string) (*Desktop, error) {
	if err := validateSecretOperation(id, token); err != nil {
		return nil, err
	}
	d, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	d.SecretOperationToken = token
	return cloneDesktop(d), nil
}

func (s *InMemoryStore) CommitSecretOperation(_ context.Context, id, token string, paths []string) error {
	if err := validateSecretOperation(id, token); err != nil {
		return err
	}
	d, ok := s.records[id]
	if !ok || d.SecretOperationToken != token {
		return ErrSecretOperationChanged
	}
	d.Secrets = slices.Clone(paths)
	d.UpdatedAt = now()
	return nil
}
