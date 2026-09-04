package pool

import (
	"context"
	"fmt"

	"github.com/orchael/ai-desktops/internal/store"
)

// Provisioner creates a new pre-configured pool member (stopped EC2 instance)
// and returns its allocation record. The caller is responsible for adding the
// record to the PoolStore.
type Provisioner interface {
	ProvisionPoolMember(ctx context.Context) (*store.ComputeAllocation, error)
}

// Manager maintains the desired number of AVAILABLE pool members by calling
// Provisioner when the pool is below the target size.
type Manager struct {
	store       store.PoolStore
	provisioner Provisioner
	desiredSize int
}

// NewManager creates a Manager that targets desiredSize AVAILABLE members.
func NewManager(s store.PoolStore, p Provisioner, desiredSize int) *Manager {
	return &Manager{store: s, provisioner: p, desiredSize: desiredSize}
}

// Replenish checks the current AVAILABLE count and provisions new members until
// the pool reaches desiredSize. It stops immediately on the first provisioning
// error.
func (m *Manager) Replenish(ctx context.Context) error {
	current, err := m.store.CountByState(ctx, store.PoolStateAvailable)
	if err != nil {
		return fmt.Errorf("count available pool members: %w", err)
	}

	for i := current; i < m.desiredSize; i++ {
		a, err := m.provisioner.ProvisionPoolMember(ctx)
		if err != nil {
			return fmt.Errorf("provision pool member: %w", err)
		}
		if err := m.store.CreatePoolMember(ctx, a); err != nil {
			return fmt.Errorf("store new pool member %q: %w", a.InstanceID, err)
		}
	}
	return nil
}
