package cmd

import (
	"testing"

	"github.com/orchael/desktopctl/internal/store"
)

func TestFilterListedDesktops(t *testing.T) {
	desktops := []*store.Desktop{
		{DesktopID: "d-ready", OrganizationID: "org-1", State: store.StateReady},
		{DesktopID: "d-terminated", OrganizationID: "org-1", State: store.StateTerminated},
	}

	filtered := filterListedDesktops(desktops, false, "")
	if len(filtered) != 1 || filtered[0].DesktopID != "d-ready" {
		t.Fatalf("filtered desktops: got %#v", filtered)
	}

	all := filterListedDesktops(desktops, true, "")
	if len(all) != 2 {
		t.Fatalf("all desktops: got %d, want 2", len(all))
	}
}

func TestFilterListedDesktops_OrganizationID(t *testing.T) {
	desktops := []*store.Desktop{
		{DesktopID: "d-org-1-ready", OrganizationID: "org-1", State: store.StateReady},
		{DesktopID: "d-org-1-terminated", OrganizationID: "org-1", State: store.StateTerminated},
		{DesktopID: "d-org-2-ready", OrganizationID: "org-2", State: store.StateReady},
		{DesktopID: "d-unscoped", State: store.StateReady},
	}

	filtered := filterListedDesktops(desktops, false, "org-1")
	if len(filtered) != 1 || filtered[0].DesktopID != "d-org-1-ready" {
		t.Fatalf("filtered desktops: got %#v", filtered)
	}

	all := filterListedDesktops(desktops, true, "org-1")
	if len(all) != 2 {
		t.Fatalf("all org-1 desktops: got %d, want 2", len(all))
	}
}
