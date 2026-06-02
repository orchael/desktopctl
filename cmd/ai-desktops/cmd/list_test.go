package cmd

import (
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestFilterListedDesktops(t *testing.T) {
	desktops := []*store.Desktop{
		{DesktopID: "d-ready", State: store.StateReady},
		{DesktopID: "d-terminated", State: store.StateTerminated},
	}

	filtered := filterListedDesktops(desktops, false)
	if len(filtered) != 1 || filtered[0].DesktopID != "d-ready" {
		t.Fatalf("filtered desktops: got %#v", filtered)
	}

	all := filterListedDesktops(desktops, true)
	if len(all) != 2 {
		t.Fatalf("all desktops: got %d, want 2", len(all))
	}
}
