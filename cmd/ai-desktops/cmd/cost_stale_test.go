package cmd

import (
	"testing"
	"time"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestParseStaleDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{input: "7d", want: 7 * 24 * time.Hour},
		{input: "14d", want: 14 * 24 * time.Hour},
		{input: "1d", want: 24 * time.Hour},
		{input: "30d", want: 30 * 24 * time.Hour},
		{input: "24h", want: 24 * time.Hour},
		{input: "1h", want: time.Hour},
		{input: "0d", wantErr: true},
		{input: "-1d", wantErr: true},
		{input: "d", wantErr: true},
		{input: "", wantErr: true},
		{input: "abc", wantErr: true},
		{input: "7", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseStaleDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseStaleDuration(%q) = %v, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStaleDuration(%q) error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseStaleDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFilterStaleDesktops(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	threshold := 7 * 24 * time.Hour

	stoppedOld := &store.Desktop{
		DesktopID: "old",
		State:     store.StateStopped,
		StoppedAt: now.Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	stoppedRecent := &store.Desktop{
		DesktopID: "recent",
		State:     store.StateStopped,
		StoppedAt: now.Add(-3 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	stoppedExact := &store.Desktop{
		DesktopID: "exact",
		State:     store.StateStopped,
		StoppedAt: now.Add(-7 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	running := &store.Desktop{
		DesktopID: "running",
		State:     store.StateReady,
		StoppedAt: now.Add(-10 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	terminated := &store.Desktop{
		DesktopID: "terminated",
		State:     store.StateTerminated,
		StoppedAt: now.Add(-20 * 24 * time.Hour).UTC().Format(time.RFC3339),
	}
	noStoppedAt := &store.Desktop{
		DesktopID: "nostop",
		State:     store.StateStopped,
		StoppedAt: "",
	}

	all := []*store.Desktop{stoppedOld, stoppedRecent, stoppedExact, running, terminated, noStoppedAt}
	got := filterStaleDesktops(all, threshold, now)

	if len(got) != 1 {
		t.Fatalf("filterStaleDesktops returned %d desktops, want 1: %v", len(got), desktopIDs(got))
	}
	if got[0].DesktopID != "old" {
		t.Fatalf("filterStaleDesktops returned %q, want %q", got[0].DesktopID, "old")
	}
}

func TestFormatStoppedAge(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		stoppedAt string
		want      string
	}{
		{stoppedAt: now.Add(-8 * 24 * time.Hour).UTC().Format(time.RFC3339), want: "8d 0h"},
		{stoppedAt: now.Add(-1 * 24 * time.Hour).UTC().Format(time.RFC3339), want: "1d 0h"},
		{stoppedAt: now.Add(-36 * time.Hour).UTC().Format(time.RFC3339), want: "1d 12h"},
		{stoppedAt: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339), want: "0d 2h"},
		{stoppedAt: "", want: "unknown"},
		{stoppedAt: "not-a-time", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatStoppedAge(tt.stoppedAt, now); got != tt.want {
				t.Fatalf("formatStoppedAge(%q) = %q, want %q", tt.stoppedAt, got, tt.want)
			}
		})
	}
}

func TestEBSMonthlyCostGiB(t *testing.T) {
	tests := []struct {
		gib  int
		want float64
	}{
		{gib: 64, want: 5.12},
		{gib: 200, want: 16.00},
		{gib: 0, want: 0},
	}
	for _, tt := range tests {
		got := ebsMonthlyCostGiB(tt.gib)
		if got != tt.want {
			t.Fatalf("ebsMonthlyCostGiB(%d) = %f, want %f", tt.gib, got, tt.want)
		}
	}
}

// desktopIDs is a test helper.
func desktopIDs(desktops []*store.Desktop) []string {
	ids := make([]string, len(desktops))
	for i, d := range desktops {
		ids[i] = d.DesktopID
	}
	return ids
}
