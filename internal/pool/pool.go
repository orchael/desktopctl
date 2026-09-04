// Package pool defines the ComputePool abstraction for warm EC2 capacity.
// The initial implementation is EC2WarmPool (see ec2warmpool.go in Part 2).
// The interface is defined here so the booking path can be tested independently
// of EC2 API calls.
package pool

import "context"

// DesktopSpec describes the requirements for a compute allocation.
type DesktopSpec struct {
	DesktopID   string
	GitHubOwner string
	Region      string
	AMIID       string
}

// Machine is the compute resource returned by a successful pool acquisition.
type Machine struct {
	InstanceID string
	AMIID      string
	Region     string
}

// ComputePool is the abstraction over warm EC2 capacity.
// Implementations must be safe for concurrent use.
type ComputePool interface {
	// Acquire reserves an AVAILABLE pool member for the given desktop spec and
	// starts the instance. It returns the acquired machine or an error if no
	// capacity is available.
	Acquire(ctx context.Context, spec DesktopSpec) (*Machine, error)

	// Release returns a machine to the pool after the desktop is stopped.
	// The machine transitions to RECYCLING so it can be scrubbed before
	// becoming AVAILABLE again.
	Release(ctx context.Context, instanceID string) error

	// Recycle scrubs a machine (removing user data and credentials) and
	// returns it to AVAILABLE state. Called by the pool manager after Release.
	Recycle(ctx context.Context, instanceID string) error

	// AvailableCount returns the current number of AVAILABLE pool members.
	AvailableCount(ctx context.Context) (int, error)
}
