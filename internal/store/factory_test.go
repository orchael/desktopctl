package store

import (
	"context"
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

func TestNewFromContext_EmptyTableName(t *testing.T) {
	s, err := NewFromContext(context.Background(), aws.Config{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*InMemoryStore); !ok {
		t.Error("expected InMemoryStore when tableName is empty")
	}
}

func TestNewFromContext_WithTableName(t *testing.T) {
	s, err := NewFromContext(context.Background(), aws.Config{}, "my-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := s.(*DynamoStore); !ok {
		t.Error("expected DynamoStore when tableName is set")
	}
}
