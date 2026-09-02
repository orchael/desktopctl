package store

import (
	"context"
	"errors"
	"testing"
)

func newDesktop(id string) *Desktop {
	return &Desktop{
		DesktopID:   id,
		StackName:   "desktop-" + id,
		GitHubOwner: "acme",
		State:       StateCreating,
	}
}

func TestInMemoryStore_Create(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	d := newDesktop("d-001")
	if err := s.Create(ctx, d); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Duplicate should error.
	if err := s.Create(ctx, d); err == nil {
		t.Error("expected duplicate create error")
	}
}

func TestInMemoryStore_Get(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_, err := s.Get(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	d := newDesktop("d-002")
	_ = s.Create(ctx, d)

	got, err := s.Get(ctx, "d-002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DesktopID != "d-002" {
		t.Errorf("got ID %q", got.DesktopID)
	}
}

func TestInMemoryStore_List(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_ = s.Create(ctx, newDesktop("d-001"))
	_ = s.Create(ctx, newDesktop("d-002"))
	_ = s.CreateWorkspace(ctx, &Workspace{
		WorkspaceName:   "factory-dev",
		WorkspaceMode:   "efs",
		Environment:     "dev",
		GitHubOwner:     "acme",
		EFSFileSystemID: "fs-123",
		MountPath:       "/workspace",
		State:           WorkspaceStateAvailable,
	})

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("got %d items, want 2", len(list))
	}
}

func TestInMemoryStore_WorkspaceLifecycle(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	w := &Workspace{
		WorkspaceName:    "factory-dev",
		WorkspaceMode:    "efs",
		Environment:      "dev",
		GitHubOwner:      "acme",
		Repos:            []string{"github.com/acme/app"},
		RepoFingerprint:  RepoFingerprint([]string{"github.com/acme/app"}),
		EFSFileSystemID:  "fs-123",
		EFSAccessPointID: "fsap-123",
		MountPath:        "/workspace",
		State:            WorkspaceStateAvailable,
	}
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if err := s.AttachWorkspace(ctx, "dev", "factory-dev", "d-001", "desktop-one"); err != nil {
		t.Fatalf("AttachWorkspace: %v", err)
	}
	got, err := s.GetWorkspace(ctx, "dev", "factory-dev")
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.State != WorkspaceStateAttached || got.AttachedDesktopID != "d-001" {
		t.Fatalf("workspace not attached: %+v", got)
	}
	if err := s.AttachWorkspace(ctx, "dev", "factory-dev", "d-002", "desktop-two"); !errors.Is(err, ErrWorkspaceAttached) {
		t.Fatalf("AttachWorkspace conflict: got %v, want ErrWorkspaceAttached", err)
	}
	if err := s.DeleteWorkspace(ctx, "dev", "factory-dev"); !errors.Is(err, ErrWorkspaceAttached) {
		t.Fatalf("DeleteWorkspace attached: got %v, want ErrWorkspaceAttached", err)
	}
	if err := s.DetachWorkspace(ctx, "dev", "factory-dev", "d-001"); err != nil {
		t.Fatalf("DetachWorkspace: %v", err)
	}
	got, _ = s.GetWorkspace(ctx, "dev", "factory-dev")
	if got.State != WorkspaceStateAvailable || got.AttachedDesktopID != "" {
		t.Fatalf("workspace not detached: %+v", got)
	}
	if err := s.DeleteWorkspace(ctx, "dev", "factory-dev"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	got, _ = s.GetWorkspace(ctx, "dev", "factory-dev")
	if got.State != WorkspaceStateDeleted {
		t.Fatalf("workspace state = %q, want deleted", got.State)
	}
	if err := s.DetachWorkspace(ctx, "dev", "factory-dev", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DetachWorkspace deleted: got %v, want ErrNotFound", err)
	}
	got, _ = s.GetWorkspace(ctx, "dev", "factory-dev")
	if got.State != WorkspaceStateDeleted {
		t.Fatalf("deleted workspace was resurrected: %+v", got)
	}
}

func TestInMemoryStore_UpdateDetachedWorkspaceRepos(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()
	w := &Workspace{
		WorkspaceName:   "factory-dev",
		WorkspaceMode:   "efs",
		Environment:     "dev",
		GitHubOwner:     "acme",
		Repos:           []string{"github.com/acme/app"},
		RepoFingerprint: RepoFingerprint([]string{"github.com/acme/app"}),
		State:           WorkspaceStateAvailable,
	}
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	repos := []string{"github.com/acme/api", "github.com/acme/app"}
	if err := s.UpdateDetachedWorkspaceRepos(ctx, "dev", "factory-dev", repos, RepoFingerprint(repos)); err != nil {
		t.Fatalf("UpdateDetachedWorkspaceRepos: %v", err)
	}
	got, _ := s.GetWorkspace(ctx, "dev", "factory-dev")
	if got.RepoFingerprint != "github.com/acme/api\ngithub.com/acme/app" {
		t.Fatalf("fingerprint = %q", got.RepoFingerprint)
	}
	if err := s.AttachWorkspace(ctx, "dev", "factory-dev", "d-001", "desktop-one"); err != nil {
		t.Fatalf("AttachWorkspace: %v", err)
	}
	if err := s.UpdateDetachedWorkspaceRepos(ctx, "dev", "factory-dev", []string{"github.com/acme/app"}, RepoFingerprint([]string{"github.com/acme/app"})); !errors.Is(err, ErrWorkspaceAttached) {
		t.Fatalf("UpdateDetachedWorkspaceRepos attached: got %v, want ErrWorkspaceAttached", err)
	}
}

func TestRepoFingerprintNormalizesRepos(t *testing.T) {
	got := RepoFingerprint([]string{" github.com/acme/b ", "github.com/acme/a", "github.com/acme/a"})
	want := "github.com/acme/a\ngithub.com/acme/b"
	if got != want {
		t.Fatalf("RepoFingerprint = %q, want %q", got, want)
	}
}

func TestInMemoryStore_Update(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_ = s.Create(ctx, newDesktop("d-003"))

	d, _ := s.Get(ctx, "d-003")
	d.State = StateReady
	d.InstanceID = "i-abc123"
	d.Hostname = "d-003.desktops.orchael.dev"

	if err := s.Update(ctx, d); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.Get(ctx, "d-003")
	if got.State != StateReady {
		t.Errorf("state: got %q, want %q", got.State, StateReady)
	}
	if got.InstanceID != "i-abc123" {
		t.Errorf("instanceID: got %q", got.InstanceID)
	}
}

func TestInMemoryStore_Delete(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_ = s.Create(ctx, newDesktop("d-delete"))
	if err := s.Delete(ctx, "d-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "d-delete"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
	if err := s.Delete(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInMemoryStore_MarkTerminated(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_ = s.Create(ctx, newDesktop("d-004"))
	if err := s.MarkTerminated(ctx, "d-004"); err != nil {
		t.Fatalf("MarkTerminated: %v", err)
	}

	d, _ := s.Get(ctx, "d-004")
	if d.State != StateTerminated {
		t.Errorf("state: got %q, want %q", d.State, StateTerminated)
	}

	if err := s.MarkTerminated(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInMemoryStore_RecordFailure(t *testing.T) {
	s := NewInMemoryStore()
	ctx := context.Background()

	_ = s.Create(ctx, newDesktop("d-005"))
	if err := s.RecordFailure(ctx, "d-005", "pulumi-up", "stack update failed"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	d, _ := s.Get(ctx, "d-005")
	if d.State != StateFailed {
		t.Errorf("state: got %q, want %q", d.State, StateFailed)
	}
	if d.FailurePhase != "pulumi-up" {
		t.Errorf("phase: got %q", d.FailurePhase)
	}
	if d.FailureMsg != "stack update failed" {
		t.Errorf("msg: got %q", d.FailureMsg)
	}
}

func TestLifecycleTransitions(t *testing.T) {
	states := []LifecycleState{
		StateCreating,
		StateReady,
		StateStopped,
		StateUnhealthy,
		StateFailed,
		StateProvisioningFailed,
		StateTerminating,
		StateTerminated,
	}
	for _, s := range states {
		if s == "" {
			t.Errorf("lifecycle state constant must not be empty")
		}
	}
}
