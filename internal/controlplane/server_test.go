package controlplane

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	appconfig "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/store"
)

const testOrganizationID = "00000000-0000-4000-8000-000000000001"

func TestListDesktopsFiltersTerminated(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-1", OrganizationID: testOrganizationID, State: store.StateReady, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-2", OrganizationID: testOrganizationID, State: store.StateTerminated, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(testConfig(), s)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got []store.Desktop
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].DesktopID != "d-1" {
		t.Fatalf("unexpected desktops: %+v", got)
	}
}

func TestGetDesktopNotFound(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops/missing", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCreateDesktopDeferredReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestCreateDesktopRequiresBearerTokenWhenConfigured(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "secret-token").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/desktops", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestAPIPathWithoutTrailingSlashReturnsJSONNotStatic(t *testing.T) {
	t.Parallel()
	staticDir := t.TempDir()
	if err := os.WriteFile(staticDir+"/index.html", []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, staticDir, slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("content type = %q, want application/json", rec.Header().Get("Content-Type"))
	}
}

func TestRuntimeConfigRequiresRoleWhenNotMock(t *testing.T) {
	t.Parallel()
	cfg := RuntimeConfig{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing role error")
	}
	cfg.MockAWS = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("mock config should validate: %v", err)
	}
}

func TestRuntimeConfigRequiresAPITokenWhenNotMock(t *testing.T) {
	t.Parallel()
	cfg := RuntimeConfig{
		OperatorAccessKeyID:     "operator-access-key",
		OperatorSecretAccessKey: "operator-secret-key",
		OperatorRoleARN:         "arn:aws:iam::123456789012:role/control-plane",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing API token error")
	}
	cfg.APIToken = "secret-token"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config with API token should validate: %v", err)
	}
}

func TestRuntimeConfigParsesLifecycleTimeout(t *testing.T) {
	t.Setenv("CONTROL_PLANE_MOCK_AWS", "true")
	t.Setenv("CONTROL_PLANE_REFRESH_TIMEOUT", "3s")
	t.Setenv("CONTROL_PLANE_LIFECYCLE_TIMEOUT", "9m")

	cfg := LoadRuntimeConfig()
	if cfg.RefreshTimeout != 3*time.Second {
		t.Fatalf("RefreshTimeout = %s, want 3s", cfg.RefreshTimeout)
	}
	if cfg.LifecycleTimeout != 9*time.Minute {
		t.Fatalf("LifecycleTimeout = %s, want 9m", cfg.LifecycleTimeout)
	}
}

func newTestService(cfg *appconfig.Config, s store.Store) *Service {
	return NewService(cfg, s, aws.Config{}, false, false, time.Second, time.Minute)
}

func testConfig() *appconfig.Config {
	cfg := &appconfig.Config{}
	cfg.Defaults()
	cfg.Pulumi.BackendBucket = "bucket"
	cfg.Desktop.OperatorCIDR = "127.0.0.1/32"
	return cfg
}
