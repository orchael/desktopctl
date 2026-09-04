package store

import (
	"context"
	"testing"
)

func TestInMemoryPoolStore_CreateAndGet(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	a := &ComputeAllocation{
		InstanceID:      "i-001",
		PoolMemberState: PoolStateAvailable,
		AMIID:           "ami-abc",
		ImageVersion:    "v1.2.3",
	}
	if err := s.CreatePoolMember(ctx, a); err != nil {
		t.Fatalf("CreatePoolMember: %v", err)
	}

	got, err := s.GetPoolMember(ctx, "i-001")
	if err != nil {
		t.Fatalf("GetPoolMember: %v", err)
	}
	if got.InstanceID != "i-001" {
		t.Errorf("InstanceID = %q, want %q", got.InstanceID, "i-001")
	}
	if got.PoolMemberState != PoolStateAvailable {
		t.Errorf("State = %q, want %q", got.PoolMemberState, PoolStateAvailable)
	}
}

func TestInMemoryPoolStore_GetNotFound(t *testing.T) {
	s := NewInMemoryPoolStore()
	_, err := s.GetPoolMember(context.Background(), "i-missing")
	if err != ErrPoolMemberNotFound {
		t.Fatalf("GetPoolMember missing: got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestInMemoryPoolStore_List(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable})
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-002", PoolMemberState: PoolStateInUse})

	members, err := s.ListPoolMembers(ctx)
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("ListPoolMembers = %d members, want 2", len(members))
	}
}

func TestInMemoryPoolStore_AcquireAvailable(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable})
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-002", PoolMemberState: PoolStateInUse})

	got, err := s.AcquireAvailable(ctx, "d-aaa")
	if err != nil {
		t.Fatalf("AcquireAvailable: %v", err)
	}
	if got.InstanceID != "i-001" {
		t.Errorf("AcquireAvailable returned %q, want %q", got.InstanceID, "i-001")
	}
	if got.PoolMemberState != PoolStateAllocating {
		t.Errorf("State after acquire = %q, want %q", got.PoolMemberState, PoolStateAllocating)
	}
	if got.DesktopID != "d-aaa" {
		t.Errorf("DesktopID = %q, want %q", got.DesktopID, "d-aaa")
	}
	if got.AllocatedAt == "" {
		t.Error("AllocatedAt should be set after acquire")
	}

	// State must be persisted in store.
	stored, _ := s.GetPoolMember(ctx, "i-001")
	if stored.PoolMemberState != PoolStateAllocating {
		t.Errorf("stored state = %q, want %q", stored.PoolMemberState, PoolStateAllocating)
	}
}

func TestInMemoryPoolStore_AcquireAvailable_Empty(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateInUse})

	_, err := s.AcquireAvailable(ctx, "d-bbb")
	if err != ErrNoAvailableCapacity {
		t.Fatalf("AcquireAvailable empty: got %v, want ErrNoAvailableCapacity", err)
	}
}

func TestInMemoryPoolStore_CountByState(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable})
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-002", PoolMemberState: PoolStateAvailable})
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-003", PoolMemberState: PoolStateInUse})
	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-004", PoolMemberState: PoolStateFailed})

	tests := []struct {
		state PoolMemberState
		want  int
	}{
		{PoolStateAvailable, 2},
		{PoolStateInUse, 1},
		{PoolStateFailed, 1},
		{PoolStateRecycling, 0},
	}
	for _, tt := range tests {
		n, err := s.CountByState(ctx, tt.state)
		if err != nil {
			t.Errorf("CountByState(%s): %v", tt.state, err)
			continue
		}
		if n != tt.want {
			t.Errorf("CountByState(%s) = %d, want %d", tt.state, n, tt.want)
		}
	}
}

func TestInMemoryPoolStore_UpdatePoolMember(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable})

	a, _ := s.GetPoolMember(ctx, "i-001")
	a.PoolMemberState = PoolStateRecycling
	if err := s.UpdatePoolMember(ctx, a); err != nil {
		t.Fatalf("UpdatePoolMember: %v", err)
	}

	got, _ := s.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != PoolStateRecycling {
		t.Errorf("state after update = %q, want %q", got.PoolMemberState, PoolStateRecycling)
	}
}

func TestInMemoryPoolStore_DeletePoolMember(t *testing.T) {
	s := NewInMemoryPoolStore()
	ctx := context.Background()

	_ = s.CreatePoolMember(ctx, &ComputeAllocation{InstanceID: "i-001", PoolMemberState: PoolStateAvailable})
	if err := s.DeletePoolMember(ctx, "i-001"); err != nil {
		t.Fatalf("DeletePoolMember: %v", err)
	}
	_, err := s.GetPoolMember(ctx, "i-001")
	if err != ErrPoolMemberNotFound {
		t.Fatalf("after delete: got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestPoolMemberStates(t *testing.T) {
	// Sanity-check all state constants are distinct non-empty strings.
	states := []PoolMemberState{
		PoolStateAvailable,
		PoolStateAllocating,
		PoolStateStarting,
		PoolStateReady,
		PoolStateInUse,
		PoolStateRecycling,
		PoolStateFailed,
	}
	seen := map[PoolMemberState]bool{}
	for _, s := range states {
		if s == "" {
			t.Error("pool state must not be empty")
		}
		if seen[s] {
			t.Errorf("duplicate pool state %q", s)
		}
		seen[s] = true
	}
}
