package store

import (
	"context"
	"errors"
	"testing"
)

func newDesktop(id string) *Desktop {
	return &Desktop{
		DesktopID:   id,
		StackName:   "ai-desktops/desktop-" + id,
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

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("got %d items, want 2", len(list))
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
