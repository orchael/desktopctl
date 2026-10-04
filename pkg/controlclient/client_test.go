package controlclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceContract(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer service-secret" || r.Header.Get("X-Organization-ID") != "org" {
			t.Error("missing scoped auth")
		}
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			var q AcquireRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil || q.RunID != "run" {
				t.Error("bad request")
			}
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(202)
			return
		}
		_ = json.NewEncoder(w).Encode(Lease{ID: "lease", State: "ACQUIRING"})
	}))
	defer server.Close()
	c, err := New(server.URL, "service-secret", "org", nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := c.Acquire(context.Background(), AcquireRequest{OrganizationID: "org", RunID: "run"})
	if err != nil || l.ID != "lease" {
		t.Fatal(l, err)
	}
	if _, err = c.Get(context.Background(), l.ID); err != nil {
		t.Fatal(err)
	}
	if err = c.Release(context.Background(), l.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST /api/workspaces/acquire,GET /api/workspaces/lease,DELETE /api/workspaces/lease" {
		t.Fatal(methods)
	}
	if _, err = c.Acquire(context.Background(), AcquireRequest{OrganizationID: "foreign"}); err == nil {
		t.Fatal("tenant mismatch accepted")
	}
}
func TestErrorsAndRedirectsNeverExposeCredentials(t *testing.T) {
	for _, code := range []int{302, 403, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://credential-leak.invalid")
				w.WriteHeader(code)
				_, _ = w.Write([]byte("service-secret provider-secret"))
			}))
			defer server.Close()
			c, _ := New(server.URL, "service-secret", "org", nil)
			_, err := c.Get(context.Background(), "lease")
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.Status != code || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
}
