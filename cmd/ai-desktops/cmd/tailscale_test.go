package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTailscaleNameMatchesDesktop(t *testing.T) {
	tests := []struct {
		name      string
		desktopID string
		want      bool
	}{
		{name: "d-1234abcd", desktopID: "d-1234abcd", want: true},
		{name: "d-1234abcd.tailnet.ts.net", desktopID: "d-1234abcd", want: true},
		{name: "d-1234abcd.", desktopID: "d-1234abcd", want: true},
		{name: "d-1234abcde", desktopID: "d-1234abcd", want: false},
		{name: "other.tailnet.ts.net", desktopID: "d-1234abcd", want: false},
	}

	for _, tt := range tests {
		if got := tailscaleNameMatchesDesktop(tt.name, tt.desktopID); got != tt.want {
			t.Errorf("tailscaleNameMatchesDesktop(%q, %q) = %v, want %v", tt.name, tt.desktopID, got, tt.want)
		}
	}
}

func TestRemoveTailscaleDesktopDeviceDeletesMatchingDevice(t *testing.T) {
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "ts-api-key" || password != "" {
			t.Fatalf("unexpected auth: user=%q password=%q ok=%v", user, password, ok)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tailnet/acme-tailnet/devices":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"devices":[{"id":"node-1","name":"d-1234abcd.acme.ts.net"},{"id":"node-2","hostname":"unrelated"}]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/device/node-1":
			deleted = append(deleted, "node-1")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	t.Setenv("TAILSCALE_API_KEY", "ts-api-key")
	t.Setenv("TAILSCALE_API_BASE_URL", srv.URL)

	if err := removeTailscaleDesktopDevice(context.Background(), "acme-tailnet", "d-1234abcd"); err != nil {
		t.Fatalf("removeTailscaleDesktopDevice: %v", err)
	}
	if strings.Join(deleted, ",") != "node-1" {
		t.Fatalf("deleted = %v, want [node-1]", deleted)
	}
}

func TestRemoveTailscaleDesktopDeviceNoMatchIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":[{"id":"node-1","name":"other.acme.ts.net"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TAILSCALE_API_KEY", "ts-api-key")
	t.Setenv("TAILSCALE_API_BASE_URL", srv.URL)

	if err := removeTailscaleDesktopDevice(context.Background(), "acme-tailnet", "d-1234abcd"); err != nil {
		t.Fatalf("removeTailscaleDesktopDevice: %v", err)
	}
}

func TestRemoveTailscaleDesktopDeviceRequiresAPIKey(t *testing.T) {
	t.Setenv("TAILSCALE_API_KEY", "")
	t.Setenv("TAILSCALE_API_BASE_URL", "http://127.0.0.1")

	err := removeTailscaleDesktopDevice(context.Background(), "acme-tailnet", "d-1234abcd")
	if err != errTailscaleAPIKeyMissing {
		t.Fatalf("error = %v, want %v", err, errTailscaleAPIKeyMissing)
	}
}
