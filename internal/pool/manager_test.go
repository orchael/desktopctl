package pool

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

// fakeProvisioner is a controllable Provisioner double for unit tests.
type fakeProvisioner struct {
	provisionErr error
	calls        int
	allocation   *store.ComputeAllocation
}

func (f *fakeProvisioner) ProvisionPoolMember(_ context.Context) (*store.ComputeAllocation, error) {
	f.calls++
	if f.provisionErr != nil {
		return nil, f.provisionErr
	}
	if f.allocation != nil {
		return f.allocation, nil
	}
	return &store.ComputeAllocation{
		InstanceID:      "i-new",
		PoolMemberState: store.PoolStateAvailable,
	}, nil
}

func TestManager_Replenish_PoolFull(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	ctx := context.Background()
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-001", PoolMemberState: store.PoolStateAvailable})
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-002", PoolMemberState: store.PoolStateAvailable})

	prov := &fakeProvisioner{}
	m := NewManager(ps, prov, 2)

	if err := m.Replenish(ctx); err != nil {
		t.Fatalf("Replenish: %v", err)
	}
	if prov.calls != 0 {
		t.Errorf("provisioner called %d times, want 0 (pool full)", prov.calls)
	}
}

func TestManager_Replenish_PoolShort(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	ctx := context.Background()
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-001", PoolMemberState: store.PoolStateAvailable})

	callCount := 0
	prov := &fakeProvisioner{}
	// Override provisioner to return unique IDs.
	origAlloc := prov.allocation
	_ = origAlloc

	// Use a custom provisioner that returns distinct instance IDs.
	counter := &countingProvisioner{base: "i-new-"}
	m := NewManager(ps, counter, 3)

	if err := m.Replenish(ctx); err != nil {
		t.Fatalf("Replenish: %v", err)
	}
	_ = callCount
	if counter.calls != 2 {
		t.Errorf("provisioner called %d times, want 2", counter.calls)
	}

	n, _ := ps.CountByState(ctx, store.PoolStateAvailable)
	if n != 3 {
		t.Errorf("available count = %d, want 3", n)
	}
}

func TestManager_Replenish_ProvisionerError(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	ctx := context.Background()

	prov := &fakeProvisioner{provisionErr: errors.New("quota exceeded")}
	m := NewManager(ps, prov, 1)

	err := m.Replenish(ctx)
	if err == nil {
		t.Fatal("Replenish should return error when provisioner fails")
	}
}

func TestManager_Replenish_EmptyPool(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	ctx := context.Background()

	counter := &countingProvisioner{base: "i-fresh-"}
	m := NewManager(ps, counter, 2)

	if err := m.Replenish(ctx); err != nil {
		t.Fatalf("Replenish: %v", err)
	}
	if counter.calls != 2 {
		t.Errorf("provisioner called %d times, want 2", counter.calls)
	}
}

// countingProvisioner generates allocation with unique IDs using a counter.
type countingProvisioner struct {
	base  string
	calls int
}

func (c *countingProvisioner) ProvisionPoolMember(_ context.Context) (*store.ComputeAllocation, error) {
	c.calls++
	return &store.ComputeAllocation{
		InstanceID:      fmt.Sprintf("%s%d", c.base, c.calls),
		PoolMemberState: store.PoolStateAvailable,
	}, nil
}
