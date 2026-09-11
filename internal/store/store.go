package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// dynamoClientAPI is the subset of the DynamoDB client used by DynamoStore and DynamoAMIStore.
type dynamoClientAPI interface {
	PutItem(ctx context.Context, params *dynamodb.PutItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	GetItem(ctx context.Context, params *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	UpdateItem(ctx context.Context, params *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
	DeleteItem(ctx context.Context, params *dynamodb.DeleteItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error)
}

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

const (
	MarketOnDemand = "on-demand"
	MarketSpot     = "spot"

	StopReasonUserRequest      = "user-request"
	StopReasonSpotInterruption = "spot-interruption"
	StopReasonAWSStopped       = "aws-stopped"
)

type WorkspaceState string

const (
	WorkspaceStateAvailable WorkspaceState = "available"
	WorkspaceStateAttached  WorkspaceState = "attached"
	WorkspaceStateFailed    WorkspaceState = "failed"
	WorkspaceStateDeleted   WorkspaceState = "deleted"
)

// Desktop is the fleet metadata record for one managed desktop.
type Desktop struct {
	DesktopID      string         `dynamodbav:"desktop_id"       json:"desktop_id"`
	OrganizationID string         `dynamodbav:"organization_id,omitempty" json:"organization_id,omitempty"`
	DesktopName    string         `dynamodbav:"desktop_name,omitempty" json:"desktop_name,omitempty"`
	StackName      string         `dynamodbav:"stack_name"       json:"stack_name"`
	GitHubOwner    string         `dynamodbav:"github_owner"     json:"github_owner"`
	Region         string         `dynamodbav:"region"           json:"region"`
	Environment    string         `dynamodbav:"environment,omitempty" json:"environment,omitempty"`
	State          LifecycleState `dynamodbav:"lifecycle_state"  json:"lifecycle_state"`
	InstanceID     string         `dynamodbav:"instance_id"      json:"instance_id"`
	Hostname       string         `dynamodbav:"hostname"         json:"hostname"`
	NoVNCURL       string         `dynamodbav:"novnc_url"        json:"novnc_url"`
	SSHTarget      string         `dynamodbav:"ssh_target"       json:"ssh_target"`
	AMIID          string         `dynamodbav:"ami_id,omitempty" json:"ami_id,omitempty"`
	Readiness      string         `dynamodbav:"readiness"        json:"readiness"`
	FailurePhase   string         `dynamodbav:"failure_phase,omitempty"   json:"failure_phase,omitempty"`
	FailureMsg     string         `dynamodbav:"failure_message,omitempty" json:"failure_message,omitempty"`
	Repos          []string       `dynamodbav:"repos,omitempty"           json:"repos,omitempty"`
	Secrets        []string       `dynamodbav:"secrets,omitempty"         json:"secrets,omitempty"`
	TailscaleNet   string         `dynamodbav:"tailscale_network,omitempty" json:"tailscale_network,omitempty"`
	StepCAServer   string         `dynamodbav:"step_ca_server,omitempty"  json:"step_ca_server,omitempty"`
	AVDNames       []string       `dynamodbav:"avd_names,omitempty"       json:"avd_names,omitempty"`
	InstanceType   string         `dynamodbav:"instance_type,omitempty"   json:"instance_type,omitempty"`
	NestedVirt     bool           `dynamodbav:"nested_virt,omitempty"     json:"nested_virt,omitempty"`
	MarketType     string         `dynamodbav:"market_type,omitempty"     json:"market_type,omitempty"`
	StopReason     string         `dynamodbav:"stop_reason,omitempty"     json:"stop_reason,omitempty"`
	StoppedAt      string         `dynamodbav:"stopped_at,omitempty"      json:"stopped_at,omitempty"`
	WorkspacePath  string         `dynamodbav:"workspace_path,omitempty"  json:"workspace_path,omitempty"`
	WorkspaceMode  string         `dynamodbav:"workspace_mode,omitempty"  json:"workspace_mode,omitempty"`
	WorkspaceName  string         `dynamodbav:"workspace_name,omitempty"  json:"workspace_name,omitempty"`
	WorkspaceID    string         `dynamodbav:"workspace_id,omitempty"    json:"workspace_id,omitempty"`
	CreatedAt      string         `dynamodbav:"created_at"       json:"created_at"`
	UpdatedAt      string         `dynamodbav:"updated_at"       json:"updated_at"`

	SecretOperationToken string `dynamodbav:"secret_operation_token,omitempty" json:"-"`
}

type Workspace struct {
	WorkspaceID         string         `dynamodbav:"desktop_id"                 json:"workspace_id"`
	WorkspaceName       string         `dynamodbav:"workspace_name"             json:"workspace_name"`
	WorkspaceMode       string         `dynamodbav:"workspace_mode"             json:"workspace_mode"`
	Environment         string         `dynamodbav:"environment"                json:"environment"`
	GitHubOwner         string         `dynamodbav:"github_owner"               json:"github_owner"`
	Repos               []string       `dynamodbav:"repos,omitempty"            json:"repos,omitempty"`
	RepoFingerprint     string         `dynamodbav:"repo_fingerprint,omitempty" json:"repo_fingerprint,omitempty"`
	EFSFileSystemID     string         `dynamodbav:"efs_file_system_id"         json:"efs_file_system_id"`
	EFSAccessPointID    string         `dynamodbav:"efs_access_point_id"        json:"efs_access_point_id"`
	MountPath           string         `dynamodbav:"mount_path"                 json:"mount_path"`
	State               WorkspaceState `dynamodbav:"workspace_state"            json:"workspace_state"`
	AttachedDesktopID   string         `dynamodbav:"attached_desktop_id,omitempty"   json:"attached_desktop_id,omitempty"`
	AttachedDesktopName string         `dynamodbav:"attached_desktop_name,omitempty" json:"attached_desktop_name,omitempty"`
	CreatedAt           string         `dynamodbav:"created_at"                 json:"created_at"`
	UpdatedAt           string         `dynamodbav:"updated_at"                 json:"updated_at"`
	FailurePhase        string         `dynamodbav:"failure_phase,omitempty"     json:"failure_phase,omitempty"`
	FailureMsg          string         `dynamodbav:"failure_message,omitempty"   json:"failure_message,omitempty"`
}

// now returns the current time as RFC3339.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ErrNotFound is returned when a desktop record does not exist.
var ErrNotFound = errors.New("desktop not found")
var ErrWorkspaceAttached = errors.New("workspace already attached")

// Store is the interface for fleet metadata operations.
type Store interface {
	Create(ctx context.Context, d *Desktop) error
	Get(ctx context.Context, id string) (*Desktop, error)
	List(ctx context.Context) ([]*Desktop, error)
	Update(ctx context.Context, d *Desktop) error
	Delete(ctx context.Context, id string) error
	MarkTerminated(ctx context.Context, id string) error
	RecordFailure(ctx context.Context, id, phase, message string) error
}

type WorkspaceStore interface {
	CreateWorkspace(ctx context.Context, w *Workspace) error
	GetWorkspace(ctx context.Context, environment, name string) (*Workspace, error)
	ListWorkspaces(ctx context.Context) ([]*Workspace, error)
	UpdateWorkspace(ctx context.Context, w *Workspace) error
	UpdateDetachedWorkspaceRepos(ctx context.Context, environment, name string, repos []string, repoFingerprint string) error
	DeleteWorkspace(ctx context.Context, environment, name string) error
	AttachWorkspace(ctx context.Context, environment, name, desktopID, desktopName string) error
	DetachWorkspace(ctx context.Context, environment, name, desktopID string) error
}

// InMemoryStore is a non-persistent Store implementation used in tests and
// local-only CLI development.
type InMemoryStore struct {
	records    map[string]*Desktop
	workspaces map[string]*Workspace
}

// NewInMemoryStore returns an initialised in-memory store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		records:    make(map[string]*Desktop),
		workspaces: make(map[string]*Workspace),
	}
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
	cp.Secrets = slices.Clone(d.Secrets)
	s.records[d.DesktopID] = &cp
	return nil
}

func (s *InMemoryStore) Get(ctx context.Context, id string) (*Desktop, error) {
	d, ok := s.records[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *d
	cp.Secrets = slices.Clone(d.Secrets)
	return &cp, nil
}

func (s *InMemoryStore) List(ctx context.Context) ([]*Desktop, error) {
	out := make([]*Desktop, 0, len(s.records))
	for id, d := range s.records {
		if IsWorkspaceRecordID(id) {
			continue
		}
		cp := *d
		cp.Secrets = slices.Clone(d.Secrets)
		out = append(out, &cp)
	}
	return out, nil
}

func (s *InMemoryStore) Update(ctx context.Context, d *Desktop) error {
	current, ok := s.records[d.DesktopID]
	if !ok {
		return ErrNotFound
	}
	if current.SecretOperationToken != d.SecretOperationToken ||
		(current.SecretOperationToken != "" && !slices.Equal(current.Secrets, d.Secrets)) {
		return ErrSecretOperationChanged
	}
	d.UpdatedAt = now()
	cp := *d
	cp.Secrets = slices.Clone(d.Secrets)
	s.records[d.DesktopID] = &cp
	return nil
}

func (s *InMemoryStore) Delete(ctx context.Context, id string) error {
	if _, ok := s.records[id]; !ok {
		return ErrNotFound
	}
	delete(s.records, id)
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

func WorkspaceRecordID(environment, name string) string {
	return "workspace:" + strings.TrimSpace(environment) + ":" + strings.TrimSpace(name)
}

func IsWorkspaceRecordID(id string) bool {
	return strings.HasPrefix(id, "workspace:")
}

func NormalizeWorkspaceRepos(repos []string) []string {
	out := make([]string, 0, len(repos))
	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" || slices.Contains(out, repo) {
			continue
		}
		out = append(out, repo)
	}
	slices.Sort(out)
	return out
}

func RepoFingerprint(repos []string) string {
	return strings.Join(NormalizeWorkspaceRepos(repos), "\n")
}

func (s *InMemoryStore) CreateWorkspace(ctx context.Context, w *Workspace) error {
	if w.WorkspaceID == "" {
		w.WorkspaceID = WorkspaceRecordID(w.Environment, w.WorkspaceName)
	}
	if existing, ok := s.workspaces[w.WorkspaceID]; ok && existing.State != WorkspaceStateDeleted {
		return fmt.Errorf("workspace %q already exists", w.WorkspaceName)
	}
	if w.CreatedAt == "" {
		w.CreatedAt = now()
	}
	w.UpdatedAt = now()
	cp := *w
	cp.Repos = append([]string(nil), w.Repos...)
	s.workspaces[w.WorkspaceID] = &cp
	return nil
}

func (s *InMemoryStore) GetWorkspace(ctx context.Context, environment, name string) (*Workspace, error) {
	w, ok := s.workspaces[WorkspaceRecordID(environment, name)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *w
	cp.Repos = append([]string(nil), w.Repos...)
	return &cp, nil
}

func (s *InMemoryStore) ListWorkspaces(ctx context.Context) ([]*Workspace, error) {
	out := make([]*Workspace, 0, len(s.workspaces))
	for _, w := range s.workspaces {
		cp := *w
		cp.Repos = append([]string(nil), w.Repos...)
		out = append(out, &cp)
	}
	return out, nil
}

func (s *InMemoryStore) UpdateWorkspace(ctx context.Context, w *Workspace) error {
	if w.WorkspaceID == "" {
		w.WorkspaceID = WorkspaceRecordID(w.Environment, w.WorkspaceName)
	}
	if _, ok := s.workspaces[w.WorkspaceID]; !ok {
		return ErrNotFound
	}
	w.UpdatedAt = now()
	cp := *w
	cp.Repos = append([]string(nil), w.Repos...)
	s.workspaces[w.WorkspaceID] = &cp
	return nil
}

func (s *InMemoryStore) UpdateDetachedWorkspaceRepos(ctx context.Context, environment, name string, repos []string, repoFingerprint string) error {
	id := WorkspaceRecordID(environment, name)
	w, ok := s.workspaces[id]
	if !ok || w.State == WorkspaceStateDeleted {
		return ErrNotFound
	}
	if w.AttachedDesktopID != "" {
		return ErrWorkspaceAttached
	}
	w.Repos = NormalizeWorkspaceRepos(repos)
	w.RepoFingerprint = repoFingerprint
	w.UpdatedAt = now()
	return nil
}

func (s *InMemoryStore) DeleteWorkspace(ctx context.Context, environment, name string) error {
	id := WorkspaceRecordID(environment, name)
	w, ok := s.workspaces[id]
	if !ok {
		return ErrNotFound
	}
	if w.AttachedDesktopID != "" {
		return ErrWorkspaceAttached
	}
	w.State = WorkspaceStateDeleted
	w.UpdatedAt = now()
	return nil
}

func (s *InMemoryStore) AttachWorkspace(ctx context.Context, environment, name, desktopID, desktopName string) error {
	w, ok := s.workspaces[WorkspaceRecordID(environment, name)]
	if !ok {
		return ErrNotFound
	}
	if w.AttachedDesktopID != "" && w.AttachedDesktopID != desktopID {
		return ErrWorkspaceAttached
	}
	w.State = WorkspaceStateAttached
	w.AttachedDesktopID = desktopID
	w.AttachedDesktopName = desktopName
	w.UpdatedAt = now()
	return nil
}

func (s *InMemoryStore) DetachWorkspace(ctx context.Context, environment, name, desktopID string) error {
	w, ok := s.workspaces[WorkspaceRecordID(environment, name)]
	if !ok {
		return ErrNotFound
	}
	if w.State == WorkspaceStateDeleted {
		return ErrNotFound
	}
	if desktopID != "" && w.AttachedDesktopID != "" && w.AttachedDesktopID != desktopID {
		return ErrWorkspaceAttached
	}
	w.State = WorkspaceStateAvailable
	w.AttachedDesktopID = ""
	w.AttachedDesktopName = ""
	w.UpdatedAt = now()
	return nil
}
