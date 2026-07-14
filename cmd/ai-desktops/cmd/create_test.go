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

func TestParseAVDs(t *testing.T) {
	tests := []struct {
		name        string
		specs       []string
		wantLen     int
		wantErr     bool
		errContains string
	}{
		{
			name:    "name:image",
			specs:   []string{"flutter_dev:system-images;android-35;google_apis;x86_64"},
			wantLen: 1,
		},
		{
			name:    "name:image:device",
			specs:   []string{"flutter_dev:system-images;android-35;google_apis;x86_64:pixel_6"},
			wantLen: 1,
		},
		{
			name:    "multiple AVDs",
			specs:   []string{"avd1:system-images;android-35;google_apis;x86_64", "avd2:system-images;android-33;google_apis;x86_64:pixel_4"},
			wantLen: 2,
		},
		{
			name:        "missing image",
			specs:       []string{"flutter_dev"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:        "empty name",
			specs:       []string{":system-images;android-35;google_apis;x86_64"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:        "empty image",
			specs:       []string{"flutter_dev:"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:    "empty list",
			specs:   []string{},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAVDs(tt.specs)
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
			if len(got) != tt.wantLen {
				t.Errorf("len(got) = %d, want %d", len(got), tt.wantLen)
			}
		})
	}

	t.Run("fields are populated correctly", func(t *testing.T) {
		got, err := parseAVDs([]string{"my_avd:system-images;android-35;google_apis;x86_64:pixel_6"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 AVD, got %d", len(got))
		}
		if got[0].Name != "my_avd" {
			t.Errorf("Name = %q, want %q", got[0].Name, "my_avd")
		}
		if got[0].Image != "system-images;android-35;google_apis;x86_64" {
			t.Errorf("Image = %q, want %q", got[0].Image, "system-images;android-35;google_apis;x86_64")
		}
		if got[0].Device != "pixel_6" {
			t.Errorf("Device = %q, want %q", got[0].Device, "pixel_6")
		}
	})

	t.Run("device is optional", func(t *testing.T) {
		got, err := parseAVDs([]string{"my_avd:system-images;android-35;google_apis;x86_64"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Device != "" {
			t.Errorf("Device should be empty when not specified, got %q", got[0].Device)
		}
	})
}

func TestResolveSwapSize(t *testing.T) {
	tests := []struct {
		name         string
		flagValue    int
		instanceType string
		volumeGiB    int
		wantSwap     int
		wantErr      bool
		errContains  string
	}{
		{
			name:         "disabled with -1",
			flagValue:    -1,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     0,
		},
		{
			name:         "values below -1 are rejected",
			flagValue:    -2,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantErr:      true,
			errContains:  "invalid --swap-size",
		},
		{
			name:         "auto: 2x memory for t3.large (8 GiB RAM → 16 GiB swap)",
			flagValue:    0,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     16,
		},
		{
			name:         "auto: 2x memory for m5.xlarge (16 GiB RAM → 32 GiB swap)",
			flagValue:    0,
			instanceType: "m5.xlarge",
			volumeGiB:    100,
			wantSwap:     32,
		},
		{
			name:         "auto: unknown instance type falls back to 4 GiB → 8 GiB swap",
			flagValue:    0,
			instanceType: "x99.mega",
			volumeGiB:    100,
			wantSwap:     8,
		},
		{
			name:         "explicit size",
			flagValue:    4,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     4,
		},
		{
			name:         "swap + OS reservation exactly fits volume",
			flagValue:    80,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     80,
		},
		{
			name:         "swap + OS reservation exceeds volume",
			flagValue:    81,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantErr:      true,
			errContains:  "exceeds root volume size",
		},
		{
			name:         "auto swap too large for small volume",
			flagValue:    0,
			instanceType: "r5.2xlarge", // 64 GiB RAM → 128 GiB swap
			volumeGiB:    100,
			wantErr:      true,
			errContains:  "exceeds root volume size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSwapSize(tt.flagValue, tt.instanceType, tt.volumeGiB)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (swap=%d)", got)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantSwap {
				t.Errorf("resolveSwapSize(%d, %q, %d) = %d, want %d",
					tt.flagValue, tt.instanceType, tt.volumeGiB, got, tt.wantSwap)
			}
		})
	}
}
