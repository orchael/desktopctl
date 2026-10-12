package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/store"
)

type fakeProfileBootstrapRunner struct {
	onlineChecks int
	results      []string
	startErr     error
}

func (f *fakeProfileBootstrapRunner) Online(context.Context, string) (bool, error) {
	f.onlineChecks++
	return f.onlineChecks > 1, nil
}

func (f *fakeProfileBootstrapRunner) Start(context.Context, string) (string, error) {
	return "command-id", f.startErr
}

func (f *fakeProfileBootstrapRunner) Result(context.Context, string, string) (string, error) {
	if len(f.results) == 0 {
		return "Pending", nil
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result, nil
}

func TestCompleteCreateReadinessWaitsForSelectedProfile(t *testing.T) {
	for _, tc := range []struct {
		name, profile, result string
		wantState             store.LifecycleState
		wantError             bool
	}{
		{name: "no profile", wantState: store.StateReady},
		{name: "profile succeeded", profile: "markcallen/ai-desktop-profile", result: "Success", wantState: store.StateReady},
		{name: "profile failed", profile: "markcallen/ai-desktop-profile", result: "Failed", wantState: store.StateFailed, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := store.NewInMemoryStore()
			ctx := context.Background()
			if err := s.Create(ctx, &store.Desktop{DesktopID: "d-profile-test", State: store.StateCreating}); err != nil {
				t.Fatal(err)
			}
			runner := &fakeProfileBootstrapRunner{results: []string{"InProgress", tc.result}}
			err := completeCreateReadiness(ctx, desktop.NewManager(s), "d-profile-test", tc.profile, "i-123", runner, time.Millisecond)
			if (err != nil) != tc.wantError {
				t.Fatalf("error = %v, wantError %v", err, tc.wantError)
			}
			got, err := s.Get(ctx, "d-profile-test")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			if tc.profile != "" && runner.onlineChecks < 2 {
				t.Fatal("did not wait for SSM to become online")
			}
			if tc.wantError && (got.FailurePhase != "desktop-profile" || !strings.Contains(got.FailureMsg, "bootstrap")) {
				t.Fatalf("failure was not recorded: %+v", got)
			}
		})
	}
}

func TestCompleteCreateReadinessStartFailure(t *testing.T) {
	s := store.NewInMemoryStore()
	ctx := context.Background()
	if err := s.Create(ctx, &store.Desktop{DesktopID: "d-profile-test", State: store.StateCreating}); err != nil {
		t.Fatal(err)
	}
	runner := &fakeProfileBootstrapRunner{startErr: errors.New("SSM unavailable")}
	if err := completeCreateReadiness(ctx, desktop.NewManager(s), "d-profile-test", "owner/repo", "i-123", runner, time.Millisecond); err == nil {
		t.Fatal("expected SSM failure")
	}
	got, _ := s.Get(ctx, "d-profile-test")
	if got.State != store.StateFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
}
