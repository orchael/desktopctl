package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateSSHKeyPair(t *testing.T) {
	pub, priv, err := generateSSHKeyPair("test-owner")
	if err != nil {
		t.Fatalf("generateSSHKeyPair: %v", err)
	}

	if !strings.HasPrefix(pub, "ssh-ed25519 ") {
		t.Errorf("public key should start with 'ssh-ed25519 ', got: %q", pub[:min(30, len(pub))])
	}
	if !strings.Contains(pub, "ai-desktops/test-owner") {
		t.Errorf("public key should contain owner comment, got: %q", pub)
	}
	if !strings.HasSuffix(pub, "\n") {
		t.Error("public key should end with newline")
	}

	if !strings.Contains(priv, "-----BEGIN OPENSSH PRIVATE KEY-----") {
		t.Errorf("private key should be PEM-encoded OPENSSH key, got prefix: %q", priv[:min(50, len(priv))])
	}
	if !strings.Contains(priv, "-----END OPENSSH PRIVATE KEY-----") {
		t.Error("private key PEM block should have END marker")
	}

	// Each call generates a unique key pair.
	pub2, priv2, err := generateSSHKeyPair("test-owner")
	if err != nil {
		t.Fatalf("second generateSSHKeyPair: %v", err)
	}
	if pub == pub2 {
		t.Error("two calls should produce different key pairs")
	}
	if priv == priv2 {
		t.Error("two calls should produce different private keys")
	}
}

func TestBuildSecretJSON(t *testing.T) {
	token := "ghp_testtoken"
	priv := "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n"
	pub := "ssh-ed25519 AAAA ai-desktops/owner"

	out, err := buildSecretJSON(token, priv, pub)
	if err != nil {
		t.Fatalf("buildSecretJSON: %v", err)
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if m["github_token"] != token {
		t.Errorf("github_token: got %q, want %q", m["github_token"], token)
	}
	if m["ssh_private_key"] != priv {
		t.Errorf("ssh_private_key mismatch")
	}
	if m["ssh_public_key"] != strings.TrimSpace(pub) {
		t.Errorf("ssh_public_key: got %q, want %q", m["ssh_public_key"], strings.TrimSpace(pub))
	}

	// Ensure all three fields are present.
	for _, key := range []string{"github_token", "ssh_private_key", "ssh_public_key"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing key %q in secret JSON", key)
		}
	}
}

func TestValidateGitHubToken_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, workflow, security_events, admin:public_key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"login":"testuser"}`))
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	if err := validateGitHubToken(context.Background(), "valid-token"); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateGitHubToken_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	err := validateGitHubToken(context.Background(), "bad-token")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention HTTP 401, got: %v", err)
	}
}

func TestValidateGitHubToken_MissingScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only grant repo — missing workflow, security_events, admin:public_key.
		w.Header().Set("X-OAuth-Scopes", "repo")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"login":"testuser"}`))
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	err := validateGitHubToken(context.Background(), "partial-token")
	if err == nil {
		t.Fatal("expected error for missing scopes")
	}
	if !strings.Contains(err.Error(), "missing required scopes") {
		t.Errorf("error should mention missing scopes, got: %v", err)
	}
}

func TestValidateGitHubToken_FineGrainedToken(t *testing.T) {
	// Fine-grained tokens don't return X-OAuth-Scopes; validate should succeed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"login":"testuser"}`))
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	if err := validateGitHubToken(context.Background(), "fine-grained-token"); err != nil {
		t.Errorf("expected no error for fine-grained token (no scope header), got: %v", err)
	}
}

func TestRegisterGitHubSSHKey_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !strings.Contains(r.URL.Path, "/user/keys") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body["title"] == "" || body["key"] == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1,"key":"ssh-ed25519 AAAA"}`))
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	err := registerGitHubSSHKey(context.Background(), "token", "ai-desktops/owner", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestRegisterGitHubSSHKey_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"key is already in use"}`))
	}))
	defer srv.Close()

	origURL := githubAPIBase
	githubAPIBase = srv.URL
	defer func() { githubAPIBase = origURL }()

	err := registerGitHubSSHKey(context.Background(), "token", "ai-desktops/owner", "ssh-ed25519 AAAA dup")
	if err == nil {
		t.Fatal("expected error for non-201 response")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should mention HTTP 422, got: %v", err)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
