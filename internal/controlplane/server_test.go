package controlplane

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/store"
)

func TestStandaloneDesktopAccessRedirect(t *testing.T) {
	t.Parallel()
	fleet := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-123", State: store.StateReady, Hostname: "d-123.example.com", SSHTarget: "ubuntu@d-123.example.com"}
	if err := fleet.Create(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	server := NewServer(newTestService(testConfig(), fleet).WithStandalone(), "", slog.Default(), "secret-token")
	server.accessURL = func(_ context.Context, got *store.Desktop, _ string) (string, error) {
		if got.DesktopID != d.DesktopID {
			t.Fatalf("desktop = %q", got.DesktopID)
		}
		return "https://d-123.example.com:8443/access?token=one-session", nil
	}
	request := httptest.NewRequest(http.MethodGet, "/api/desktops/d-123/access", nil)
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("without token = %d", unauthorized.Code)
	}
	request.Header.Set("Authorization", "Bearer secret-token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "https://d-123.example.com:8443/access?token=one-session" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("access redirect = %d, headers = %v", response.Code, response.Header())
	}
}

const testOrganizationID = "00000000-0000-4000-8000-000000000001"

func TestStandaloneListsCLIRecordsWithoutOrganization(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	for _, d := range []store.Desktop{
		{DesktopID: "cli", State: store.StateReady},
		{DesktopID: "hosted", OrganizationID: testOrganizationID, State: store.StateReady},
	} {
		if err := s.Create(t.Context(), &d); err != nil {
			t.Fatal(err)
		}
	}
	service := newTestService(testConfig(), s).WithStandalone()
	server := NewServer(service, "", slog.Default(), "secret-token").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without token: status = %d", rec.Code)
	}

	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with token: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got []store.Desktop
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d desktops, want 2", len(got))
	}
}

func TestStandaloneRequiresLifecycleRunner(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore()).WithStandalone()
	server := NewServer(service, "", slog.Default(), "secret-token").Handler()
	for _, tc := range []struct{ path, body string }{
		{"/api/desktops", `{"owner":"orchael"}`},
		{"/api/desktops/d-test001/terminate", `{"confirm":"d-test001"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer secret-token")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d", tc.path, rec.Code)
		}
	}
}

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

func TestCreateDesktopWithoutPulumiRunnerReturnsError(t *testing.T) {
	t.Parallel()
	// newTestService passes nil runner; CreateDesktop must return an error.
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	body := strings.NewReader(`{"owner":"orchael"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/desktops", body)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
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

	body := strings.NewReader(`{"owner":"orchael"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/desktops", body)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Organization-ID", testOrganizationID)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	// No Pulumi runner configured → service returns an error → 500.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
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

func TestReadyTreatsMissingProbeRecordAsStorageReady(t *testing.T) {
	t.Parallel()
	service := NewService(testConfig(), store.NewInMemoryStore(), nil, false, true, time.Second, time.Minute)

	got := service.Ready(t.Context())

	if !got.OK {
		t.Fatalf("Ready().OK = false, error = %q", got.Error)
	}
	if got.Identity != "mock" {
		t.Fatalf("Ready().Identity = %q, want mock", got.Identity)
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
		OperatorExternalID:      "external-id",
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

func TestTerminateDesktopNoPulumiRunnerReturnsError(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-001/terminate", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestTerminateDesktopNotFound(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	service := NewService(testConfig(), s, &mockPulumiRunner{}, false, false, time.Second, time.Minute)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-missing/terminate", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTerminateDesktopSuccessViaServer(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Format(time.RFC3339)
	s := store.NewInMemoryStore()
	if err := s.Create(t.Context(), &store.Desktop{
		DesktopID:      "d-term",
		OrganizationID: testOrganizationID,
		State:          store.StateReady,
		InstanceID:     "i-term",
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(testConfig(), s, &mockPulumiRunner{}, false, false, time.Second, time.Minute)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-term/terminate", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
}

func TestListDesktopsIncludeTerminatedQueryParam(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Format(time.RFC3339)
	s := store.NewInMemoryStore()
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-a", OrganizationID: testOrganizationID, State: store.StateReady, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-b", OrganizationID: testOrganizationID, State: store.StateTerminated, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := newTestService(testConfig(), s)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops?all=true", nil)
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
	if len(got) != 2 {
		t.Errorf("expected 2 desktops (including terminated), got %d", len(got))
	}
}

func TestStartDesktopNotFound(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-missing/start", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStopDesktopNotFound(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-missing/stop", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRefreshDesktopNotFound(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-missing/refresh", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHealthReturnsOK(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyReturnsMockIdentity(t *testing.T) {
	t.Parallel()
	service := NewService(testConfig(), store.NewInMemoryStore(), nil, false, true, time.Second, time.Minute)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestReadyReturnsUnavailableWhenAWSNotReady(t *testing.T) {
	t.Parallel()
	service := NewService(testConfig(), store.NewInMemoryStore(), nil, false, false, time.Second, time.Minute)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestCreateDesktopBadJSONReturns400(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader("not-json"))
	req.Header.Set("X-Organization-ID", testOrganizationID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestListDesktopsMissingOrgIDReturns400(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestDesktopActionUnknownReturns404(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-001/explode", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDesktopActionMissingIDReturns404(t *testing.T) {
	t.Parallel()
	service := newTestService(testConfig(), store.NewInMemoryStore())
	server := NewServer(service, "", slog.Default(), "").Handler()

	// /api/desktops// with an empty id segment
	req := httptest.NewRequest(http.MethodGet, "/api/desktops/", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetDesktopWithMockAWS(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Format(time.RFC3339)
	s := store.NewInMemoryStore()
	if err := s.Create(t.Context(), &store.Desktop{
		DesktopID:      "d-mock",
		OrganizationID: testOrganizationID,
		State:          store.StateReady,
		InstanceID:     "i-mock",
		CreatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	// mockAWS=true so liveInstanceState returns "mock" without real EC2 call.
	service := NewService(testConfig(), s, nil, false, true, time.Second, time.Minute)
	server := NewServer(service, "", slog.Default(), "").Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops/d-mock", nil)
	req.Header.Set("X-Organization-ID", testOrganizationID)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func newTestService(cfg *appconfig.Config, s store.Store) *Service {
	return NewService(cfg, s, nil, false, false, time.Second, time.Minute)
}

func testConfig() *appconfig.Config {
	cfg := &appconfig.Config{}
	cfg.Defaults()
	cfg.Pulumi.BackendBucket = "bucket"
	cfg.Desktop.OperatorCIDR = "127.0.0.1/32"
	return cfg
}
