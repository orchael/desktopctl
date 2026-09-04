package store

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestNew_EmptyTableName(t *testing.T) {
	s := New(aws.Config{}, "")
	if _, ok := s.(*InMemoryStore); !ok {
		t.Error("expected InMemoryStore when tableName is empty")
	}
}

func TestNew_WithTableName(t *testing.T) {
	s := New(aws.Config{}, "my-table")
	if _, ok := s.(*DynamoStore); !ok {
		t.Error("expected DynamoStore when tableName is set")
	}
}

func TestNewWithConfig_EmptyTableName(t *testing.T) {
	s, err := NewWithConfig(aws.Config{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*InMemoryStore); !ok {
		t.Error("expected InMemoryStore when tableName is empty")
	}
}

func TestNewWithConfig_WithTableName(t *testing.T) {
	s, err := NewWithConfig(aws.Config{}, "my-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*DynamoStore); !ok {
		t.Error("expected DynamoStore when tableName is set")
	}
}

func TestNewAMIStore_ReturnsInMemoryAMIStore(t *testing.T) {
	s := NewAMIStore()
	if _, ok := s.(*InMemoryAMIStore); !ok {
		t.Error("expected *InMemoryAMIStore from NewAMIStore()")
	}
}

func TestNewDynamoAMIStoreFn_EmptyTableName_ReturnsInMemoryAMIStore(t *testing.T) {
	s := NewDynamoAMIStoreFn(aws.Config{}, "")
	if _, ok := s.(*InMemoryAMIStore); !ok {
		t.Error("expected *InMemoryAMIStore when tableName is empty")
	}
}

func TestNewDynamoAMIStoreFn_WithTableName_ReturnsDynamoAMIStore(t *testing.T) {
	s := NewDynamoAMIStoreFn(aws.Config{}, "my-table")
	if _, ok := s.(*DynamoAMIStore); !ok {
		t.Error("expected *DynamoAMIStore when tableName is set")
	}
}

func TestNewPoolStore_EmptyTableName_ReturnsInMemoryPoolStore(t *testing.T) {
	s := NewPoolStore(aws.Config{}, "")
	if _, ok := s.(*InMemoryPoolStore); !ok {
		t.Error("expected *InMemoryPoolStore when tableName is empty")
	}
}

func TestNewPoolStore_WithTableName_ReturnsDynamoPoolStore(t *testing.T) {
	s := NewPoolStore(aws.Config{}, "pool-table")
	if _, ok := s.(*DynamoPoolStore); !ok {
		t.Error("expected *DynamoPoolStore when tableName is set")
	}
}
