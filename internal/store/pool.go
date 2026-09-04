package store

import (
	"context"
	"errors"
	"sync"
)

// PoolMemberState is the lifecycle state of a warm-pool compute member.
type PoolMemberState string

const (
	PoolStateAvailable  PoolMemberState = "AVAILABLE"
	PoolStateAllocating PoolMemberState = "ALLOCATING"
	PoolStateStarting   PoolMemberState = "STARTING"
	PoolStateReady      PoolMemberState = "READY"
	PoolStateInUse      PoolMemberState = "IN_USE"
	PoolStateRecycling  PoolMemberState = "RECYCLING"
	PoolStateFailed     PoolMemberState = "FAILED"
)

// ComputeAllocation records the binding between a warm-pool EC2 instance and
// the logical Desktop that acquired it. When DesktopID is empty the member is
// available for acquisition.
type ComputeAllocation struct {
	InstanceID      string          `dynamodbav:"instance_id"                json:"instance_id"`
	PoolMemberState PoolMemberState `dynamodbav:"pool_state"                 json:"pool_state"`
	AMIID           string          `dynamodbav:"ami_id,omitempty"           json:"ami_id,omitempty"`
	ImageVersion    string          `dynamodbav:"image_version,omitempty"    json:"image_version,omitempty"`
	DesktopID       string          `dynamodbav:"desktop_id,omitempty"       json:"desktop_id,omitempty"`
	AllocatedAt     string          `dynamodbav:"allocated_at,omitempty"     json:"allocated_at,omitempty"`
	CreatedAt       string          `dynamodbav:"created_at"                 json:"created_at"`
	UpdatedAt       string          `dynamodbav:"updated_at"                 json:"updated_at"`
	FailureMsg      string          `dynamodbav:"failure_message,omitempty"  json:"failure_message,omitempty"`
}

var (
	ErrPoolMemberNotFound  = errors.New("pool member not found")
	ErrNoAvailableCapacity = errors.New("no available pool capacity")
)

// PoolStore is the persistence interface for warm-pool compute members.
type PoolStore interface {
	CreatePoolMember(ctx context.Context, a *ComputeAllocation) error
	GetPoolMember(ctx context.Context, instanceID string) (*ComputeAllocation, error)
	ListPoolMembers(ctx context.Context) ([]*ComputeAllocation, error)
	UpdatePoolMember(ctx context.Context, a *ComputeAllocation) error
	DeletePoolMember(ctx context.Context, instanceID string) error
	// AcquireAvailable atomically transitions one AVAILABLE member to ALLOCATING,
	// sets its DesktopID and AllocatedAt, and returns it.
	// Returns ErrNoAvailableCapacity when the pool has no AVAILABLE members.
	AcquireAvailable(ctx context.Context, desktopID string) (*ComputeAllocation, error)
	// CountByState returns the number of pool members in the given state.
	CountByState(ctx context.Context, state PoolMemberState) (int, error)
}

// InMemoryPoolStore is a thread-safe in-memory implementation of PoolStore,
// used in tests and local development without AWS credentials.
type InMemoryPoolStore struct {
	mu      sync.Mutex
	members map[string]*ComputeAllocation
}

func NewInMemoryPoolStore() *InMemoryPoolStore {
	return &InMemoryPoolStore{members: make(map[string]*ComputeAllocation)}
}

func (s *InMemoryPoolStore) CreatePoolMember(_ context.Context, a *ComputeAllocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := now()
	if a.CreatedAt == "" {
		a.CreatedAt = ts
	}
	a.UpdatedAt = ts
	cp := *a
	s.members[a.InstanceID] = &cp
	return nil
}

func (s *InMemoryPoolStore) GetPoolMember(_ context.Context, instanceID string) (*ComputeAllocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.members[instanceID]
	if !ok {
		return nil, ErrPoolMemberNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *InMemoryPoolStore) ListPoolMembers(_ context.Context) ([]*ComputeAllocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*ComputeAllocation, 0, len(s.members))
	for _, a := range s.members {
		cp := *a
		result = append(result, &cp)
	}
	return result, nil
}

func (s *InMemoryPoolStore) UpdatePoolMember(_ context.Context, a *ComputeAllocation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.members[a.InstanceID]; !ok {
		return ErrPoolMemberNotFound
	}
	a.UpdatedAt = now()
	cp := *a
	s.members[a.InstanceID] = &cp
	return nil
}

func (s *InMemoryPoolStore) DeletePoolMember(_ context.Context, instanceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.members[instanceID]; !ok {
		return ErrPoolMemberNotFound
	}
	delete(s.members, instanceID)
	return nil
}

func (s *InMemoryPoolStore) AcquireAvailable(_ context.Context, desktopID string) (*ComputeAllocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.members {
		if a.PoolMemberState == PoolStateAvailable {
			ts := now()
			a.PoolMemberState = PoolStateAllocating
			a.DesktopID = desktopID
			a.AllocatedAt = ts
			a.UpdatedAt = ts
			cp := *a
			return &cp, nil
		}
	}
	return nil, ErrNoAvailableCapacity
}

func (s *InMemoryPoolStore) CountByState(_ context.Context, state PoolMemberState) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, a := range s.members {
		if a.PoolMemberState == state {
			n++
		}
	}
	return n, nil
}
