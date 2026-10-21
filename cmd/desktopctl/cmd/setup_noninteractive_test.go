package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/config"
	"github.com/spf13/cobra"
)

func setupInputCommand() *cobra.Command {
	c := &cobra.Command{}
	for _, name := range append([]string{"region", "profile"}, setupValueFlags...) {
		c.Flags().String(name, "", "")
	}
	return c
}

func TestNonInteractiveAnswersPrecedenceAndIdentity(t *testing.T) {
	c := setupInputCommand()
	current := &config.Config{
		AWS:     config.AWSConfig{Region: "ca-central-1", Profile: "marka"},
		Fleet:   config.FleetConfig{Environment: "prod"},
		Pulumi:  config.PulumiConfig{BackendBucket: "old-desktopctl-state"},
		GitHub:  config.GitHubConfig{Owner: "orchael"},
		Desktop: config.DesktopConfig{OperatorCIDR: "157.230.223.137/32", SSHKeyPath: "/tmp/ssh-key", SSHKeyName: "desktopctl-prod-ca-central-1-orchael"},
	}
	t.Setenv("DESKTOPCTL_BACKEND_BUCKET", "env-desktopctl-state")
	t.Setenv("DESKTOPCTL_GITHUB_TOKEN", "env-token")
	if err := c.Flags().Set("backend-bucket", "flag-desktopctl-state"); err != nil {
		t.Fatal(err)
	}
	a, err := nonInteractiveAnswers(c, current)
	if err != nil {
		t.Fatal(err)
	}
	if a.BackendBucket != "flag-desktopctl-state" || a.GitHubToken != "env-token" || a.AWSRegion != "ca-central-1" || a.SSHKeyName != "desktopctl-prod-ca-central-1-orchael" {
		t.Fatalf("unexpected resolved answers: bucket=%q token_present=%t region=%q key=%q", a.BackendBucket, a.GitHubToken != "", a.AWSRegion, a.SSHKeyName)
	}
	if err := c.Flags().Set("github-owner", "another"); err != nil {
		t.Fatal(err)
	}
	a, err = nonInteractiveAnswers(c, current)
	if err != nil {
		t.Fatal(err)
	}
	if a.SSHKeyName != "desktopctl-prod-ca-central-1-another" {
		t.Fatalf("wrong key name for changed owner: %q", a.SSHKeyName)
	}
}

func TestSetupSavedConfigIgnoresRootRegionOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := &config.Config{AWS: config.AWSConfig{Region: "us-east-1"}, Fleet: config.FleetConfig{Environment: "prod"}, GitHub: config.GitHubConfig{Owner: "orchael"}}
	if err := original.Save(path); err != nil {
		t.Fatal(err)
	}
	oldConfig, oldOutput := cfgFile, setupConfigOut
	cfgFile, setupConfigOut = path, ""
	t.Cleanup(func() { cfgFile, setupConfigOut = oldConfig, oldOutput })
	loaded := *original
	loaded.SetRegion("ca-central-1") // root --region mutates the in-memory config
	saved, err := setupSavedConfig()
	if err != nil {
		t.Fatal(err)
	}
	if setupSameIdentity(saved, loaded.AWS.Region, "prod", "orchael") {
		t.Fatal("root region override must not authorize reuse of another region's resources")
	}
}

func TestNonInteractiveAnswersRejectsUnsafeOrMissingValues(t *testing.T) {
	base := &config.Config{AWS: config.AWSConfig{Region: "ca-central-1", Profile: "marka"}, Pulumi: config.PulumiConfig{BackendBucket: "desktopctl-state"}, Fleet: config.FleetConfig{Environment: "prod"}, GitHub: config.GitHubConfig{Owner: "orchael"}, Desktop: config.DesktopConfig{OperatorCIDR: "157.230.223.137/32", SSHKeyPath: "/tmp/key"}}
	for _, tc := range []struct{ name, flag, value string }{
		{"unsafe CIDR", "operator-cidr", "0.0.0.0/0"},
		{"missing key path", "ssh-key-path", ""},
		{"invalid environment", "environment", "staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := setupInputCommand()
			if err := cmd.Flags().Set(tc.flag, tc.value); err != nil {
				t.Fatal(err)
			}
			if _, err := nonInteractiveAnswers(cmd, base); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestRestoreSetupSecretIfPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			restored := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.Header.Get("X-Amz-Target"), "DescribeSecret"):
					if pending {
						_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github","DeletedDate":1792534549}`))
					} else {
						_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github"}`))
					}
				case strings.HasSuffix(r.Header.Get("X-Amz-Target"), "RestoreSecret"):
					restored++
					_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github"}`))
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer srv.Close()
			if err := restoreSetupSecretIfPending(context.Background(), makeSecretsManagerConfig(srv.URL), "/desktopctl/prod/ca-central-1/orchael/github"); err != nil {
				t.Fatal(err)
			}
			want := 0
			if pending {
				want = 1
			}
			if restored != want {
				t.Fatalf("RestoreSecret calls=%d, want %d", restored, want)
			}
		})
	}
}

func TestStoreSecretRestoresThenUpdates(t *testing.T) {
	pending := true
	put := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.Header.Get("X-Amz-Target"), "DescribeSecret"):
			if pending {
				_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github","DeletedDate":1792534549}`))
			} else {
				_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github"}`))
			}
		case strings.HasSuffix(r.Header.Get("X-Amz-Target"), "RestoreSecret"):
			pending = false
			_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github"}`))
		case strings.HasSuffix(r.Header.Get("X-Amz-Target"), "PutSecretValue"):
			var body struct{ SecretString string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if body.SecretString != `{"github_token":"replacement"}` {
				t.Error("new secret value was not written")
			}
			put++
			_, _ = w.Write([]byte(`{"Name":"/desktopctl/prod/ca-central-1/orchael/github"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	if err := storeSecret(context.Background(), makeSecretsManagerConfig(srv.URL), "/desktopctl/prod/ca-central-1/orchael/github", `{"github_token":"replacement"}`, "orchael", "prod"); err != nil {
		t.Fatal(err)
	}
	if pending || put != 1 {
		t.Fatalf("pending=%t updates=%d", pending, put)
	}
}
