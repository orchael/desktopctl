package cmd

import (
	"strings"
	"testing"
)

func TestParseAndValidateRepos(t *testing.T) {
	tests := []struct {
		name        string
		owner       string
		rawRepos    []string
		wantOwner   string
		wantCount   int
		wantErr     bool
		errContains string
	}{
		{
			name:      "no repos, owner provided",
			owner:     "acme",
			rawRepos:  nil,
			wantOwner: "acme",
			wantCount: 0,
		},
		{
			name:      "no repos, no owner",
			owner:     "",
			rawRepos:  nil,
			wantOwner: "",
			wantCount: 0,
		},
		{
			name:      "repos provided, owner inferred",
			owner:     "",
			rawRepos:  []string{"github.com/acme/app-one", "github.com/acme/app-two"},
			wantOwner: "acme",
			wantCount: 2,
		},
		{
			name:      "repos provided, owner matches flag",
			owner:     "acme",
			rawRepos:  []string{"github.com/acme/app-one"},
			wantOwner: "acme",
			wantCount: 1,
		},
		{
			name:      "repos provided, owner matches flag case-insensitively",
			owner:     "ACME",
			rawRepos:  []string{"github.com/acme/app-one"},
			wantOwner: "acme", // canonical owner from the repo URL, not the flag value
			wantCount: 1,
		},
		{
			name:        "repos provided, owner mismatch",
			owner:       "other",
			rawRepos:    []string{"github.com/acme/app-one"},
			wantErr:     true,
			errContains: "does not match --github-owner",
		},
		{
			name:     "repos provided, invalid URL",
			owner:    "",
			rawRepos: []string{"https://gitlab.com/acme/app"},
			wantErr:  true,
		},
		{
			name:     "repos provided, mixed owners",
			owner:    "",
			rawRepos: []string{"github.com/acme/app-one", "github.com/other/app-two"},
			wantErr:  true,
		},
		{
			name:      "single SSH repo, owner inferred",
			owner:     "",
			rawRepos:  []string{"git@github.com:myorg/myrepo.git"},
			wantOwner: "myorg",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos, owner, err := parseAndValidateRepos(tt.owner, tt.rawRepos)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", owner, tt.wantOwner)
			}
			if len(repos) != tt.wantCount {
				t.Errorf("repo count: got %d, want %d", len(repos), tt.wantCount)
			}
		})
	}
}
