package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoStore is a DynamoDB-backed fleet metadata store.
type DynamoStore struct {
	client    *dynamodb.Client
	tableName string
}

// NewDynamoStore creates a DynamoStore using the given DynamoDB client and table name.
func NewDynamoStore(client *dynamodb.Client, tableName string) *DynamoStore {
	return &DynamoStore{client: client, tableName: tableName}
}

func (s *DynamoStore) Create(ctx context.Context, d *Desktop) error {
	if d.CreatedAt == "" {
		d.CreatedAt = now()
	}
	d.UpdatedAt = now()

	item, err := attributevalue.MarshalMap(d)
	if err != nil {
		return fmt.Errorf("marshal desktop record: %w", err)
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(desktop_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return fmt.Errorf("desktop %q already exists", d.DesktopID)
		}
		return fmt.Errorf("put desktop record: %w", err)
	}
	return nil
}

func (s *DynamoStore) Get(ctx context.Context, id string) (*Desktop, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get desktop %q: %w", id, err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	var d Desktop
	if err := attributevalue.UnmarshalMap(out.Item, &d); err != nil {
		return nil, fmt.Errorf("unmarshal desktop record: %w", err)
	}
	return &d, nil
}

func (s *DynamoStore) List(ctx context.Context) ([]*Desktop, error) {
	out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String(s.tableName),
	})
	if err != nil {
		return nil, fmt.Errorf("scan fleet table: %w", err)
	}

	desktops := make([]*Desktop, 0, len(out.Items))
	for _, item := range out.Items {
		var d Desktop
		if err := attributevalue.UnmarshalMap(item, &d); err != nil {
			return nil, fmt.Errorf("unmarshal desktop record: %w", err)
		}
		desktops = append(desktops, &d)
	}
	return desktops, nil
}

func (s *DynamoStore) Update(ctx context.Context, d *Desktop) error {
	d.UpdatedAt = now()
	item, err := attributevalue.MarshalMap(d)
	if err != nil {
		return fmt.Errorf("marshal desktop record: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_exists(desktop_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrNotFound
		}
		return fmt.Errorf("update desktop record: %w", err)
	}
	return nil
}

func (s *DynamoStore) MarkTerminated(ctx context.Context, id string) error {
	ts := now()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression: aws.String("SET lifecycle_state = :s, updated_at = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: string(StateTerminated)},
			":t": &types.AttributeValueMemberS{Value: ts},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrNotFound
		}
		return fmt.Errorf("mark terminated: %w", err)
	}
	return nil
}

func (s *DynamoStore) RecordFailure(ctx context.Context, id, phase, message string) error {
	ts := now()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
		UpdateExpression: aws.String(
			"SET lifecycle_state = :s, failure_phase = :p, failure_message = :m, updated_at = :t",
		),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s": &types.AttributeValueMemberS{Value: string(StateFailed)},
			":p": &types.AttributeValueMemberS{Value: phase},
			":m": &types.AttributeValueMemberS{Value: message},
			":t": &types.AttributeValueMemberS{Value: ts},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrNotFound
		}
		return fmt.Errorf("record failure: %w", err)
	}
	return nil
}
