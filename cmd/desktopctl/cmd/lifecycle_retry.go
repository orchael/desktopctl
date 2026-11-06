package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

func claimAndReadDesktop(ctx context.Context, s store.Store, d *store.Desktop, next store.LifecycleState) (*store.Desktop, error) {
	if err := s.Transition(ctx, d.DesktopID, d.State, next); err != nil {
		return nil, err
	}
	return s.Get(ctx, d.DesktopID)
}

// Interrupted operations are retried only after their normal AWS waiter could
// have finished. This prevents an immediate second process from racing one
// that is still active. The operator must also verify that the old process has
// exited before choosing the explicit retry flag.
func requireStaleLifecycle(d *store.Desktop, operation string, minimumAge time.Duration, now time.Time) error {
	updated, err := time.Parse(time.RFC3339, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("desktop %s has no trustworthy lifecycle timestamp; inspect it before retrying %s", d.DesktopID, operation)
	}
	if now.Sub(updated) < minimumAge {
		return fmt.Errorf("desktop %s %s operation may still be active; retry after %s and confirm the old process exited", d.DesktopID, operation, minimumAge)
	}
	return nil
}
