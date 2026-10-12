package cmd

import (
	"testing"

	"github.com/orchael/desktopctl/internal/store"
)

func TestDesktopSecretsCheckersExcludeAgentSecret(t *testing.T) {
	for _, tc := range []struct {
		name             string
		configuredAgent  string
		tracked          []string
		wantCheckerCount int
	}{
		{name: "agent-only", configuredAgent: "/agents", tracked: []string{"/agents"}},
		{name: "agent-and-desktop", configuredAgent: "/agents", tracked: []string{"/agents", "/application"}, wantCheckerCount: 2},
		{name: "configured-agent-not-tracked", configuredAgent: "/agents", tracked: []string{"/application"}, wantCheckerCount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desktop := &store.Desktop{Hostname: "desktop.example.com", Secrets: tc.tracked}
			if got := len(desktopSecretsCheckers(desktop, "/test/key", tc.configuredAgent)); got != tc.wantCheckerCount {
				t.Fatalf("checker count = %d, want %d", got, tc.wantCheckerCount)
			}
		})
	}
}
