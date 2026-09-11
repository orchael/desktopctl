package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// AUTH-4: acquiring a new operation fences clients that lost their remote lock.
func TestInMemorySecretOperationFencesStaleCommit(t *testing.T) {
	ctx := context.Background()
	s := NewInMemoryStore()
	original := &Desktop{DesktopID: "desktop-1", Secrets: []string{"old"}, Repos: []string{"owner/repo"}, State: StateReady, Hostname: "original"}
	if err := s.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	first, err := s.BeginSecretOperation(ctx, original.DesktopID, "first")
	if err != nil {
		t.Fatal(err)
	}
	if first.SecretOperationToken != "first" || !reflect.DeepEqual(first.Secrets, original.Secrets) {
		t.Fatalf("unexpected initial snapshot: %+v", first)
	}
	first.Secrets[0] = "mutated snapshot"
	first.Repos[0] = "mutated snapshot"
	stored, err := s.Get(ctx, original.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Secrets[0] != "old" || stored.Repos[0] != "owner/repo" {
		t.Fatal("begin snapshot aliases stored slices")
	}
	stored.Hostname = "updated independently"
	if err := s.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	second, err := s.BeginSecretOperation(ctx, original.DesktopID, "second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Hostname != "updated independently" {
		t.Fatal("begin did not return fresh metadata")
	}
	if err := s.CommitSecretOperation(ctx, original.DesktopID, "first", []string{"stale"}); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("stale commit error = %v", err)
	}
	paths := []string{"current"}
	if err := s.CommitSecretOperation(ctx, original.DesktopID, "second", paths); err != nil {
		t.Fatal(err)
	}
	paths[0] = "mutated input"
	got, err := s.Get(ctx, original.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Secrets, []string{"current"}) || got.Hostname != second.Hostname || got.State != second.State || got.CreatedAt != second.CreatedAt || got.SecretOperationToken != "second" {
		t.Fatalf("commit changed unrelated metadata or aliased input: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.UpdatedAt); err != nil {
		t.Fatalf("invalid updated_at: %v", err)
	}
	if err := s.CommitSecretOperation(ctx, original.DesktopID, "second", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, original.DesktopID)
	if len(got.Secrets) != 0 {
		t.Fatalf("empty commit retained paths: %v", got.Secrets)
	}
}

func TestSecretOperationRejectsInvalidTargetsAndTokens(t *testing.T) {
	ctx := context.Background()
	for name, s := range map[string]SecretStore{
		"memory": NewInMemoryStore(),
		"dynamo": &DynamoStore{client: &mockDynamoClient{updateFn: func(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			t.Fatal("invalid request reached DynamoDB")
			return nil, nil
		}}, tableName: "fleet"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, token := range []string{"", " \t"} {
				if _, err := s.BeginSecretOperation(ctx, "desktop-1", token); err == nil {
					t.Fatal("begin accepted blank token")
				}
				if err := s.CommitSecretOperation(ctx, "desktop-1", token, nil); err == nil {
					t.Fatal("commit accepted blank token")
				}
			}
			workspaceID := WorkspaceRecordID("dev", "shared")
			if _, err := s.BeginSecretOperation(ctx, workspaceID, "token"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("workspace begin: %v", err)
			}
			if err := s.CommitSecretOperation(ctx, workspaceID, "token", nil); !errors.Is(err, ErrNotFound) {
				t.Fatalf("workspace commit: %v", err)
			}
		})
	}
	s := NewInMemoryStore()
	if _, err := s.BeginSecretOperation(ctx, "missing", "token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing begin: %v", err)
	}
	if err := s.CommitSecretOperation(ctx, "missing", "token", nil); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("missing commit: %v", err)
	}
}

func TestDynamoBeginSecretOperationReturnsAtomicSnapshot(t *testing.T) {
	d := &Desktop{DesktopID: "desktop-1", Secrets: []string{"fresh"}, Hostname: "new-host", SecretOperationToken: "token"}
	calls := 0
	s := &DynamoStore{tableName: "fleet", client: &mockDynamoClient{updateFn: func(in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
		calls++
		if aws.ToString(in.TableName) != "fleet" || in.Key["desktop_id"].(*types.AttributeValueMemberS).Value != d.DesktopID {
			t.Fatalf("wrong update target: %+v", in)
		}
		if aws.ToString(in.UpdateExpression) != "SET secret_operation_token = :token" || aws.ToString(in.ConditionExpression) != "attribute_exists(desktop_id)" || in.ReturnValues != types.ReturnValueAllNew {
			t.Fatalf("begin must atomically fence and return existing record: %+v", in)
		}
		if len(in.ExpressionAttributeValues) != 1 || in.ExpressionAttributeValues[":token"].(*types.AttributeValueMemberS).Value != "token" {
			t.Fatalf("unexpected begin values: %+v", in.ExpressionAttributeValues)
		}
		return &dynamodb.UpdateItemOutput{Attributes: marshaledDesktop(t, d)}, nil
	}}}
	got, err := s.BeginSecretOperation(context.Background(), d.DesktopID, "token")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(got, d) {
		t.Fatalf("got %+v with %d calls, want %+v", got, calls, d)
	}
}

func TestDynamoCommitSecretOperationTargetsOnlyPathsAndTimestamp(t *testing.T) {
	for _, paths := range [][]string{{"one", "two"}, nil} {
		s := &DynamoStore{tableName: "fleet", client: &mockDynamoClient{updateFn: func(in *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
			if aws.ToString(in.TableName) != "fleet" || in.Key["desktop_id"].(*types.AttributeValueMemberS).Value != "desktop-1" {
				t.Fatalf("wrong update target: %+v", in)
			}
			if aws.ToString(in.UpdateExpression) != "SET secrets = :secrets, updated_at = :t" || aws.ToString(in.ConditionExpression) != "attribute_exists(desktop_id) AND secret_operation_token = :token" {
				t.Fatalf("commit must fence and preserve other fields: %+v", in)
			}
			if len(in.ExpressionAttributeValues) != 3 || in.ExpressionAttributeValues[":token"].(*types.AttributeValueMemberS).Value != "token" {
				t.Fatalf("unexpected commit values: %+v", in.ExpressionAttributeValues)
			}
			list, ok := in.ExpressionAttributeValues[":secrets"].(*types.AttributeValueMemberL)
			if !ok || len(list.Value) != len(paths) {
				t.Fatalf("expected explicit paths list: %+v", in.ExpressionAttributeValues[":secrets"])
			}
			for i, path := range paths {
				if list.Value[i].(*types.AttributeValueMemberS).Value != path {
					t.Fatal("path order changed")
				}
			}
			if _, err := time.Parse(time.RFC3339, in.ExpressionAttributeValues[":t"].(*types.AttributeValueMemberS).Value); err != nil {
				t.Fatal(err)
			}
			return &dynamodb.UpdateItemOutput{}, nil
		}}}
		if err := s.CommitSecretOperation(context.Background(), "desktop-1", "token", paths); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDynamoSecretOperationErrors(t *testing.T) {
	ctx := context.Background()
	backendErr := errors.New("backend failed")
	for _, tc := range []struct {
		name                   string
		backend, begin, commit error
	}{
		{"condition", &types.ConditionalCheckFailedException{}, ErrNotFound, ErrSecretOperationChanged},
		{"backend", backendErr, backendErr, backendErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &DynamoStore{client: &mockDynamoClient{updateFn: func(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) { return nil, tc.backend }}}
			if _, err := s.BeginSecretOperation(ctx, "desktop-1", "token"); !errors.Is(err, tc.begin) {
				t.Fatalf("begin error = %v, want %v", err, tc.begin)
			}
			if err := s.CommitSecretOperation(ctx, "desktop-1", "token", nil); !errors.Is(err, tc.commit) {
				t.Fatalf("commit error = %v, want %v", err, tc.commit)
			}
		})
	}
	s := &DynamoStore{client: &mockDynamoClient{updateFn: func(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
		return &dynamodb.UpdateItemOutput{Attributes: map[string]types.AttributeValue{"desktop_id": &types.AttributeValueMemberL{}}}, nil
	}}}
	if _, err := s.BeginSecretOperation(ctx, "desktop-1", "token"); err == nil {
		t.Fatal("begin accepted malformed response")
	}
}

func TestDesktopSecretOperationTokenStorageAndLegacy(t *testing.T) {
	d := &Desktop{DesktopID: "desktop-1", SecretOperationToken: "private-fence"}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-fence") || strings.Contains(string(b), "secret_operation_token") {
		t.Fatal("operation token exposed in JSON")
	}
	attrs := marshaledDesktop(t, d)
	if attrs["secret_operation_token"].(*types.AttributeValueMemberS).Value != d.SecretOperationToken {
		t.Fatal("token not persisted")
	}
	legacy := marshaledDesktop(t, &Desktop{DesktopID: "legacy"})
	if _, ok := legacy["secret_operation_token"]; ok {
		t.Fatal("empty token should be absent on legacy records")
	}
	var restored Desktop
	if err := attributevalue.UnmarshalMap(legacy, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.SecretOperationToken != "" {
		t.Fatal("legacy token should be empty")
	}
}

func TestInMemoryUpdateCannotRestoreSecretFenceOrPaths(t *testing.T) {
	ctx := context.Background()
	s := NewInMemoryStore()
	d := &Desktop{DesktopID: "desktop-1", Secrets: []string{"old"}}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	legacy, _ := s.Get(ctx, d.DesktopID)
	a, err := s.BeginSecretOperation(ctx, d.DesktopID, "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.BeginSecretOperation(ctx, d.DesktopID, "B")
	if err != nil {
		t.Fatal(err)
	}
	for _, stale := range []*Desktop{legacy, a} {
		stale.State = StateStopped
		if err := s.Update(ctx, stale); !errors.Is(err, ErrSecretOperationChanged) {
			t.Fatalf("stale Update error = %v", err)
		}
	}
	if err := s.CommitSecretOperation(ctx, d.DesktopID, "A", []string{"late A"}); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("late A commit = %v", err)
	}
	if err := s.CommitSecretOperation(ctx, d.DesktopID, "B", []string{"fresh"}); err != nil {
		t.Fatal(err)
	}
	b.State = StateStopped
	if err := s.Update(ctx, b); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("stale paths Update error = %v", err)
	}
	current, _ := s.Get(ctx, d.DesktopID)
	if current.SecretOperationToken != "B" || !reflect.DeepEqual(current.Secrets, []string{"fresh"}) {
		t.Fatalf("stale Update corrupted current metadata: %+v", current)
	}
	current.State = StateStopped
	if err := s.Update(ctx, current); err != nil {
		t.Fatalf("fresh unrelated Update: %v", err)
	}
	mutated, _ := s.Get(ctx, d.DesktopID)
	mutated.Secrets[0] = "uncoordinated mutation"
	if err := s.Update(ctx, mutated); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("in-place path edit bypassed fence: %v", err)
	}
	if err := s.CommitSecretOperation(ctx, d.DesktopID, "B", nil); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Get(ctx, d.DesktopID)
	current.Secrets = []string{}
	if err := s.Update(ctx, current); err != nil {
		t.Fatalf("equivalent empty paths Update: %v", err)
	}
}

func TestDynamoUpdatePreservesSecretFenceAndPaths(t *testing.T) {
	for _, tc := range []struct {
		name, token, condition string
		paths                  []string
	}{
		{"legacy", "", "attribute_exists(desktop_id) AND attribute_not_exists(secret_operation_token)", []string{"legacy change"}},
		{"fenced", "B", "attribute_exists(desktop_id) AND secret_operation_token = :token AND secrets = :secrets", []string{"one", "two"}},
		{"fenced empty", "B", "attribute_exists(desktop_id) AND secret_operation_token = :token AND (attribute_not_exists(secrets) OR secrets = :secrets)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &DynamoStore{client: &mockDynamoClient{putFn: func(in *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
				if aws.ToString(in.ConditionExpression) != tc.condition || in.ReturnValuesOnConditionCheckFailure != types.ReturnValuesOnConditionCheckFailureAllOld {
					t.Fatalf("unsafe update condition: %+v", in)
				}
				if tc.token == "" {
					if len(in.ExpressionAttributeValues) != 0 {
						t.Fatalf("legacy values: %+v", in.ExpressionAttributeValues)
					}
				} else {
					if in.ExpressionAttributeValues[":token"].(*types.AttributeValueMemberS).Value != tc.token {
						t.Fatal("wrong fence")
					}
					list, ok := in.ExpressionAttributeValues[":secrets"].(*types.AttributeValueMemberL)
					if !ok || len(list.Value) != len(tc.paths) {
						t.Fatal("wrong paths condition")
					}
					for i, path := range tc.paths {
						if list.Value[i].(*types.AttributeValueMemberS).Value != path {
							t.Fatal("wrong condition path")
						}
					}
				}
				return &dynamodb.PutItemOutput{}, nil
			}}}
			if err := s.Update(context.Background(), &Desktop{DesktopID: "desktop-1", SecretOperationToken: tc.token, Secrets: tc.paths}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDynamoUpdateRejectsSecretOperationMismatch(t *testing.T) {
	s := &DynamoStore{client: &mockDynamoClient{putFn: func(*dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
		return nil, &types.ConditionalCheckFailedException{Item: marshaledDesktop(t, &Desktop{DesktopID: "desktop-1", SecretOperationToken: "B", Secrets: []string{"new"}})}
	}}}
	if err := s.Update(context.Background(), &Desktop{DesktopID: "desktop-1", SecretOperationToken: "A", Secrets: []string{"old"}}); !errors.Is(err, ErrSecretOperationChanged) {
		t.Fatalf("update conflict: %v", err)
	}
}
