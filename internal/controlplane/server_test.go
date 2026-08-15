package controlplane

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	appconfig "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/store"
)

func TestListDesktopsFiltersTerminated(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-1", State: store.StateReady, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(t.Context(), &store.Desktop{DesktopID: "d-2", State: store.StateTerminated, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	service := NewService(testConfig(), s, aws.Config{}, false, false, time.Second)
	server := NewServer(service, "", slog.Default()).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops", nil)
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
	service := NewService(testConfig(), store.NewInMemoryStore(), aws.Config{}, false, false, time.Second)
	server := NewServer(service, "", slog.Default()).Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/desktops/missing", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
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

func testConfig() *appconfig.Config {
	cfg := &appconfig.Config{}
	cfg.Defaults()
	cfg.Pulumi.BackendBucket = "bucket"
	cfg.Desktop.OperatorCIDR = "127.0.0.1/32"
	return cfg
}
