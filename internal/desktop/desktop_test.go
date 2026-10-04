package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/orchael/desktopctl/internal/store"
)

func TestGenerateID(t *testing.T) {
	id, err := GenerateID()
	if err != nil {
		t.Fatalf("GenerateID: %v", err)
	}
	if len(id) == 0 {
		t.Error("ID must not be empty")
	}
	if id[:2] != "d-" {
		t.Errorf("ID should start with d-, got %q", id)
	}

	id2, _ := GenerateID()
	if id == id2 {
		t.Error("generated IDs should be unique")
	}
}

func TestStackName(t *testing.T) {
	if got := StackName("abc123"); got != "desktop-abc123" {
		t.Errorf("got %q", got)
	}
}

func TestHostname(t *testing.T) {
	if got := Hostname("d-001", "desktops.orchael.dev"); got != "d-001.desktops.orchael.dev" {
		t.Errorf("got %q", got)
	}
}

func TestNoVNCURL(t *testing.T) {
	if got := NoVNCURL("d-001.desktops.orchael.dev"); got != "https://d-001.desktops.orchael.dev:8443/novnc/vnc.html" {
		t.Errorf("got %q", got)
	}
}

func TestSSHTarget(t *testing.T) {
	if got := SSHTarget("d-001.desktops.orchael.dev"); got != "ubuntu@d-001.desktops.orchael.dev" {
		t.Errorf("got %q", got)
	}
}

func TestCreateRequest_Validate(t *testing.T) {
	r := &CreateRequest{GitHubOwner: "acme", Zone: "desktops.orchael.dev", BackendBucket: "bucket"}
	if err := r.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if r.WorkspaceMode != "local" {
		t.Errorf("workspace mode default = %q, want local", r.WorkspaceMode)
	}

	r.GitHubOwner = ""
	if err := r.Validate(); err == nil {
		t.Error("expected error for missing GitHubOwner")
	}
}

func TestCreateRequest_ValidateWorkspaceMode(t *testing.T) {
	base := CreateRequest{GitHubOwner: "acme", Zone: "desktops.orchael.dev", BackendBucket: "bucket"}
	efs := base
	efs.WorkspaceMode = "efs"
	efs.WorkspaceName = "factory-dev"
	if err := efs.Validate(); err != nil {
		t.Fatalf("efs workspace should validate: %v", err)
	}

	missingName := base
	missingName.WorkspaceMode = "efs"
	if err := missingName.Validate(); err == nil {
		t.Fatal("expected missing workspace name error")
	}

	localWithName := base
	localWithName.WorkspaceName = "factory-dev"
	if err := localWithName.Validate(); err == nil {
		t.Fatal("expected local workspace-name error")
	}
}

func TestManager_CreateRecord(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	req := &CreateRequest{
		OrganizationID: "00000000-0000-4000-8000-000000000001",
		DesktopName:    "factory-dev",
		GitHubOwner:    "acme",
		Zone:           "desktops.orchael.dev",
		BackendBucket:  "my-bucket",
		Environment:    "dev",
		Repos:          []string{"github.com/acme/app"},
		InstanceType:   "m7i.xlarge",
		MarketType:     store.MarketSpot,
		WorkspaceMode:  "efs",
		WorkspaceName:  "workspace-dev",
		WorkspaceID:    "workspace:dev:workspace-dev",
	}

	if err := m.CreateRecord(ctx, "d-test1", req); err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}

	d, err := s.Get(ctx, "d-test1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.State != store.StateCreating {
		t.Errorf("state: got %q", d.State)
	}
	if d.GitHubOwner != "acme" {
		t.Errorf("owner: got %q", d.GitHubOwner)
	}
	if d.OrganizationID != "00000000-0000-4000-8000-000000000001" {
		t.Errorf("organization ID: got %q", d.OrganizationID)
	}
	if d.InstanceType != "m7i.xlarge" {
		t.Errorf("instance type: got %q", d.InstanceType)
	}
	if d.MarketType != store.MarketSpot {
		t.Errorf("market type: got %q", d.MarketType)
	}
	if d.DesktopName != "factory-dev" || d.Environment != "dev" {
		t.Errorf("desktop name/environment not persisted: %+v", d)
	}
	if d.WorkspaceMode != "efs" || d.WorkspaceName != "workspace-dev" || d.WorkspaceID != "workspace:dev:workspace-dev" {
		t.Errorf("workspace fields not persisted: %+v", d)
	}
}

func TestManager_MarkReady(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{
		DesktopID:    "d-r1",
		State:        store.StateFailed,
		FailurePhase: "create",
		FailureMsg:   "old failure",
	})
	if err := m.MarkReady(ctx, "d-r1", "all checks passed"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	d, _ := s.Get(ctx, "d-r1")
	if d.State != store.StateReady {
		t.Errorf("state: got %q", d.State)
	}
	if d.FailurePhase != "" || d.FailureMsg != "" {
		t.Errorf("failure fields should be cleared: phase=%q msg=%q", d.FailurePhase, d.FailureMsg)
	}
}

func TestManager_MarkRunningClearsStopAndFailureDetails(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{
		DesktopID:    "d-run1",
		State:        store.StateFailed,
		StopReason:   store.StopReasonUserRequest,
		StoppedAt:    "2026-09-01T00:00:00Z",
		FailurePhase: "start",
		FailureMsg:   "old failure",
	})
	if err := m.MarkRunning(ctx, "d-run1", "started"); err != nil {
		t.Fatalf("MarkRunning: %v", err)
	}
	d, _ := s.Get(ctx, "d-run1")
	if d.State != store.StateReady {
		t.Errorf("state: got %q", d.State)
	}
	if d.StopReason != "" || d.StoppedAt != "" {
		t.Errorf("stop fields should be cleared: reason=%q stopped_at=%q", d.StopReason, d.StoppedAt)
	}
	if d.FailurePhase != "" || d.FailureMsg != "" {
		t.Errorf("failure fields should be cleared: phase=%q msg=%q", d.FailurePhase, d.FailureMsg)
	}
}

func TestManager_UpdateFromOutputsUpdatesInstanceType(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{
		DesktopID:    "d-u1",
		State:        store.StateCreating,
		InstanceType: "m7i.xlarge",
	})
	if err := m.UpdateFromOutputs(ctx, "d-u1", map[string]string{
		"instanceId":   "i-123",
		"instanceType": "m6i.xlarge",
	}); err != nil {
		t.Fatalf("UpdateFromOutputs: %v", err)
	}
	d, _ := s.Get(ctx, "d-u1")
	if d.InstanceID != "i-123" {
		t.Errorf("instance ID: got %q", d.InstanceID)
	}
	if d.InstanceType != "m6i.xlarge" {
		t.Errorf("instance type: got %q", d.InstanceType)
	}
}

func TestManager_MarkStopped(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-s1", State: store.StateReady})
	if err := m.MarkStopped(ctx, "d-s1"); err != nil {
		t.Fatalf("MarkStopped: %v", err)
	}
	d, _ := s.Get(ctx, "d-s1")
	if d.State != store.StateStopped {
		t.Errorf("state: got %q", d.State)
	}
}

func TestManager_MarkStoppedWithReason(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-s2", State: store.StateReady})
	if err := m.MarkStoppedWithReason(ctx, "d-s2", store.StopReasonSpotInterruption); err != nil {
		t.Fatalf("MarkStoppedWithReason: %v", err)
	}
	d, _ := s.Get(ctx, "d-s2")
	if d.State != store.StateStopped {
		t.Errorf("state: got %q", d.State)
	}
	if d.StopReason != store.StopReasonSpotInterruption {
		t.Errorf("stop reason: got %q", d.StopReason)
	}
	if d.StoppedAt == "" {
		t.Error("StoppedAt should be set when a stop reason is recorded")
	}
}

func TestManager_MarkTerminating(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-t1", State: store.StateReady})
	if err := m.MarkTerminating(ctx, "d-t1"); err != nil {
		t.Fatalf("MarkTerminating: %v", err)
	}
	d, _ := s.Get(ctx, "d-t1")
	if d.State != store.StateTerminating {
		t.Errorf("state: got %q", d.State)
	}
}

func TestManager_RemoveSecrets(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{
		DesktopID: "d-sec1",
		Secrets:   []string{"/one", "/two", "/three"},
	})
	if err := m.RemoveSecrets(ctx, "d-sec1", []string{"/two", "/missing", "/two"}); err != nil {
		t.Fatalf("RemoveSecrets: %v", err)
	}

	d, _ := s.Get(ctx, "d-sec1")
	want := []string{"/one", "/three"}
	if len(d.Secrets) != len(want) {
		t.Fatalf("secrets = %#v, want %#v", d.Secrets, want)
	}
	for i := range want {
		if d.Secrets[i] != want[i] {
			t.Fatalf("secrets = %#v, want %#v", d.Secrets, want)
		}
	}
}

func TestManager_RecordFailure(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-f1", State: store.StateCreating})
	if err := m.RecordFailure(ctx, "d-f1", "readiness", "SSH timeout"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	d, _ := s.Get(ctx, "d-f1")
	if d.State != store.StateFailed {
		t.Errorf("state: got %q", d.State)
	}
	if d.FailurePhase != "readiness" {
		t.Errorf("phase: got %q", d.FailurePhase)
	}
}

func TestManager_NotFound(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	err := m.MarkReady(ctx, "missing", "")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRepoNames(t *testing.T) {
	d := &store.Desktop{
		Repos: []string{
			"github.com/acme/app-one",
			"https://github.com/acme/app-two.git",
		},
	}
	names := RepoNames(d)
	if len(names) != 2 {
		t.Fatalf("got %d names: %v", len(names), names)
	}
}
