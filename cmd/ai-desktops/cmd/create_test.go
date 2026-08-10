package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/config"
)

func TestCreateCmd_stepCAProvisionerDefault(t *testing.T) {
	flag := createCmd.Flags().Lookup("step-ca-provisioner")
	if flag == nil {
		t.Fatal("step-ca-provisioner flag not registered")
	}
	if flag.DefValue != "admin" {
		t.Fatalf("step-ca-provisioner default = %q, want admin", flag.DefValue)
	}
}

func TestCreateCmd_spotFlagsRegistered(t *testing.T) {
	if flag := createCmd.Flags().Lookup("spot"); flag == nil {
		t.Fatal("spot flag not registered")
	}
	if flag := createCmd.Flags().Lookup("spot-max-price"); flag == nil {
		t.Fatal("spot-max-price flag not registered")
	}
}

func TestResolveCreateIntegrations(t *testing.T) {
	tests := []struct {
		name          string
		in            resolveCreateIntegrationsInput
		wantTailnet   string
		wantTailscale bool
		wantStepCA    string
		wantStepCAOn  bool
		wantProv      string
		wantFP        string
		wantErr       string
	}{
		{
			name: "config alone does not enable tailscale or step-ca",
			in: resolveCreateIntegrationsInput{
				configTailscaleNetwork:  "config-tailnet",
				configStepCA:            "ca.config.ts.net",
				configStepCAProvisioner: "admin",
				configStepCAFingerprint: "config-fp",
			},
		},
		{
			name: "tailscale flag enables tailscale and configured step-ca",
			in: resolveCreateIntegrationsInput{
				tailscale:               true,
				configTailscaleNetwork:  "config-tailnet",
				configStepCA:            "ca.config.ts.net",
				configStepCAProvisioner: "ai-desktops",
				configStepCAFingerprint: "config-fp",
			},
			wantTailnet:   "config-tailnet",
			wantTailscale: true,
			wantStepCA:    "ca.config.ts.net",
			wantStepCAOn:  true,
			wantProv:      "ai-desktops",
			wantFP:        "config-fp",
		},
		{
			name: "tailscale flag requires tailnet",
			in: resolveCreateIntegrationsInput{
				tailscale: true,
			},
			wantErr: "Tailscale requires --tailscale-network",
		},
		{
			name: "tailscale network flag enables tailscale and overrides config",
			in: resolveCreateIntegrationsInput{
				tailscaleNetwork:       "flag-tailnet",
				tailscaleNetworkSet:    true,
				configTailscaleNetwork: "config-tailnet",
				configStepCA:           "ca.config.ts.net",
			},
			wantTailnet:   "flag-tailnet",
			wantTailscale: true,
		},
		{
			name: "step-ca requires fingerprint",
			in: resolveCreateIntegrationsInput{
				stepCA:                  "ca.flag.ts.net",
				stepCASet:               true,
				configStepCAProvisioner: "admin",
			},
			wantErr: "step-ca fingerprint must be set",
		},
		{
			name: "step-ca flag enables step-ca without tailscale",
			in: resolveCreateIntegrationsInput{
				stepCA:                 "ca.flag.ts.net",
				stepCASet:              true,
				stepCAProvisioner:      "ops",
				stepCAProvisionerSet:   true,
				stepCAFingerprint:      "flag-fp",
				stepCAFingerprintSet:   true,
				configTailscaleNetwork: "config-tailnet",
			},
			wantStepCA:   "ca.flag.ts.net",
			wantStepCAOn: true,
			wantProv:     "ops",
			wantFP:       "flag-fp",
		},
		{
			name: "step-ca fingerprint falls back to environment",
			in: resolveCreateIntegrationsInput{
				stepCA:                  "ca.flag.ts.net",
				stepCASet:               true,
				configStepCAProvisioner: "admin",
				envStepCAFingerprint:    "env-fp",
			},
			wantStepCA:   "ca.flag.ts.net",
			wantStepCAOn: true,
			wantProv:     "admin",
			wantFP:       "env-fp",
		},
		{
			name: "empty step-ca provisioner fails when step-ca enabled",
			in: resolveCreateIntegrationsInput{
				stepCA:               "ca.flag.ts.net",
				stepCASet:            true,
				stepCAProvisioner:    "",
				stepCAProvisionerSet: true,
			},
			wantErr: "step-ca provisioner must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCreateIntegrations(tt.in)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.tailscaleEnabled != tt.wantTailscale {
				t.Errorf("tailscaleEnabled = %v, want %v", got.tailscaleEnabled, tt.wantTailscale)
			}
			if got.tailscaleNetwork != tt.wantTailnet {
				t.Errorf("tailscaleNetwork = %q, want %q", got.tailscaleNetwork, tt.wantTailnet)
			}
			if got.stepCAEnabled != tt.wantStepCAOn {
				t.Errorf("stepCAEnabled = %v, want %v", got.stepCAEnabled, tt.wantStepCAOn)
			}
			if got.stepCA != tt.wantStepCA {
				t.Errorf("stepCA = %q, want %q", got.stepCA, tt.wantStepCA)
			}
			if got.stepCAProvisioner != tt.wantProv {
				t.Errorf("stepCAProvisioner = %q, want %q", got.stepCAProvisioner, tt.wantProv)
			}
			if got.stepCAFingerprint != tt.wantFP {
				t.Errorf("stepCAFingerprint = %q, want %q", got.stepCAFingerprint, tt.wantFP)
			}
		})
	}
}

func TestResolveStepCAClients_ConfigAndFlags(t *testing.T) {
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "mark.pub")
	if err := os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA mark\n"), 0600); err != nil {
		t.Fatal(err)
	}

	clients, err := resolveStepCAClients(
		[]config.StepCAClientConfig{
			{Issuer: "config-client", PublicKey: "ssh-ed25519 BBBB config"},
		},
		[]string{"issuer=mark-macbook,public-key-path=" + pubPath + ",required=true"},
	)
	if err != nil {
		t.Fatalf("resolveStepCAClients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients = %d, want 2", len(clients))
	}
	if clients[0].Issuer != "config-client" || clients[0].PublicKey != "ssh-ed25519 BBBB config" {
		t.Fatalf("config client not preserved: %+v", clients[0])
	}
	if clients[1].Issuer != "mark-macbook" {
		t.Fatalf("flag client issuer = %q", clients[1].Issuer)
	}
	if clients[1].PublicKey != "ssh-ed25519 AAAA mark" {
		t.Fatalf("flag client public key = %q", clients[1].PublicKey)
	}
	if !clients[1].Required {
		t.Fatal("flag client required should be true")
	}
}

func TestResolveStepCAClients_InvalidIssuer(t *testing.T) {
	_, err := resolveStepCAClients([]config.StepCAClientConfig{
		{Issuer: "../bad", PublicKey: "ssh-ed25519 AAAA bad"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error = %v, want issuer validation error", err)
	}
}

func TestPrepareIntegrationSecrets_previewDoesNotRequireSecretEnv(t *testing.T) {
	t.Setenv("TAILSCALE_AUTHKEY", "")
	t.Setenv("STEP_CA_PROVISIONER_PASSWORD", "")

	tailscalePath, stepCAPath, err := prepareIntegrationSecrets(
		context.Background(),
		true,
		"acme",
		"dev",
		"invalid-region-for-preview-test",
		"invalid-profile-for-preview-test",
		"/ai-desktops/acme",
		"acme-tailnet",
		"ca.acme-tailnet.ts.net",
	)
	if err != nil {
		t.Fatalf("prepareIntegrationSecrets preview returned error: %v", err)
	}
	if tailscalePath != "/ai-desktops/acme/tailscale/acme-tailnet" {
		t.Fatalf("tailscale path = %q", tailscalePath)
	}
	if stepCAPath != "/ai-desktops/acme/step-ca/ca.acme-tailnet.ts.net" {
		t.Fatalf("step-ca path = %q", stepCAPath)
	}
}

func TestIntegrationSecretHasKey(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		want        bool
		wantErr     bool
		errContains string
	}{
		{
			name:   "secret contains key",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"TS_AUTHKEY\":\"tskey-auth-test\"}"}`,
			want:   true,
		},
		{
			name:   "secret missing key",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"OTHER\":\"value\"}"}`,
		},
		{
			name:   "secret key blank",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"TS_AUTHKEY\":\"  \"}"}`,
		},
		{
			name:   "secret not found",
			status: http.StatusBadRequest,
			body:   `{"__type":"ResourceNotFoundException","Message":"not found"}`,
		},
		{
			name:        "invalid json secret",
			status:      http.StatusOK,
			body:        `{"SecretString":"not-json"}`,
			wantErr:     true,
			errContains: "parse integration secret",
		},
		{
			name:        "permission error",
			status:      http.StatusBadRequest,
			body:        `{"__type":"AccessDeniedException","Message":"denied"}`,
			wantErr:     true,
			errContains: "read integration secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target := r.Header.Get("X-Amz-Target"); !strings.HasSuffix(target, "GetSecretValue") {
					t.Fatalf("unexpected AWS target %q", target)
				}
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got, err := integrationSecretHasKey(context.Background(), makeSecretsManagerConfig(srv.URL), "/ai-desktops/acme/tailscale/acme", "TS_AUTHKEY")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGzipBase64UserData(t *testing.T) {
	const userData = "#cloud-config\nruncmd:\n  - echo hello\n"

	encoded, err := gzipBase64UserData(userData)
	if err != nil {
		t.Fatalf("gzipBase64UserData returned error: %v", err)
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(decoded) != userData {
		t.Fatalf("decoded user-data = %q, want %q", string(decoded), userData)
	}
}

func TestSecretPathSlugDisallowsSlash(t *testing.T) {
	got := secretPathSlug("/team/tailnet/name/")
	if got != "team-tailnet-name" {
		t.Fatalf("secretPathSlug with slashes = %q, want team-tailnet-name", got)
	}
}

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
		{
			name:        "name with shell metacharacter rejected",
			specs:       []string{"flutter$(evil):system-images;android-35;google_apis;x86_64"},
			wantErr:     true,
			errContains: "invalid --avd name",
		},
		{
			name:        "image with shell metacharacter rejected",
			specs:       []string{"flutter_dev:system-images;android-35;google_apis;x86_64$(evil)"},
			wantErr:     true,
			errContains: "invalid --avd image",
		},
		{
			name:        "device with shell metacharacter rejected",
			specs:       []string{"flutter_dev:system-images;android-35;google_apis;x86_64:pixel$(evil)"},
			wantErr:     true,
			errContains: "invalid --avd device",
		},
		{
			name:    "name with hyphens allowed",
			specs:   []string{"my-avd:system-images;android-35;google_apis;x86_64"},
			wantLen: 1,
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
