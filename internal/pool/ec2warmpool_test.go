package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

// fakeEC2Ops is a controllable EC2Ops double for unit tests.
type fakeEC2Ops struct {
	startErr error
	stopErr  error
	started  []string
	stopped  []string
}

func (f *fakeEC2Ops) StartInstance(_ context.Context, instanceID string) error {
	f.started = append(f.started, instanceID)
	return f.startErr
}

func (f *fakeEC2Ops) StopInstance(_ context.Context, instanceID string) error {
	f.stopped = append(f.stopped, instanceID)
	return f.stopErr
}

func newPoolWithMember(state store.PoolMemberState) (*EC2WarmPool, *store.InMemoryPoolStore, *fakeEC2Ops) {
	ps := store.NewInMemoryPoolStore()
	_ = ps.CreatePoolMember(context.Background(), &store.ComputeAllocation{
		InstanceID:      "i-001",
		PoolMemberState: state,
		AMIID:           "ami-test",
	})
	ec2 := &fakeEC2Ops{}
	pool := NewEC2WarmPool(ps, ec2)
	return pool, ps, ec2
}

func TestEC2WarmPool_Acquire_success(t *testing.T) {
	p, ps, ec2 := newPoolWithMember(store.PoolStateAvailable)
	ctx := context.Background()

	m, err := p.Acquire(ctx, DesktopSpec{DesktopID: "d-001", Region: "us-east-1"})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if m.InstanceID != "i-001" {
		t.Errorf("Machine.InstanceID = %q, want %q", m.InstanceID, "i-001")
	}
	if len(ec2.started) != 1 || ec2.started[0] != "i-001" {
		t.Errorf("StartInstance not called correctly: %v", ec2.started)
	}

	// State must be READY after successful acquire.
	got, _ := ps.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != store.PoolStateReady {
		t.Errorf("state after acquire = %q, want READY", got.PoolMemberState)
	}
	if got.DesktopID != "d-001" {
		t.Errorf("DesktopID = %q, want %q", got.DesktopID, "d-001")
	}
}

func TestEC2WarmPool_Acquire_noCapacity(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	p := NewEC2WarmPool(ps, &fakeEC2Ops{})
	_, err := p.Acquire(context.Background(), DesktopSpec{DesktopID: "d-001"})
	if !errors.Is(err, store.ErrNoAvailableCapacity) {
		t.Fatalf("empty pool: got %v, want ErrNoAvailableCapacity", err)
	}
}

func TestEC2WarmPool_Acquire_startFails(t *testing.T) {
	p, ps, _ := newPoolWithMember(store.PoolStateAvailable)
	// Inject start error after pool is set up.
	ec2Fail := &fakeEC2Ops{startErr: errors.New("EC2 throttled")}
	p.ec2 = ec2Fail
	ctx := context.Background()

	_, err := p.Acquire(ctx, DesktopSpec{DesktopID: "d-fail"})
	if err == nil {
		t.Fatal("Acquire with failing EC2 should return error")
	}

	// Member must be marked FAILED.
	got, _ := ps.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != store.PoolStateFailed {
		t.Errorf("state after failed acquire = %q, want FAILED", got.PoolMemberState)
	}
	if got.FailureMsg == "" {
		t.Error("FailureMsg should be set on failed acquire")
	}
}

func TestEC2WarmPool_Release(t *testing.T) {
	p, ps, _ := newPoolWithMember(store.PoolStateInUse)
	ctx := context.Background()

	if err := p.Release(ctx, "i-001"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	got, _ := ps.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != store.PoolStateRecycling {
		t.Errorf("state after release = %q, want RECYCLING", got.PoolMemberState)
	}
}

func TestEC2WarmPool_Release_notFound(t *testing.T) {
	p := NewEC2WarmPool(store.NewInMemoryPoolStore(), &fakeEC2Ops{})
	err := p.Release(context.Background(), "i-missing")
	if !errors.Is(err, store.ErrPoolMemberNotFound) {
		t.Fatalf("Release missing: got %v, want ErrPoolMemberNotFound", err)
	}
}

func TestEC2WarmPool_Recycle_success(t *testing.T) {
	p, ps, ec2 := newPoolWithMember(store.PoolStateRecycling)
	// Set a desktop ID to verify it gets cleared.
	a, _ := ps.GetPoolMember(context.Background(), "i-001")
	a.DesktopID = "d-old"
	_ = ps.UpdatePoolMember(context.Background(), a)
	ctx := context.Background()

	if err := p.Recycle(ctx, "i-001"); err != nil {
		t.Fatalf("Recycle: %v", err)
	}
	if len(ec2.stopped) != 1 || ec2.stopped[0] != "i-001" {
		t.Errorf("StopInstance not called correctly: %v", ec2.stopped)
	}

	got, _ := ps.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != store.PoolStateAvailable {
		t.Errorf("state after recycle = %q, want AVAILABLE", got.PoolMemberState)
	}
	if got.DesktopID != "" {
		t.Errorf("DesktopID should be cleared after recycle, got %q", got.DesktopID)
	}
}

func TestEC2WarmPool_Recycle_stopFails(t *testing.T) {
	p, ps, _ := newPoolWithMember(store.PoolStateRecycling)
	p.ec2 = &fakeEC2Ops{stopErr: errors.New("stop failed")}
	ctx := context.Background()

	if err := p.Recycle(ctx, "i-001"); err == nil {
		t.Fatal("Recycle with failing stop should return error")
	}
	got, _ := ps.GetPoolMember(ctx, "i-001")
	if got.PoolMemberState != store.PoolStateFailed {
		t.Errorf("state after failed recycle = %q, want FAILED", got.PoolMemberState)
	}
}

func TestEC2WarmPool_AvailableCount(t *testing.T) {
	ps := store.NewInMemoryPoolStore()
	ctx := context.Background()
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-001", PoolMemberState: store.PoolStateAvailable})
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-002", PoolMemberState: store.PoolStateAvailable})
	_ = ps.CreatePoolMember(ctx, &store.ComputeAllocation{InstanceID: "i-003", PoolMemberState: store.PoolStateInUse})

	p := NewEC2WarmPool(ps, &fakeEC2Ops{})
	n, err := p.AvailableCount(ctx)
	if err != nil {
		t.Fatalf("AvailableCount: %v", err)
	}
	if n != 2 {
		t.Errorf("AvailableCount = %d, want 2", n)
	}
}
