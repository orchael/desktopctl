package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// LifecycleState represents the known state of a desktop.
type LifecycleState string

const (
	StateCreating           LifecycleState = "creating"
	StateReady              LifecycleState = "ready"
	StateStopped            LifecycleState = "stopped"
	StateUnhealthy          LifecycleState = "unhealthy"
	StateFailed             LifecycleState = "failed"
	StateProvisioningFailed LifecycleState = "provisioning_failed"
	StateTerminating        LifecycleState = "terminating"
	StateTerminated         LifecycleState = "terminated"
)

// Desktop is the fleet metadata record for one managed desktop.
type Desktop struct {
	DesktopID     string         `dynamodbav:"desktop_id"`
	StackName     string         `dynamodbav:"stack_name"`
	GitHubOwner   string         `dynamodbav:"github_owner"`
	Region        string         `dynamodbav:"region"`
	State         LifecycleState `dynamodbav:"lifecycle_state"`
	InstanceID    string         `dynamodbav:"instance_id"`
	Hostname      string         `dynamodbav:"hostname"`
	NoVNCURL      string         `dynamodbav:"novnc_url"`
	SSHTarget     string         `dynamodbav:"ssh_target"`
	AMIID         string         `dynamodbav:"ami_id,omitempty"`
	Readiness     string         `dynamodbav:"readiness"`
	FailurePhase  string         `dynamodbav:"failure_phase,omitempty"`
	FailureMsg    string         `dynamodbav:"failure_message,omitempty"`
	Repos         []string       `dynamodbav:"repos,omitempty"`
	WorkspacePath string         `dynamodbav:"workspace_path,omitempty"`
	CreatedAt     string         `dynamodbav:"created_at"`
	UpdatedAt     string         `dynamodbav:"updated_at"`
}

// now returns the current time as RFC3339.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ErrNotFound is returned when a desktop record does not exist.
var ErrNotFound = errors.New("desktop not found")

// Store is the interface for fleet metadata operations.
type Store interface {
	Create(ctx context.Context, d *Desktop) error
	Get(ctx context.Context, id string) (*Desktop, error)
	List(ctx context.Context) ([]*Desktop, error)
	Update(ctx context.Context, d *Desktop) error
	MarkTerminated(ctx context.Context, id string) error
	RecordFailure(ctx context.Context, id, phase, message string) error
}

// DynamoAPI is the subset of the DynamoDB client used by DynamoStore.
type DynamoAPI interface {
	PutItem(ctx context.Context, input interface{}) (interface{}, error)
	GetItem(ctx context.Context, input interface{}) (interface{}, error)
	Scan(ctx context.Context, input interface{}) (interface{}, error)
	UpdateItem(ctx context.Context, input interface{}) (interface{}, error)
}

// InMemoryStore is a non-persistent Store implementation used in tests and
// local-only CLI development.
type InMemoryStore struct {
	records map[string]*Desktop
}

// NewInMemoryStore returns an initialised in-memory store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{records: make(map[string]*Desktop)}
}

func (s *InMemoryStore) Create(ctx context.Context, d *Desktop) error {
	if _, ok := s.records[d.DesktopID]; ok {
		return fmt.Errorf("desktop %q already exists", d.DesktopID)
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now()
	}
	d.UpdatedAt = now()
	cp := *d
	s.records[d.DesktopID] = &cp
	return nil
}

func (s *InMemoryStore) Get(ctx context.Context, id string) (*Desktop, error) {
	d, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (s *InMemoryStore) List(ctx context.Context) ([]*Desktop, error) {
	out := make([]*Desktop, 0, len(s.records))
	for _, d := range s.records {
		cp := *d
		out = append(out, &cp)
	}
	return out, nil
}

func (s *InMemoryStore) Update(ctx context.Context, d *Desktop) error {
	if _, ok := s.records[d.DesktopID]; !ok {
		return ErrNotFound
	}
	d.UpdatedAt = now()
	cp := *d
	s.records[d.DesktopID] = &cp
	return nil
}

func (s *InMemoryStore) MarkTerminated(ctx context.Context, id string) error {
	d, ok := s.records[id]
	if !ok {
		return ErrNotFound
	}
	d.State = StateTerminated
	d.UpdatedAt = now()
	return nil
}

func (s *InMemoryStore) RecordFailure(ctx context.Context, id, phase, message string) error {
	d, ok := s.records[id]
	if !ok {
		return ErrNotFound
	}
	d.State = StateFailed
	d.FailurePhase = phase
	d.FailureMsg = message
	d.UpdatedAt = now()
	return nil
}
