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

// DynamoPoolStore is a DynamoDB-backed implementation of PoolStore.
type DynamoPoolStore struct {
	client    dynamoClientAPI
	tableName string
}

// NewDynamoPoolStore creates a DynamoPoolStore using the given DynamoDB client and table name.
func NewDynamoPoolStore(client *dynamodb.Client, tableName string) *DynamoPoolStore {
	return &DynamoPoolStore{client: client, tableName: tableName}
}

func (s *DynamoPoolStore) CreatePoolMember(ctx context.Context, a *ComputeAllocation) error {
	if a == nil {
		return errors.New("pool member must not be nil")
	}
	if a.InstanceID == "" {
		return errors.New("pool member InstanceID must not be empty")
	}
	ts := now()
	if a.CreatedAt == "" {
		a.CreatedAt = ts
	}
	a.UpdatedAt = ts

	item, err := attributevalue.MarshalMap(a)
	if err != nil {
		return fmt.Errorf("marshal pool member: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(instance_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrPoolMemberExists
		}
		return fmt.Errorf("put pool member: %w", err)
	}
	return nil
}

func (s *DynamoPoolStore) GetPoolMember(ctx context.Context, instanceID string) (*ComputeAllocation, error) {
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"instance_id": &types.AttributeValueMemberS{Value: instanceID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get pool member %q: %w", instanceID, err)
	}
	if out.Item == nil {
		return nil, ErrPoolMemberNotFound
	}
	var a ComputeAllocation
	if err := attributevalue.UnmarshalMap(out.Item, &a); err != nil {
		return nil, fmt.Errorf("unmarshal pool member: %w", err)
	}
	return &a, nil
}

func (s *DynamoPoolStore) ListPoolMembers(ctx context.Context) ([]*ComputeAllocation, error) {
	var result []*ComputeAllocation
	var exclusiveStartKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: exclusiveStartKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan pool table: %w", err)
		}
		for _, item := range out.Items {
			var a ComputeAllocation
			if err := attributevalue.UnmarshalMap(item, &a); err != nil {
				return nil, fmt.Errorf("unmarshal pool member: %w", err)
			}
			result = append(result, &a)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}
	return result, nil
}

// UpdatePoolMember persists changes to an existing pool member. Returns
// ErrPoolMemberNotFound if the instance does not exist in the table.
func (s *DynamoPoolStore) UpdatePoolMember(ctx context.Context, a *ComputeAllocation) error {
	if a == nil {
		return errors.New("pool member must not be nil")
	}
	if a.InstanceID == "" {
		return errors.New("pool member InstanceID must not be empty")
	}
	a.UpdatedAt = now()
	item, err := attributevalue.MarshalMap(a)
	if err != nil {
		return fmt.Errorf("marshal pool member: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_exists(instance_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrPoolMemberNotFound
		}
		return fmt.Errorf("put pool member (update): %w", err)
	}
	return nil
}

// DeletePoolMember removes a pool member. Returns ErrPoolMemberNotFound if
// the instance does not exist in the table.
func (s *DynamoPoolStore) DeletePoolMember(ctx context.Context, instanceID string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"instance_id": &types.AttributeValueMemberS{Value: instanceID},
		},
		ConditionExpression: aws.String("attribute_exists(instance_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrPoolMemberNotFound
		}
		return fmt.Errorf("delete pool member %q: %w", instanceID, err)
	}
	return nil
}

// AcquireAvailable atomically transitions one AVAILABLE member to ALLOCATING
// using a conditional UpdateItem. Scans the table page-by-page and attempts
// the claim immediately per candidate, exiting as soon as one succeeds.
// If another caller wins the race for a candidate, the next AVAILABLE candidate
// is tried. Returns ErrNoAvailableCapacity when no AVAILABLE member can be
// claimed.
func (s *DynamoPoolStore) AcquireAvailable(ctx context.Context, desktopID string) (*ComputeAllocation, error) {
	if desktopID == "" {
		return nil, errors.New("desktopID must not be empty")
	}

	ts := now()
	var exclusiveStartKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: exclusiveStartKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan pool table: %w", err)
		}

		for _, item := range out.Items {
			var a ComputeAllocation
			if err := attributevalue.UnmarshalMap(item, &a); err != nil {
				return nil, fmt.Errorf("unmarshal pool member: %w", err)
			}
			if a.PoolMemberState != PoolStateAvailable {
				continue
			}

			_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName: aws.String(s.tableName),
				Key: map[string]types.AttributeValue{
					"instance_id": &types.AttributeValueMemberS{Value: a.InstanceID},
				},
				ConditionExpression: aws.String("pool_state = :available"),
				UpdateExpression:    aws.String("SET pool_state = :allocating, desktop_id = :did, allocated_at = :ts, updated_at = :ts"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":available":  &types.AttributeValueMemberS{Value: string(PoolStateAvailable)},
					":allocating": &types.AttributeValueMemberS{Value: string(PoolStateAllocating)},
					":did":        &types.AttributeValueMemberS{Value: desktopID},
					":ts":         &types.AttributeValueMemberS{Value: ts},
				},
			})
			if err != nil {
				var cce *types.ConditionalCheckFailedException
				if errors.As(err, &cce) {
					continue
				}
				return nil, fmt.Errorf("acquire pool member %q: %w", a.InstanceID, err)
			}

			acquired := a
			acquired.PoolMemberState = PoolStateAllocating
			acquired.DesktopID = desktopID
			acquired.AllocatedAt = ts
			acquired.UpdatedAt = ts
			return &acquired, nil
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}

	return nil, ErrNoAvailableCapacity
}

func (s *DynamoPoolStore) CountByState(ctx context.Context, state PoolMemberState) (int, error) {
	members, err := s.ListPoolMembers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range members {
		if a.PoolMemberState == state {
			n++
		}
	}
	return n, nil
}
