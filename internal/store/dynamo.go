package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoStore is a DynamoDB-backed fleet metadata store.
type DynamoStore struct {
	client    dynamoClientAPI
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
		ConditionExpression: aws.String("attribute_not_exists(desktop_id) OR workspace_state = :deleted"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":deleted": &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
		},
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
	if IsWorkspaceRecordID(id) {
		return nil, ErrNotFound
	}
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.tableName),
		ConsistentRead: aws.Bool(true),
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
	var desktops []*Desktop
	var exclusiveStartKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: exclusiveStartKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan fleet table: %w", err)
		}
		for _, item := range out.Items {
			var d Desktop
			if err := attributevalue.UnmarshalMap(item, &d); err != nil {
				return nil, fmt.Errorf("unmarshal desktop record: %w", err)
			}
			if IsWorkspaceRecordID(d.DesktopID) {
				continue
			}
			desktops = append(desktops, &d)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}
	return desktops, nil
}

func (s *DynamoStore) CreateWorkspace(ctx context.Context, w *Workspace) error {
	if w.WorkspaceID == "" {
		w.WorkspaceID = WorkspaceRecordID(w.Environment, w.WorkspaceName)
	}
	if w.CreatedAt == "" {
		w.CreatedAt = now()
	}
	w.UpdatedAt = now()

	item, err := attributevalue.MarshalMap(w)
	if err != nil {
		return fmt.Errorf("marshal workspace record: %w", err)
	}
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(s.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(desktop_id) OR workspace_state = :deleted"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":deleted": &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
		},
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return fmt.Errorf("workspace %q already exists", w.WorkspaceName)
		}
		return fmt.Errorf("put workspace record: %w", err)
	}
	return nil
}

func (s *DynamoStore) GetWorkspace(ctx context.Context, environment, name string) (*Workspace, error) {
	id := WorkspaceRecordID(environment, name)
	out, err := s.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get workspace %q: %w", name, err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	var w Workspace
	if err := attributevalue.UnmarshalMap(out.Item, &w); err != nil {
		return nil, fmt.Errorf("unmarshal workspace record: %w", err)
	}
	return &w, nil
}

func (s *DynamoStore) ListWorkspaces(ctx context.Context) ([]*Workspace, error) {
	var workspaces []*Workspace
	var exclusiveStartKey map[string]types.AttributeValue
	for {
		out, err := s.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(s.tableName),
			ExclusiveStartKey: exclusiveStartKey,
		})
		if err != nil {
			return nil, fmt.Errorf("scan fleet table for workspaces: %w", err)
		}
		for _, item := range out.Items {
			idAttr, ok := item["desktop_id"].(*types.AttributeValueMemberS)
			if !ok || !IsWorkspaceRecordID(idAttr.Value) {
				continue
			}
			var w Workspace
			if err := attributevalue.UnmarshalMap(item, &w); err != nil {
				return nil, fmt.Errorf("unmarshal workspace record: %w", err)
			}
			workspaces = append(workspaces, &w)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}
	return workspaces, nil
}

func (s *DynamoStore) UpdateWorkspace(ctx context.Context, w *Workspace) error {
	if w.WorkspaceID == "" {
		w.WorkspaceID = WorkspaceRecordID(w.Environment, w.WorkspaceName)
	}
	w.UpdatedAt = now()
	item, err := attributevalue.MarshalMap(w)
	if err != nil {
		return fmt.Errorf("marshal workspace record: %w", err)
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
		return fmt.Errorf("update workspace record: %w", err)
	}
	return nil
}

func (s *DynamoStore) UpdateDetachedWorkspaceRepos(ctx context.Context, environment, name string, repos []string, repoFingerprint string) error {
	normalizedRepos := NormalizeWorkspaceRepos(repos)
	reposAttr, err := attributevalue.Marshal(normalizedRepos)
	if err != nil {
		return fmt.Errorf("marshal workspace repos: %w", err)
	}
	ts := now()
	_, err = s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: WorkspaceRecordID(environment, name)},
		},
		UpdateExpression: aws.String("SET repos = :repos, repo_fingerprint = :fingerprint, updated_at = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":deleted":     &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
			":empty":       &types.AttributeValueMemberS{Value: ""},
			":fingerprint": &types.AttributeValueMemberS{Value: repoFingerprint},
			":repos":       reposAttr,
			":t":           &types.AttributeValueMemberS{Value: ts},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id) AND (attribute_not_exists(attached_desktop_id) OR attached_desktop_id = :empty) AND (attribute_not_exists(workspace_state) OR workspace_state <> :deleted)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			w, getErr := s.GetWorkspace(ctx, environment, name)
			if errors.Is(getErr, ErrNotFound) || (getErr == nil && w.State == WorkspaceStateDeleted) {
				return ErrNotFound
			}
			return ErrWorkspaceAttached
		}
		return fmt.Errorf("update detached workspace repos: %w", err)
	}
	return nil
}

func (s *DynamoStore) DeleteWorkspace(ctx context.Context, environment, name string) error {
	ts := now()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: WorkspaceRecordID(environment, name)},
		},
		UpdateExpression: aws.String("SET workspace_state = :deleted, updated_at = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":deleted": &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
			":empty":   &types.AttributeValueMemberS{Value: ""},
			":t":       &types.AttributeValueMemberS{Value: ts},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id) AND (attribute_not_exists(attached_desktop_id) OR attached_desktop_id = :empty) AND (attribute_not_exists(workspace_state) OR workspace_state <> :deleted)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			w, getErr := s.GetWorkspace(ctx, environment, name)
			if errors.Is(getErr, ErrNotFound) || (getErr == nil && w.State == WorkspaceStateDeleted) {
				return ErrNotFound
			}
			return ErrWorkspaceAttached
		}
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

func (s *DynamoStore) AttachWorkspace(ctx context.Context, environment, name, desktopID, desktopName string) error {
	ts := now()
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: WorkspaceRecordID(environment, name)},
		},
		UpdateExpression: aws.String("SET workspace_state = :attached, attached_desktop_id = :desktop_id, attached_desktop_name = :desktop_name, updated_at = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":attached":     &types.AttributeValueMemberS{Value: string(WorkspaceStateAttached)},
			":deleted":      &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
			":desktop_id":   &types.AttributeValueMemberS{Value: desktopID},
			":desktop_name": &types.AttributeValueMemberS{Value: desktopName},
			":empty":        &types.AttributeValueMemberS{Value: ""},
			":t":            &types.AttributeValueMemberS{Value: ts},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id) AND (attribute_not_exists(attached_desktop_id) OR attached_desktop_id = :empty OR attached_desktop_id = :desktop_id) AND (attribute_not_exists(workspace_state) OR workspace_state <> :deleted)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			if _, getErr := s.GetWorkspace(ctx, environment, name); errors.Is(getErr, ErrNotFound) {
				return ErrNotFound
			}
			return ErrWorkspaceAttached
		}
		return fmt.Errorf("attach workspace: %w", err)
	}
	return nil
}

func (s *DynamoStore) DetachWorkspace(ctx context.Context, environment, name, desktopID string) error {
	ts := now()
	exprValues := map[string]types.AttributeValue{
		":available": &types.AttributeValueMemberS{Value: string(WorkspaceStateAvailable)},
		":deleted":   &types.AttributeValueMemberS{Value: string(WorkspaceStateDeleted)},
		":t":         &types.AttributeValueMemberS{Value: ts},
	}
	condition := "attribute_exists(desktop_id) AND (attribute_not_exists(workspace_state) OR workspace_state <> :deleted)"
	if desktopID != "" {
		condition += " AND (attribute_not_exists(attached_desktop_id) OR attached_desktop_id = :desktop_id)"
		exprValues[":desktop_id"] = &types.AttributeValueMemberS{Value: desktopID}
	}
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: WorkspaceRecordID(environment, name)},
		},
		UpdateExpression:          aws.String("SET workspace_state = :available, updated_at = :t REMOVE attached_desktop_id, attached_desktop_name"),
		ExpressionAttributeValues: exprValues,
		ConditionExpression:       aws.String(condition),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			w, getErr := s.GetWorkspace(ctx, environment, name)
			if errors.Is(getErr, ErrNotFound) || (getErr == nil && w.State == WorkspaceStateDeleted) {
				return ErrNotFound
			}
			return ErrWorkspaceAttached
		}
		return fmt.Errorf("detach workspace: %w", err)
	}
	return nil
}

func (s *DynamoStore) Update(ctx context.Context, d *Desktop) error {
	previousVersion := d.StateVersion
	updated := *d
	updated.StateVersion++
	updated.UpdatedAt = now()
	item, err := attributevalue.MarshalMap(&updated)
	if err != nil {
		return fmt.Errorf("marshal desktop record: %w", err)
	}
	// A full-record update must never restore an earlier fencing token or secret
	// list. Once secret coordination starts, only CommitSecretOperation may
	// change paths; ordinary updates may still change unrelated desktop fields.
	condition := "attribute_exists(desktop_id) AND attribute_not_exists(secret_operation_token)"
	values := map[string]types.AttributeValue{}
	versionCondition := "state_version = :version"
	if previousVersion == 0 {
		versionCondition = "(attribute_not_exists(state_version) OR state_version = :version)"
	}
	values[":version"] = &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", previousVersion)}
	if d.SecretOperationToken != "" {
		paths := make([]types.AttributeValue, len(d.Secrets))
		for i, path := range d.Secrets {
			paths[i] = &types.AttributeValueMemberS{Value: path}
		}
		values[":token"] = &types.AttributeValueMemberS{Value: d.SecretOperationToken}
		values[":secrets"] = &types.AttributeValueMemberL{Value: paths}
		secretCondition := "secret_operation_token = :token AND secrets = :secrets"
		if len(d.Secrets) == 0 {
			secretCondition = "secret_operation_token = :token AND (attribute_not_exists(secrets) OR secrets = :secrets)"
		}
		condition = "attribute_exists(desktop_id) AND " + secretCondition
	}
	condition += " AND " + versionCondition
	_, err = s.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                           aws.String(s.tableName),
		Item:                                item,
		ConditionExpression:                 aws.String(condition),
		ExpressionAttributeValues:           values,
		ReturnValuesOnConditionCheckFailure: types.ReturnValuesOnConditionCheckFailureAllOld,
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			if len(cce.Item) != 0 {
				var current Desktop
				if unmarshalErr := attributevalue.UnmarshalMap(cce.Item, &current); unmarshalErr == nil && current.SecretOperationToken == d.SecretOperationToken && (current.SecretOperationToken == "" || slices.Equal(current.Secrets, d.Secrets)) {
					return ErrStateConflict
				}
				return ErrSecretOperationChanged
			}
			return ErrNotFound
		}
		return fmt.Errorf("update desktop record: %w", err)
	}
	d.StateVersion = updated.StateVersion
	d.UpdatedAt = updated.UpdatedAt
	return nil
}

// RecordParkFailure fences stale park workers after an explicit retry claim.
func (s *DynamoStore) RecordParkFailure(ctx context.Context, id, token, message string) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 map[string]types.AttributeValue{"desktop_id": &types.AttributeValueMemberS{Value: id}},
		UpdateExpression:    aws.String("SET lifecycle_state = :failed, failure_phase = :phase, failure_message = :message, updated_at = :now REMOVE park_operation_token ADD state_version :one"),
		ConditionExpression: aws.String("lifecycle_state = :parking AND park_operation_token = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":failed":  &types.AttributeValueMemberS{Value: string(StateFailed)},
			":parking": &types.AttributeValueMemberS{Value: string(StateParking)},
			":phase":   &types.AttributeValueMemberS{Value: "park"},
			":message": &types.AttributeValueMemberS{Value: message},
			":now":     &types.AttributeValueMemberS{Value: now()},
			":one":     &types.AttributeValueMemberN{Value: "1"},
			":token":   &types.AttributeValueMemberS{Value: token},
		},
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrStateConflict
		}
		return fmt.Errorf("record park failure: %w", err)
	}
	return nil
}

// RecordUnparkFailure fences stale restore workers after an explicit retry claim.
func (s *DynamoStore) RecordUnparkFailure(ctx context.Context, id, token, message string) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 map[string]types.AttributeValue{"desktop_id": &types.AttributeValueMemberS{Value: id}},
		UpdateExpression:    aws.String("SET lifecycle_state = :failed, failure_phase = :phase, failure_message = :message, updated_at = :now REMOVE unpark_operation_token ADD state_version :one"),
		ConditionExpression: aws.String("lifecycle_state = :restoring AND unpark_operation_token = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":failed":    &types.AttributeValueMemberS{Value: string(StateFailed)},
			":restoring": &types.AttributeValueMemberS{Value: string(StateRestoring)},
			":phase":     &types.AttributeValueMemberS{Value: "unpark"},
			":message":   &types.AttributeValueMemberS{Value: message},
			":now":       &types.AttributeValueMemberS{Value: now()},
			":one":       &types.AttributeValueMemberN{Value: "1"},
			":token":     &types.AttributeValueMemberS{Value: token},
		},
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrStateConflict
		}
		return fmt.Errorf("record unpark failure: %w", err)
	}
	return nil
}

// Transition atomically claims a lifecycle transition across CLI processes.
func (s *DynamoStore) Transition(ctx context.Context, id string, from, to LifecycleState) error {
	_, err := s.client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(s.tableName),
		Key:                 map[string]types.AttributeValue{"desktop_id": &types.AttributeValueMemberS{Value: id}},
		UpdateExpression:    aws.String("SET lifecycle_state = :next, updated_at = :now ADD state_version :one"),
		ConditionExpression: aws.String("lifecycle_state = :previous"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":previous": &types.AttributeValueMemberS{Value: string(from)},
			":next":     &types.AttributeValueMemberS{Value: string(to)},
			":now":      &types.AttributeValueMemberS{Value: now()},
			":one":      &types.AttributeValueMemberN{Value: "1"},
		},
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrStateConflict
		}
		return fmt.Errorf("transition desktop: %w", err)
	}
	return nil
}

func (s *DynamoStore) Delete(ctx context.Context, id string) error {
	_, err := s.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(s.tableName),
		Key: map[string]types.AttributeValue{
			"desktop_id": &types.AttributeValueMemberS{Value: id},
		},
		ConditionExpression: aws.String("attribute_exists(desktop_id)"),
	})
	if err != nil {
		var cce *types.ConditionalCheckFailedException
		if errors.As(err, &cce) {
			return ErrNotFound
		}
		return fmt.Errorf("delete desktop %q: %w", id, err)
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
		UpdateExpression: aws.String("SET lifecycle_state = :s, updated_at = :t ADD state_version :one"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":   &types.AttributeValueMemberS{Value: string(StateTerminated)},
			":t":   &types.AttributeValueMemberS{Value: ts},
			":one": &types.AttributeValueMemberN{Value: "1"},
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
		UpdateExpression: aws.String("SET lifecycle_state = :s, failure_phase = :p, failure_message = :m, updated_at = :t ADD state_version :one"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":   &types.AttributeValueMemberS{Value: string(StateFailed)},
			":p":   &types.AttributeValueMemberS{Value: phase},
			":m":   &types.AttributeValueMemberS{Value: message},
			":t":   &types.AttributeValueMemberS{Value: ts},
			":one": &types.AttributeValueMemberN{Value: "1"},
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
