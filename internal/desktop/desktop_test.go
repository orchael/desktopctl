package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
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

	r.GitHubOwner = ""
	if err := r.Validate(); err == nil {
		t.Error("expected error for missing GitHubOwner")
	}
}

func TestManager_CreateRecord(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	req := &CreateRequest{
		GitHubOwner:   "acme",
		Zone:          "desktops.orchael.dev",
		BackendBucket: "my-bucket",
		Repos:         []string{"github.com/acme/app"},
		InstanceType:  "m7i.xlarge",
		MarketType:    store.MarketSpot,
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
	if d.InstanceType != "m7i.xlarge" {
		t.Errorf("instance type: got %q", d.InstanceType)
	}
	if d.MarketType != store.MarketSpot {
		t.Errorf("market type: got %q", d.MarketType)
	}
}

func TestManager_MarkReady(t *testing.T) {
	s := store.NewInMemoryStore()
	m := NewManager(s)
	ctx := context.Background()

	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-r1", State: store.StateCreating})
	if err := m.MarkReady(ctx, "d-r1", "all checks passed"); err != nil {
		t.Fatalf("MarkReady: %v", err)
	}
	d, _ := s.Get(ctx, "d-r1")
	if d.State != store.StateReady {
		t.Errorf("state: got %q", d.State)
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
