package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

func claimLifecycleOwner(ctx context.Context, s store.Store, observed *store.Desktop, next store.LifecycleState) (*store.Desktop, string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(bytes[:])
	if observed.State != next {
		if err := s.Transition(ctx, observed.DesktopID, observed.State, next); err != nil {
			return nil, "", err
		}
	}
	d, err := s.Get(ctx, observed.DesktopID)
	if err != nil {
		return nil, "", err
	}
	if d.State != next || (observed.State == next && d.StateVersion != observed.StateVersion) {
		return nil, "", store.ErrStateConflict
	}
	d.LifecycleOperationToken = token
	if err := s.Update(ctx, d); err != nil {
		return nil, "", err
	}
	return d, token, nil
}

func recordLifecycleFailure(ctx context.Context, s store.Store, id, token, phase, message string) error {
	recorder, ok := s.(interface {
		RecordLifecycleFailure(context.Context, string, string, string, string) error
	})
	if !ok {
		return fmt.Errorf("fleet store lacks fenced lifecycle failure support")
	}
	return recorder.RecordLifecycleFailure(ctx, id, token, phase, message)
}

func completeLifecycle(ctx context.Context, s store.Store, id, token string, from, to store.LifecycleState, readiness, stopReason string) error {
	d, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if d.State != from || d.LifecycleOperationToken != token {
		return store.ErrStateConflict
	}
	d.State, d.LifecycleOperationToken = to, ""
	d.Readiness = readiness
	d.StopReason = stopReason
	if to == store.StateStopped {
		d.StoppedAt = time.Now().UTC().Format(time.RFC3339)
	} else {
		d.StoppedAt = ""
	}
	d.FailurePhase, d.FailureMsg = "", ""
	return s.Update(ctx, d)
}

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
