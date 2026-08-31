package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// mockDynamoClient is a configurable implementation of dynamoClientAPI for tests.
type mockDynamoClient struct {
	putFn    func(*dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)
	getFn    func(*dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
	scanFn   func(*dynamodb.ScanInput) (*dynamodb.ScanOutput, error)
	updateFn func(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
	deleteFn func(*dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error)
}

func (m *mockDynamoClient) PutItem(_ context.Context, params *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if m.putFn != nil {
		return m.putFn(params)
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (m *mockDynamoClient) GetItem(_ context.Context, params *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if m.getFn != nil {
		return m.getFn(params)
	}
	return &dynamodb.GetItemOutput{}, nil
}

func (m *mockDynamoClient) Scan(_ context.Context, params *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	if m.scanFn != nil {
		return m.scanFn(params)
	}
	return &dynamodb.ScanOutput{}, nil
}

func (m *mockDynamoClient) UpdateItem(_ context.Context, params *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	if m.updateFn != nil {
		return m.updateFn(params)
	}
	return &dynamodb.UpdateItemOutput{}, nil
}

func (m *mockDynamoClient) DeleteItem(_ context.Context, params *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	if m.deleteFn != nil {
		return m.deleteFn(params)
	}
	return &dynamodb.DeleteItemOutput{}, nil
}

func marshaledDesktop(t *testing.T, d *Desktop) map[string]types.AttributeValue {
	t.Helper()
	item, err := attributevalue.MarshalMap(d)
	if err != nil {
		t.Fatalf("MarshalMap: %v", err)
	}
	return item
}

// conditionalCheckErr returns an error that errors.As can match to *types.ConditionalCheckFailedException.
func conditionalCheckErr() error {
	return &types.ConditionalCheckFailedException{
		Message: aws.String("The conditional request failed"),
	}
}

// ---- DynamoStore tests ----

func TestDynamoStore_Create_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	d := newDesktop("d-dyn-001")
	if err := s.Create(context.Background(), d); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestDynamoStore_Create_Duplicate(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.Create(context.Background(), newDesktop("d-dup"))
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if !errors.Is(err, err) || !contains(err.Error(), "already exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDynamoStore_CreateWorkspace_AllowsDeletedRecord(t *testing.T) {
	var input *dynamodb.PutItemInput
	mock := &mockDynamoClient{
		putFn: func(params *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			input = params
			return &dynamodb.PutItemOutput{}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.CreateWorkspace(context.Background(), &Workspace{
		WorkspaceName:   "factory-dev",
		WorkspaceMode:   "efs",
		Environment:     "dev",
		GitHubOwner:     "acme",
		EFSFileSystemID: "fs-123",
		MountPath:       "/workspace",
		State:           WorkspaceStateAvailable,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if got := aws.ToString(input.ConditionExpression); !contains(got, "workspace_state = :deleted") {
		t.Fatalf("condition = %q", got)
	}
}

func TestDynamoStore_Create_PutError(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, errors.New("connection refused")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.Create(context.Background(), newDesktop("d-err")); err == nil {
		t.Fatal("expected error from PutItem failure")
	}
}

func TestDynamoStore_Get_Success(t *testing.T) {
	want := &Desktop{
		DesktopID:   "d-dyn-002",
		StackName:   "stack-002",
		GitHubOwner: "acme",
		State:       StateReady,
	}
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: marshaledDesktop(nil, want)}, nil
		},
	}
	// MarshalMap in the helper needs t; use inline approach.
	item, _ := attributevalue.MarshalMap(want)
	mock.getFn = func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
		return &dynamodb.GetItemOutput{Item: item}, nil
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	got, err := s.Get(context.Background(), "d-dyn-002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DesktopID != want.DesktopID {
		t.Errorf("DesktopID: got %q, want %q", got.DesktopID, want.DesktopID)
	}
	if got.State != want.State {
		t.Errorf("State: got %q, want %q", got.State, want.State)
	}
}

func TestDynamoStore_Get_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: nil}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoStore_Get_Error(t *testing.T) {
	mock := &mockDynamoClient{
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return nil, errors.New("network error")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if _, err := s.Get(context.Background(), "d-err"); err == nil {
		t.Fatal("expected error from GetItem failure")
	}
}

func TestDynamoStore_List_Success(t *testing.T) {
	d1 := &Desktop{DesktopID: "d-001", StackName: "s1", State: StateReady}
	d2 := &Desktop{DesktopID: "d-002", StackName: "s2", State: StateCreating}
	item1, _ := attributevalue.MarshalMap(d1)
	item2, _ := attributevalue.MarshalMap(d2)
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{
				Items: []map[string]types.AttributeValue{item1, item2},
				Count: 2,
			}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
}

func TestDynamoStore_List_Empty(t *testing.T) {
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return &dynamodb.ScanOutput{Items: nil, Count: 0}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 items, got %d", len(list))
	}
}

func TestDynamoStore_List_Paginates(t *testing.T) {
	d1 := &Desktop{DesktopID: "d-001", StackName: "s1", State: StateReady}
	d2 := &Desktop{DesktopID: "d-002", StackName: "s2", State: StateTerminated}
	item1, _ := attributevalue.MarshalMap(d1)
	item2, _ := attributevalue.MarshalMap(d2)
	lastKey := map[string]types.AttributeValue{
		"desktop_id": &types.AttributeValueMemberS{Value: "d-001"},
	}
	calls := 0
	mock := &mockDynamoClient{
		scanFn: func(input *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			calls++
			if calls == 1 {
				if input.ExclusiveStartKey != nil {
					t.Fatalf("first page start key: got %#v", input.ExclusiveStartKey)
				}
				return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{item1}, LastEvaluatedKey: lastKey}, nil
			}
			if len(input.ExclusiveStartKey) == 0 {
				t.Fatal("second page missing exclusive start key")
			}
			return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{item2}}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if calls != 2 {
		t.Fatalf("scan calls: got %d, want 2", calls)
	}
	if len(list) != 2 {
		t.Fatalf("items: got %d, want 2", len(list))
	}
}

func TestDynamoStore_List_Error(t *testing.T) {
	mock := &mockDynamoClient{
		scanFn: func(_ *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
			return nil, errors.New("scan failed")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if _, err := s.List(context.Background()); err == nil {
		t.Fatal("expected error from Scan failure")
	}
}

func TestDynamoStore_Update_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	d := newDesktop("d-upd-001")
	d.State = StateReady
	if err := s.Update(context.Background(), d); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func TestDynamoStore_Update_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.Update(context.Background(), newDesktop("d-missing"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoStore_Update_Error(t *testing.T) {
	mock := &mockDynamoClient{
		putFn: func(_ *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
			return nil, errors.New("put failed")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.Update(context.Background(), newDesktop("d-err")); err == nil {
		t.Fatal("expected error from PutItem failure")
	}
}

func TestDynamoStore_Delete_Success(t *testing.T) {
	var input *dynamodb.DeleteItemInput
	mock := &mockDynamoClient{
		deleteFn: func(params *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			input = params
			return &dynamodb.DeleteItemOutput{}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.Delete(context.Background(), "d-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if aws.ToString(input.TableName) != "fleet" {
		t.Errorf("table: got %q", aws.ToString(input.TableName))
	}
	if input.ConditionExpression == nil {
		t.Fatal("expected condition expression")
	}
}

func TestDynamoStore_Delete_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		deleteFn: func(_ *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.Delete(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoStore_Delete_Error(t *testing.T) {
	mock := &mockDynamoClient{
		deleteFn: func(_ *dynamodb.DeleteItemInput) (*dynamodb.DeleteItemOutput, error) {
			return nil, errors.New("delete failed")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.Delete(context.Background(), "d-error"); err == nil {
		t.Fatal("expected error from DeleteItem failure")
	}
}

func TestDynamoStore_MarkTerminated_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.MarkTerminated(context.Background(), "d-term-001"); err != nil {
		t.Fatalf("MarkTerminated: %v", err)
	}
}

func TestDynamoStore_MarkTerminated_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.MarkTerminated(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoStore_MarkTerminated_Error(t *testing.T) {
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, errors.New("update failed")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.MarkTerminated(context.Background(), "d-err"); err == nil {
		t.Fatal("expected error from UpdateItem failure")
	}
}

func TestDynamoStore_RecordFailure_Success(t *testing.T) {
	mock := &mockDynamoClient{}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.RecordFailure(context.Background(), "d-fail-001", "pulumi-up", "timed out"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
}

func TestDynamoStore_RecordFailure_NotFound(t *testing.T) {
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, conditionalCheckErr()
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.RecordFailure(context.Background(), "missing", "init", "not found")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDynamoStore_RecordFailure_Error(t *testing.T) {
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, errors.New("update failed")
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.RecordFailure(context.Background(), "d-err", "phase", "msg"); err == nil {
		t.Fatal("expected error from UpdateItem failure")
	}
}

func TestDynamoStore_DeleteWorkspace_Success(t *testing.T) {
	var input *dynamodb.UpdateItemInput
	mock := &mockDynamoClient{
		updateFn: func(params *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			input = params
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	if err := s.DeleteWorkspace(context.Background(), "dev", "factory-dev"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if got := aws.ToString(input.ConditionExpression); !contains(got, "attribute_not_exists(attached_desktop_id)") {
		t.Fatalf("condition = %q", got)
	}
	if got := aws.ToString(input.UpdateExpression); !contains(got, "workspace_state = :deleted") {
		t.Fatalf("update = %q", got)
	}
}

func TestDynamoStore_DeleteWorkspace_Attached(t *testing.T) {
	item, _ := attributevalue.MarshalMap(&Workspace{
		WorkspaceName:     "factory-dev",
		Environment:       "dev",
		State:             WorkspaceStateAttached,
		AttachedDesktopID: "d-001",
	})
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, conditionalCheckErr()
		},
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.DeleteWorkspace(context.Background(), "dev", "factory-dev")
	if !errors.Is(err, ErrWorkspaceAttached) {
		t.Fatalf("error = %v, want ErrWorkspaceAttached", err)
	}
}

func TestDynamoStore_UpdateDetachedWorkspaceRepos_Success(t *testing.T) {
	var input *dynamodb.UpdateItemInput
	mock := &mockDynamoClient{
		updateFn: func(params *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			input = params
			return &dynamodb.UpdateItemOutput{}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	repos := []string{"github.com/acme/app", "github.com/acme/api"}
	if err := s.UpdateDetachedWorkspaceRepos(context.Background(), "dev", "factory-dev", repos, RepoFingerprint(repos)); err != nil {
		t.Fatalf("UpdateDetachedWorkspaceRepos: %v", err)
	}
	if input == nil {
		t.Fatal("expected UpdateItem")
	}
	if got := aws.ToString(input.ConditionExpression); !contains(got, "attribute_not_exists(attached_desktop_id)") {
		t.Fatalf("condition = %q", got)
	}
	if got := aws.ToString(input.UpdateExpression); !contains(got, "repo_fingerprint") {
		t.Fatalf("update = %q", got)
	}
	if _, ok := input.ExpressionAttributeValues[":repos"].(*types.AttributeValueMemberL); !ok {
		t.Fatalf(":repos attribute = %#v, want list", input.ExpressionAttributeValues[":repos"])
	}
}

func TestDynamoStore_UpdateDetachedWorkspaceRepos_Attached(t *testing.T) {
	item, _ := attributevalue.MarshalMap(&Workspace{
		WorkspaceName:     "factory-dev",
		Environment:       "dev",
		State:             WorkspaceStateAttached,
		AttachedDesktopID: "d-001",
	})
	mock := &mockDynamoClient{
		updateFn: func(_ *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			return nil, conditionalCheckErr()
		},
		getFn: func(_ *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
			return &dynamodb.GetItemOutput{Item: item}, nil
		},
	}
	s := &DynamoStore{client: mock, tableName: "fleet"}
	err := s.UpdateDetachedWorkspaceRepos(context.Background(), "dev", "factory-dev", []string{"github.com/acme/app"}, RepoFingerprint([]string{"github.com/acme/app"}))
	if !errors.Is(err, ErrWorkspaceAttached) {
		t.Fatalf("error = %v, want ErrWorkspaceAttached", err)
	}
}

// ---- InMemoryStore missing branch tests ----

func TestInMemoryStore_Update_NotFound(t *testing.T) {
	s := NewInMemoryStore()
	err := s.Update(context.Background(), newDesktop("d-missing"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInMemoryStore_RecordFailure_NotFound(t *testing.T) {
	s := NewInMemoryStore()
	err := s.RecordFailure(context.Background(), "d-missing", "phase", "msg")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// contains is a helper to avoid importing strings in the test file.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}
