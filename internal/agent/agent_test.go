package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
}

func TestClient_Providers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/providers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ProviderInfo{
			{Name: "codex", Available: true},
			{Name: "claude", Available: false},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	providers, err := c.Providers(context.Background())
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(providers) != 2 {
		t.Errorf("got %d providers, want 2", len(providers))
	}
	if providers[0].Name != "codex" {
		t.Errorf("provider[0].Name: got %q", providers[0].Name)
	}
}

func TestClient_StartSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sessions" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Session{ID: "sess-001", Provider: "codex", Repo: "app", Status: "running"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	sess, err := c.StartSession(context.Background(), "codex", "app")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sess.ID != "sess-001" {
		t.Errorf("session ID: got %q", sess.ID)
	}
}

func TestClient_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	_, err := c.Status(context.Background())
	if err == nil {
		t.Error("expected error for 500 response")
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("http://127.0.0.1:9445")
	if c.baseURL != "http://127.0.0.1:9445" {
		t.Errorf("baseURL: got %q", c.baseURL)
	}
}
