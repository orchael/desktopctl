package pool

import (
	"context"
	"testing"
)

func TestDesktopSpec(t *testing.T) {
	spec := DesktopSpec{
		DesktopID:   "d-001",
		GitHubOwner: "alice",
		Region:      "us-east-1",
		AMIID:       "ami-abc123",
	}
	if spec.DesktopID == "" {
		t.Error("DesktopID must not be empty")
	}
}

func TestMachine(t *testing.T) {
	m := Machine{
		InstanceID: "i-001",
		AMIID:      "ami-abc123",
		Region:     "us-east-1",
	}
	if m.InstanceID == "" {
		t.Error("InstanceID must not be empty")
	}
}

// TestComputePoolInterface is a compile-time check that noopPool satisfies
// ComputePool. No concrete EC2 implementation exists in Part 1.
func TestComputePoolInterface(t *testing.T) {
	var _ ComputePool = (*noopPool)(nil)
}

type noopPool struct{}

func (n *noopPool) Acquire(_ context.Context, _ DesktopSpec) (*Machine, error) { return nil, nil }
func (n *noopPool) Release(_ context.Context, _ string) error                  { return nil }
func (n *noopPool) Recycle(_ context.Context, _ string) error                  { return nil }
func (n *noopPool) AvailableCount(_ context.Context) (int, error)              { return 0, nil }
