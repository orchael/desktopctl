package pool

import (
	"context"
	"fmt"

	"github.com/orchael/ai-desktops/internal/store"
)

// EC2Ops is the minimal EC2 API surface needed by the warm pool.
type EC2Ops interface {
	StartInstance(ctx context.Context, instanceID string) error
	StopInstance(ctx context.Context, instanceID string) error
}

// EC2WarmPool manages a pool of pre-provisioned EC2 instances.
type EC2WarmPool struct {
	store store.PoolStore
	ec2   EC2Ops
}

// NewEC2WarmPool creates an EC2WarmPool backed by the given store and EC2 client.
func NewEC2WarmPool(s store.PoolStore, ec2 EC2Ops) *EC2WarmPool {
	return &EC2WarmPool{store: s, ec2: ec2}
}

// Acquire reserves an AVAILABLE pool member for spec, starts its EC2 instance,
// and transitions it to READY. If the instance fails to start the member is
// marked FAILED and the error is returned.
func (p *EC2WarmPool) Acquire(ctx context.Context, spec DesktopSpec) (*Machine, error) {
	a, err := p.store.AcquireAvailable(ctx, spec.DesktopID)
	if err != nil {
		return nil, err
	}

	if startErr := p.ec2.StartInstance(ctx, a.InstanceID); startErr != nil {
		a.PoolMemberState = store.PoolStateFailed
		a.FailureMsg = startErr.Error()
		_ = p.store.UpdatePoolMember(ctx, a)
		return nil, fmt.Errorf("start instance %s: %w", a.InstanceID, startErr)
	}

	a.PoolMemberState = store.PoolStateReady
	if err := p.store.UpdatePoolMember(ctx, a); err != nil {
		return nil, fmt.Errorf("update pool member after start: %w", err)
	}

	return &Machine{
		InstanceID: a.InstanceID,
		AMIID:      a.AMIID,
	}, nil
}

// Release transitions a pool member to RECYCLING so the pool manager can
// scrub it before returning it to AVAILABLE.
func (p *EC2WarmPool) Release(ctx context.Context, instanceID string) error {
	a, err := p.store.GetPoolMember(ctx, instanceID)
	if err != nil {
		return err
	}
	a.PoolMemberState = store.PoolStateRecycling
	return p.store.UpdatePoolMember(ctx, a)
}

// Recycle stops the EC2 instance, clears desktop-specific data, and returns
// the member to AVAILABLE. If the stop call fails the member is marked FAILED.
func (p *EC2WarmPool) Recycle(ctx context.Context, instanceID string) error {
	a, err := p.store.GetPoolMember(ctx, instanceID)
	if err != nil {
		return err
	}

	if stopErr := p.ec2.StopInstance(ctx, instanceID); stopErr != nil {
		a.PoolMemberState = store.PoolStateFailed
		a.FailureMsg = stopErr.Error()
		_ = p.store.UpdatePoolMember(ctx, a)
		return fmt.Errorf("stop instance %s: %w", instanceID, stopErr)
	}

	a.PoolMemberState = store.PoolStateAvailable
	a.DesktopID = ""
	a.AllocatedAt = ""
	a.FailureMsg = ""
	return p.store.UpdatePoolMember(ctx, a)
}

// AvailableCount returns the number of AVAILABLE pool members.
func (p *EC2WarmPool) AvailableCount(ctx context.Context) (int, error) {
	return p.store.CountByState(ctx, store.PoolStateAvailable)
}
