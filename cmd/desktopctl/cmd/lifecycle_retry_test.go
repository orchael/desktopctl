package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

func TestStartAndResizeClaimsReturnFreshFleetVersion(t *testing.T) {
	for _, next := range []store.LifecycleState{store.StateStarting, store.StateResizing} {
		t.Run(string(next), func(t *testing.T) {
			ctx := context.Background()
			s := store.NewInMemoryStore()
			original := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateReady, InstanceType: "m7i.large"}
			if err := s.Create(ctx, original); err != nil {
				t.Fatal(err)
			}
			stale, _ := s.Get(ctx, original.DesktopID)
			fresh, err := claimAndReadDesktop(ctx, s, stale, next)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.State != next || fresh.StateVersion != stale.StateVersion+1 {
				t.Fatalf("claim did not refresh record: %+v", fresh)
			}
			stale.InstanceType = "m8i.large"
			if err := s.Update(ctx, stale); !errors.Is(err, store.ErrStateConflict) {
				t.Fatalf("stale update was allowed: %v", err)
			}
			fresh.InstanceType = "m8i.large"
			if err := s.Update(ctx, fresh); err != nil {
				t.Fatalf("fresh update failed: %v", err)
			}
		})
	}
}

func TestInterruptedLifecycleRetryRequiresAge(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateStarting, UpdatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339)}
	if err := requireStaleLifecycle(d, "start", 30*time.Minute, now); err == nil {
		t.Fatal("active start was retried")
	}
	d.UpdatedAt = now.Add(-31 * time.Minute).Format(time.RFC3339)
	if err := requireStaleLifecycle(d, "start", 30*time.Minute, now); err != nil {
		t.Fatal(err)
	}
	d.UpdatedAt = ""
	if err := requireStaleLifecycle(d, "start", 30*time.Minute, now); err == nil {
		t.Fatal("missing timestamp was accepted")
	}
}

func TestLifecycleRetryClaimsFenceOverlappingWorkers(t *testing.T) {
	for _, tc := range []struct {
		state store.LifecycleState
		phase string
		final store.LifecycleState
	}{
		{store.StateStarting, "start", store.StateReady},
		{store.StateStopping, "stop", store.StateStopped},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := context.Background()
			s := store.NewInMemoryStore()
			d := &store.Desktop{DesktopID: "d-0123abcd", State: tc.state}
			if err := s.Create(ctx, d); err != nil {
				t.Fatal(err)
			}
			observed, _ := s.Get(ctx, d.DesktopID)
			_, first, err := claimLifecycleOwner(ctx, s, observed, tc.state)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := claimLifecycleOwner(ctx, s, observed, tc.state); !errors.Is(err, store.ErrStateConflict) {
				t.Fatalf("overlap claim: %v", err)
			}
			fresh, _ := s.Get(ctx, d.DesktopID)
			_, second, err := claimLifecycleOwner(ctx, s, fresh, tc.state)
			if err != nil {
				t.Fatal(err)
			}
			if err := recordLifecycleFailure(ctx, s, d.DesktopID, first, tc.phase, "late failure"); !errors.Is(err, store.ErrStateConflict) {
				t.Fatalf("stale failure: %v", err)
			}
			if err := completeLifecycle(ctx, s, d.DesktopID, first, tc.state, tc.final, "", ""); !errors.Is(err, store.ErrStateConflict) {
				t.Fatalf("stale completion: %v", err)
			}
			if err := completeLifecycle(ctx, s, d.DesktopID, second, tc.state, tc.final, "", ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}
