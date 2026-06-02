package cmd

import (
	"context"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestPurgeTerminated(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-ready", State: store.StateReady})
	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-terminated", State: store.StateTerminated})

	purged, err := purgeTerminated(ctx, s, mustList(t, ctx, s), false)
	if err != nil {
		t.Fatalf("purgeTerminated: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged: got %d, want 1", purged)
	}
	if _, err := s.Get(ctx, "d-terminated"); err == nil {
		t.Fatal("terminated desktop still exists")
	}
	if _, err := s.Get(ctx, "d-ready"); err != nil {
		t.Fatalf("ready desktop was deleted: %v", err)
	}
}

func TestPurgeTerminatedDryRun(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	_ = s.Create(ctx, &store.Desktop{DesktopID: "d-terminated", State: store.StateTerminated})

	purged, err := purgeTerminated(ctx, s, mustList(t, ctx, s), true)
	if err != nil {
		t.Fatalf("purgeTerminated: %v", err)
	}
	if purged != 1 {
		t.Fatalf("purged: got %d, want 1", purged)
	}
	if _, err := s.Get(ctx, "d-terminated"); err != nil {
		t.Fatalf("dry run deleted desktop: %v", err)
	}
}

func mustList(t *testing.T, ctx context.Context, s store.Store) []*store.Desktop {
	t.Helper()
	desktops, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return desktops
}
