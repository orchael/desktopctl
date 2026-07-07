package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func newAMIRecord(region, amiID string) *AMIRecord {
	return &AMIRecord{
		Region:        region,
		AMIID:         amiID,
		CreatedAt:     "2024-01-01T00:00:00Z",
		NovncVersion:  "1.0.0",
		BridgeVersion: "0.1.0",
	}
}

func TestDynamoAMIStore_SaveAMI_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if err := s.SaveAMI(context.Background(), newAMIRecord("us-east-1", "ami-abc")); err != nil {
		t.Fatalf("SaveAMI: %v", err)
	}
}

func TestDynamoAMIStore_SaveAMI_EmptyRegionErrors(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	err := s.SaveAMI(context.Background(), &AMIRecord{Region: "", AMIID: "ami-123"})
	if err == nil {
		t.Error("expected error when Region is empty")
	}
}

func TestDynamoAMIStore_SaveAMI_EmptyAMIIDErrors(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	err := s.SaveAMI(context.Background(), &AMIRecord{Region: "us-east-1", AMIID: ""})
	if err == nil {
		t.Error("expected error when AMIID is empty")
	}
}

func TestDynamoAMIStore_SaveAMI_PutError(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, errors.New("put failed")
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if err := s.SaveAMI(context.Background(), newAMIRecord("us-east-1", "ami-err")); err == nil {
		t.Fatal("expected error from PutItem failure")
	}
}

func TestDynamoAMIStore_GetAMI_Success(t *testing.T) {
	want := newAMIRecord("us-east-1", "ami-get-001")
	item, _ := attributevalue.MarshalMap(want)
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	got, err := s.GetAMI(context.Background(), "us-east-1", "ami-get-001")
	if err != nil {
		t.Fatalf("GetAMI: %v", err)
	}
	if got.Region != want.Region {
		t.Errorf("Region: got %q, want %q", got.Region, want.Region)
	}
	if got.AMIID != want.AMIID {
		t.Errorf("AMIID: got %q, want %q", got.AMIID, want.AMIID)
	}
}

func TestDynamoAMIStore_GetAMI_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: nil}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	_, err := s.GetAMI(context.Background(), "us-east-1", "ami-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoAMIStore_GetAMI_Error(t *testing.T) {
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return nil, errors.New("get failed")
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if _, err := s.GetAMI(context.Background(), "us-east-1", "ami-err"); err == nil {
		t.Fatal("expected error from GetItem failure")
	}
}

func TestDynamoAMIStore_ListAMIs_Success(t *testing.T) {
	r1 := newAMIRecord("us-east-1", "ami-001")
	r2 := newAMIRecord("us-east-1", "ami-002")
	item1, _ := attributevalue.MarshalMap(r1)
	item2, _ := attributevalue.MarshalMap(r2)
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{item1, item2},
				Count: 2,
			}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	list, err := s.ListAMIs(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("ListAMIs: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
}

func TestDynamoAMIStore_ListAMIs_Empty(t *testing.T) {
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: nil, Count: 0}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	list, err := s.ListAMIs(context.Background(), "us-east-1")
	if err != nil {
		t.Fatalf("ListAMIs: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 items, got %d", len(list))
	}
}

func TestDynamoAMIStore_ListAMIs_Error(t *testing.T) {
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return nil, errors.New("scan failed")
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if _, err := s.ListAMIs(context.Background(), "us-east-1"); err == nil {
		t.Fatal("expected error from Scan failure")
	}
}

func TestDynamoAMIStore_DeleteAMI_Success(t *testing.T) {
	record := newAMIRecord("us-east-1", "ami-del-001")
	item, _ := attributevalue.MarshalMap(record)
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if err := s.DeleteAMI(context.Background(), "us-east-1", "ami-del-001"); err != nil {
		t.Fatalf("DeleteAMI: %v", err)
	}
}

func TestDynamoAMIStore_DeleteAMI_Error(t *testing.T) {
	record := newAMIRecord("us-east-1", "ami-err")
	item, _ := attributevalue.MarshalMap(record)
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
		deleteFn: func(_ *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			return nil, errors.New("delete failed")
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if err := s.DeleteAMI(context.Background(), "us-east-1", "ami-err"); err == nil {
		t.Fatal("expected error from DeleteItem failure")
	}
}

func TestDynamoAMIStore_DeleteAMI_RegionMismatch(t *testing.T) {
	// Record stored under us-west-2, but caller requests us-east-1 — should be a no-op.
	record := newAMIRecord("us-west-2", "ami-cross-region")
	item, _ := attributevalue.MarshalMap(record)
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
		deleteFn: func(_ *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			return nil, errors.New("DeleteItem must not be called on region mismatch")
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	if err := s.DeleteAMI(context.Background(), "us-east-1", "ami-cross-region"); err != nil {
		t.Fatalf("DeleteAMI region mismatch should be a no-op, got: %v", err)
	}
}

func TestDynamoAMIStore_GetAMI_RegionMismatch(t *testing.T) {
	// Record stored under us-west-2, but caller requests us-east-1 — should return ErrNotFound.
	record := newAMIRecord("us-west-2", "ami-cross-region")
	item, _ := attributevalue.MarshalMap(record)
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
	}
	s := &DynamoAMIStore{client: mock, tableName: "amis"}
	_, err := s.GetAMI(context.Background(), "us-east-1", "ami-cross-region")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for region mismatch, got %v", err)
	}
}
