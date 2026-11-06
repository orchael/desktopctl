package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestParkTransitionAndRestoreSpecIsolation(t *testing.T) {
	ctx := context.Background()
	s := NewInMemoryStore()
	d := &Desktop{DesktopID: "d-0123abcd", State: StateReady, RestoreSpec: &RestoreSpec{Version: 1, Config: map[string]string{"volumeSize": "150"}}}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	stale, _ := s.Get(ctx, d.DesktopID)
	if err := s.Transition(ctx, d.DesktopID, StateReady, StateParking); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, stale); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale full-record update: %v", err)
	}
	if err := s.Transition(ctx, d.DesktopID, StateReady, StateTerminating); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale terminate transition: %v", err)
	}
	d.RestoreSpec.Config["volumeSize"] = "200"
	got, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateParking || got.RestoreSpec.Config["volumeSize"] != "150" {
		t.Fatalf("stored record changed: %+v", got)
	}
	got.RestoreSpec.Config["volumeSize"] = "300"
	again, _ := s.Get(ctx, d.DesktopID)
	if again.RestoreSpec.Config["volumeSize"] != "150" {
		t.Fatal("restore spec map escaped store boundary")
	}
}

func TestDynamoUpdateRejectsStaleLifecycleVersion(t *testing.T) {
	d := &Desktop{DesktopID: "d-0123abcd", State: StateReady, StateVersion: 2}
	s := &DynamoStore{client: &mockDynamoClient{putFn: func(input *dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error) {
		if got := aws.ToString(input.ConditionExpression); got != "attribute_exists(desktop_id) AND attribute_not_exists(secret_operation_token) AND state_version = :version" {
			t.Fatalf("missing version fence: %s", got)
		}
		if got := input.ExpressionAttributeValues[":version"].(*types.AttributeValueMemberN).Value; got != "2" {
			t.Fatalf("version = %s", got)
		}
		return nil, &types.ConditionalCheckFailedException{Item: marshaledDesktop(t, &Desktop{DesktopID: d.DesktopID, State: StateParking, StateVersion: 3})}
	}}, tableName: "fleet"}
	if err := s.Update(context.Background(), d); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale update = %v", err)
	}
	if d.StateVersion != 2 {
		t.Fatalf("failed update changed local version to %d", d.StateVersion)
	}
}

func TestDynamoTransitionUsesStateCondition(t *testing.T) {
	called := false
	s := &DynamoStore{client: &mockDynamoClient{updateFn: func(input *dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error) {
		called = true
		if input.ConditionExpression == nil || *input.ConditionExpression != "lifecycle_state = :previous" {
			t.Fatalf("missing state fence: %+v", input)
		}
		return nil, conditionalCheckErr()
	}}, tableName: "fleet"}
	if err := s.Transition(context.Background(), "d-0123abcd", StateReady, StateParking); !errors.Is(err, ErrStateConflict) || !called {
		t.Fatalf("transition conflict = %v, called = %v", err, called)
	}
}
