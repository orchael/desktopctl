package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func marshaledAllocation(t *testing.T, a *ComputeAllocation) map[string]types.AttributeValue {
	t.Helper()
	item, err := attributevalue.MarshalMap(a)
	if err != nil {
		t.Fatalf("MarshalMap: %v", err)
	}
	return item
}

func TestDynamoPoolStore_CreatePoolMember_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	a := &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable}
	if err := s.CreatePoolMember(context.Background(), a); err != nil {
		t.Fatalf("CreatePoolMember: %v", err)
	}
}

func TestDynamoPoolStore_CreatePoolMember_Duplicate(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	a := &ComputeAllocation{InstanceID: "i-dup", PoolMemberState: PoolStateAvailable}
	err := s.CreatePoolMember(context.Background(), a)
	if !errors.Is(err, ErrPoolMemberExists) {
		t.Fatalf("duplicate create: got %v, want ErrPoolMemberExists", err)
	}
}

func TestDynamoPoolStore_GetPoolMember_Success(t *testing.T) {
	a := &ComputeAllocation{
		InstanceID:      "i-001",
		PoolMemberState: PoolStateAvailable,
		AMIID:           "ami-abc",
	}
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: marshaledAllocation(t, a)}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	got, err := s.GetPoolMember(context.Background(), "i-001")
	if err != nil {
		t.Fatalf("GetPoolMember: %v", err)
	}
	if got.InstanceID != "i-001" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "i-001")
	}
	if got.PoolMemberState != PoolStateAvailable {
		t.Errorf("PoolMemberState = %q, want AVAILABLE", got.PoolMemberState)
	}
}

func TestDynamoPoolStore_GetPoolMember_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	_, err := s.GetPoolMember(context.Background(), "i-missing")
	if !errors.Is(err, ErrPoolMemberNotFound) {
		t.Fatalf("got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestDynamoPoolStore_UpdatePoolMember_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	a := &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateReady}
	if err := s.UpdatePoolMember(context.Background(), a); err != nil {
		t.Fatalf("UpdatePoolMember: %v", err)
	}
}

func TestDynamoPoolStore_UpdatePoolMember_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	a := &ComputeAllocation{InstanceID: "i-missing", PoolMemberState: PoolStateReady}
	err := s.UpdatePoolMember(context.Background(), a)
	if !errors.Is(err, ErrPoolMemberNotFound) {
		t.Fatalf("UpdatePoolMember missing: got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestDynamoPoolStore_DeletePoolMember_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	if err := s.DeletePoolMember(context.Background(), "i-001"); err != nil {
		t.Fatalf("DeletePoolMember: %v", err)
	}
}

func TestDynamoPoolStore_DeletePoolMember_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		deleteFn: func(_ *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	err := s.DeletePoolMember(context.Background(), "i-missing")
	if !errors.Is(err, ErrPoolMemberNotFound) {
		t.Fatalf("DeletePoolMember missing: got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestDynamoPoolStore_ListPoolMembers(t *testing.T) {
	members := []*ComputeAllocation{
		{InstanceID: "i-001", PoolMemberState: PoolStateAvailable},
		{InstanceID: "i-002", PoolMemberState: PoolStateInUse},
	}
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			items := make([]map[string]types.AttributeValue, 0, len(members))
			for _, a := range members {
				items = append(items, marshaledAllocation(t, a))
			}
			return &dynamodb.ScanOutput{Items: items}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	got, err := s.ListPoolMembers(context.Background())
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListPoolMembers returned %d, want 2", len(got))
	}
}

func TestDynamoPoolStore_CountByState(t *testing.T) {
	members := []*ComputeAllocation{
		{InstanceID: "i-001", PoolMemberState: PoolStateAvailable},
		{InstanceID: "i-002", PoolMemberState: PoolStateAvailable},
		{InstanceID: "i-003", PoolMemberState: PoolStateInUse},
	}
	mock := &mockDynamoClient{
		scanFn: func(in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			if in.Select != types.SelectCount {
				t.Errorf("expected Select=COUNT, got %v", in.Select)
			}
			stateVal, ok := in.ExpressionAttributeValues[":state"]
			if !ok {
				t.Error("missing :state in ExpressionAttributeValues")
				return &dynamodb.ScanOutput{}, nil
			}
			wantState := stateVal.(*types.AttributeValueMemberS).Value
			var count int32
			for _, a := range members {
				if string(a.PoolMemberState) == wantState {
					count++
				}
			}
			return &dynamodb.ScanOutput{Count: count}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	n, err := s.CountByState(context.Background(), PoolStateAvailable)
	if err != nil {
		t.Fatalf("CountByState: %v", err)
	}
	if n != 2 {
		t.Errorf("CountByState(AVAILABLE) = %d, want 2", n)
	}
}

func TestDynamoPoolStore_AcquireAvailable_Success(t *testing.T) {
	available := &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable}
	updateCalled := false
	mock := &mockDynamoClient{
		// Simulate DynamoDB FilterExpression: only AVAILABLE members returned.
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
				marshaledAllocation(t, available),
			}}, nil
		},
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			updateCalled = true
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	got, err := s.AcquireAvailable(context.Background(), "d-001")
	if err != nil {
		t.Fatalf("AcquireAvailable: %v", err)
	}
	if got.InstanceID != "i-001" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "i-001")
	}
	if got.PoolMemberState != PoolStateAllocating {
		t.Errorf("PoolMemberState = %q, want ALLOCATING", got.PoolMemberState)
	}
	if got.DesktopID != "d-001" {
		t.Errorf("DesktopID = %q, want %q", got.DesktopID, "d-001")
	}
	if !updateCalled {
		t.Error("UpdateItem should have been called")
	}
}

func TestDynamoPoolStore_AcquireAvailable_EmptyDesktopID(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	_, err := s.AcquireAvailable(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty desktopID, got nil")
	}
}

func TestDynamoPoolStore_AcquireAvailable_EmptyPool(t *testing.T) {
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{}}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	_, err := s.AcquireAvailable(context.Background(), "d-001")
	if !errors.Is(err, ErrNoAvailableCapacity) {
		t.Fatalf("empty pool: got %v, want ErrNoAvailableCapacity", err)
	}
}

func TestDynamoPoolStore_AcquireAvailable_ConditionalRace(t *testing.T) {
	// First member loses the conditional-write race; second member succeeds.
	first := &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable}
	second := &ComputeAllocation{InstanceID: "i-002", PoolMemberState: PoolStateAvailable}
	callCount := 0
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{
				marshaledAllocation(t, first),
				marshaledAllocation(t, second),
			}}, nil
		},
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			callCount++
			if callCount == 1 {
				return nil, conditionalCheckErr()
			}
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}
	s := &DynamoPoolStore{client: mock, tableName: "pool"}
	got, err := s.AcquireAvailable(context.Background(), "d-race")
	if err != nil {
		t.Fatalf("AcquireAvailable after race: %v", err)
	}
	if got.InstanceID != "i-002" {
		t.Errorf("should fall back to second member, got %q", got.InstanceID)
	}
	if callCount != 2 {
		t.Errorf("UpdateItem called %d times, want 2", callCount)
	}
}
