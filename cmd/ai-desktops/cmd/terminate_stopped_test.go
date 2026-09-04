package cmd

import (
	"testing"
	"time"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestTerminateStoppedDryRunOutput(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	desktops := []*store.Desktop{
		{
			DesktopID:   "d-aaa",
			GitHubOwner: "alice",
			Region:      "us-east-1",
			InstanceID:  "i-001",
			State:       store.StateStopped,
			StoppedAt:   now.Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339),
		},
		{
			DesktopID:   "d-bbb",
			GitHubOwner: "bob",
			Region:      "us-west-2",
			InstanceID:  "i-002",
			State:       store.StateStopped,
			StoppedAt:   now.Add(-15 * 24 * time.Hour).UTC().Format(time.RFC3339),
		},
	}

	lines := buildTerminateStoppedDryRunLines(desktops, now)

	if len(lines) != 2 {
		t.Fatalf("buildTerminateStoppedDryRunLines returned %d lines, want 2", len(lines))
	}
	for i, d := range desktops {
		if lines[i] != d.DesktopID {
			t.Fatalf("line[%d] = %q, want %q", i, lines[i], d.DesktopID)
		}
	}
}

func TestBuildTerminateStoppedDryRunLines_empty(t *testing.T) {
	lines := buildTerminateStoppedDryRunLines(nil, time.Now())
	if len(lines) != 0 {
		t.Fatalf("expected empty, got %v", lines)
	}
}
