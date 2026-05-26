package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// AMIRecord represents a pre-baked AMI in the history.
type AMIRecord struct {
	Region        string `dynamodbav:"region"`
	AMIID         string `dynamodbav:"ami_id"`
	CreatedAt     string `dynamodbav:"created_at"`
	NovncVersion  string `dynamodbav:"novnc_version,omitempty"`
	BridgeVersion string `dynamodbav:"bridge_version,omitempty"`
	GoVersion     string `dynamodbav:"go_version,omitempty"`
	UvVersion     string `dynamodbav:"uv_version,omitempty"`
}

// AMIStore is the interface for AMI history operations.
type AMIStore interface {
	SaveAMI(ctx context.Context, record *AMIRecord) error
	ListAMIs(ctx context.Context, region string) ([]*AMIRecord, error)
	GetAMI(ctx context.Context, region, amiID string) (*AMIRecord, error)
	DeleteAMI(ctx context.Context, region, amiID string) error
}

// InMemoryAMIStore is a non-persistent AMIStore implementation.
type InMemoryAMIStore struct {
	// Key: "region|ami-id"
	records map[string]*AMIRecord
}

// NewInMemoryAMIStore returns an initialised in-memory AMI store.
func NewInMemoryAMIStore() *InMemoryAMIStore {
	return &InMemoryAMIStore{records: make(map[string]*AMIRecord)}
}

func (s *InMemoryAMIStore) SaveAMI(ctx context.Context, record *AMIRecord) error {
	if record.Region == "" || record.AMIID == "" {
		return errors.New("region and ami_id are required")
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	key := fmt.Sprintf("%s|%s", record.Region, record.AMIID)
	cp := *record
	s.records[key] = &cp
	return nil
}

func (s *InMemoryAMIStore) ListAMIs(ctx context.Context, region string) ([]*AMIRecord, error) {
	out := make([]*AMIRecord, 0)
	for _, record := range s.records {
		if record.Region == region {
			cp := *record
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *InMemoryAMIStore) GetAMI(ctx context.Context, region, amiID string) (*AMIRecord, error) {
	key := fmt.Sprintf("%s|%s", region, amiID)
	record, ok := s.records[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *record
	return &cp, nil
}

func (s *InMemoryAMIStore) DeleteAMI(ctx context.Context, region, amiID string) error {
	key := fmt.Sprintf("%s|%s", region, amiID)
	delete(s.records, key)
	return nil
}

// DynamoAMIStore is a DynamoDB-backed AMI history store.
type DynamoAMIStore struct {
	client    dynamoClientAPI
	tableName string
}

// NewDynamoAMIStore creates a DynamoAMIStore.
func NewDynamoAMIStore(client *dynamodb.Client, tableName string) *DynamoAMIStore {
	return &DynamoAMIStore{client: client, tableName: tableName}
}

func (s *DynamoAMIStore) SaveAMI(ctx context.Context, record *AMIRecord) error {
	if record.Region == "" || record.AMIID == "" {
		return errors.New("region and ami_id are required")
	}
	if record.CreatedAt == "" {
		record.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("marshal AMI record: %w", err)
	}

	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("put AMI record: %w", err)
	}
	return nil
}

func (s *DynamoAMIStore) ListAMIs(ctx context.Context, region string) ([]*AMIRecord, error) {
	result, err := s.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:                aws.String(s.tableName),
		FilterExpression:         aws.String("#r = :region"),
		ExpressionAttributeNames: map[string]string{"#r": "region"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":region": &types.AttributeValueMemberS{Value: region},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("scan AMI records: %w", err)
	}

	out := make([]*AMIRecord, 0, len(result.Items))
	if err = attributevalue.UnmarshalListOfMaps(result.Items, &out); err != nil {
		return nil, fmt.Errorf("unmarshal AMI records: %w", err)
	}
	return out, nil
}

func (s *DynamoAMIStore) GetAMI(ctx context.Context, region, amiID string) (*AMIRecord, error) {
	result, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"region": &types.AttributeValueMemberS{Value: region},
			"ami_id": &types.AttributeValueMemberS{Value: amiID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get AMI record: %w", err)
	}

	if result.Item == nil {
		return nil, ErrNotFound
	}

	record := &AMIRecord{}
	if err = attributevalue.UnmarshalMap(result.Item, record); err != nil {
		return nil, fmt.Errorf("unmarshal AMI record: %w", err)
	}
	return record, nil
}

func (s *DynamoAMIStore) DeleteAMI(ctx context.Context, region, amiID string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"region": &types.AttributeValueMemberS{Value: region},
			"ami_id": &types.AttributeValueMemberS{Value: amiID},
		},
	})
	if err != nil {
		return fmt.Errorf("delete AMI record: %w", err)
	}
	return nil
}
