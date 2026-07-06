package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
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
		w.Header().Set("X-OAuth-Scopes", "repo, workflow, security_events, admin:public_key, read:packages")
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

// makeSecretsManagerConfig returns an aws.Config wired to a local httptest server URL.
func makeSecretsManagerConfig(serverURL string) aws.Config {
	return aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		EndpointResolverWithOptions: aws.EndpointResolverWithOptionsFunc(
			func(service, region string, _ ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: serverURL, HostnameImmutable: true}, nil
			},
		),
	}
}

func TestStoreSecret_Create(t *testing.T) {
	var describeCalled, createCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		switch {
		case strings.HasSuffix(target, "DescribeSecret"):
			describeCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException","Message":"secret not found"}`))
		case strings.HasSuffix(target, "CreateSecret"):
			createCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123456789012:secret:test","Name":"test"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"InvalidRequestException","Message":"unexpected target"}`))
		}
	}))
	defer srv.Close()

	cfg := makeSecretsManagerConfig(srv.URL)
	err := storeSecret(context.Background(), cfg, "/ai-desktops/testowner/github", `{"token":"abc"}`, "testowner", "dev")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !describeCalled {
		t.Error("expected DescribeSecret to be called")
	}
	if !createCalled {
		t.Error("expected CreateSecret to be called")
	}
}

func TestStoreSecret_Update(t *testing.T) {
	var describeCalled, putCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		switch {
		case strings.HasSuffix(target, "DescribeSecret"):
			describeCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123456789012:secret:test","Name":"test"}`))
		case strings.HasSuffix(target, "PutSecretValue"):
			putCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123456789012:secret:test","Name":"test"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"InvalidRequestException","Message":"unexpected target"}`))
		}
	}))
	defer srv.Close()

	cfg := makeSecretsManagerConfig(srv.URL)
	err := storeSecret(context.Background(), cfg, "/ai-desktops/testowner/github", `{"token":"abc"}`, "testowner", "dev")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !describeCalled {
		t.Error("expected DescribeSecret to be called")
	}
	if !putCalled {
		t.Error("expected PutSecretValue to be called")
	}
}

func TestStoreSecret_DescribeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"__type":"AccessDeniedException","Message":"access denied"}`))
	}))
	defer srv.Close()

	cfg := makeSecretsManagerConfig(srv.URL)
	err := storeSecret(context.Background(), cfg, "/ai-desktops/testowner/github", `{"token":"abc"}`, "testowner", "dev")
	if err == nil {
		t.Fatal("expected error for non-NotFound DescribeSecret failure")
	}
	if !strings.Contains(err.Error(), "describe secret") {
		t.Errorf("error should mention 'describe secret', got: %v", err)
	}
}

func TestBuildAgentSecretJSON(t *testing.T) {
	tests := []struct {
		name         string
		anthropicKey string
		openaiKey    string
		geminiKey    string
		wantKeys     []string
		wantMissing  []string
	}{
		{
			name:         "all keys",
			anthropicKey: "sk-ant-123",
			openaiKey:    "sk-openai-456",
			geminiKey:    "AIza-789",
			wantKeys:     []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY"},
		},
		{
			name:         "only anthropic",
			anthropicKey: "sk-ant-123",
			wantKeys:     []string{"CLAUDE_CODE_OAUTH_TOKEN"},
			wantMissing:  []string{"OPENAI_API_KEY", "GEMINI_API_KEY"},
		},
		{
			name:        "no keys",
			wantMissing: []string{"CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "GEMINI_API_KEY"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := buildAgentSecretJSON(tc.anthropicKey, tc.openaiKey, tc.geminiKey)
			if err != nil {
				t.Fatalf("buildAgentSecretJSON: %v", err)
			}
			var m map[string]string
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}
			for _, k := range tc.wantKeys {
				if _, ok := m[k]; !ok {
					t.Errorf("expected key %q to be present", k)
				}
			}
			for _, k := range tc.wantMissing {
				if _, ok := m[k]; ok {
					t.Errorf("expected key %q to be absent", k)
				}
			}
		})
	}
}

func TestMergeAgentKeys(t *testing.T) {
	tests := []struct {
		name         string
		existing     map[string]string
		anthropicKey string
		openaiKey    string
		geminiKey    string
		want         map[string]string
	}{
		{
			name:         "new key added, existing preserved",
			existing:     map[string]string{"OPENAI_API_KEY": "sk-old", "GEMINI_API_KEY": "gem-old"},
			anthropicKey: "claude-new",
			want: map[string]string{
				"CLAUDE_CODE_OAUTH_TOKEN": "claude-new",
				"OPENAI_API_KEY":          "sk-old",
				"GEMINI_API_KEY":          "gem-old",
			},
		},
		{
			name:      "existing key updated",
			existing:  map[string]string{"OPENAI_API_KEY": "sk-old"},
			openaiKey: "sk-new",
			want:      map[string]string{"OPENAI_API_KEY": "sk-new"},
		},
		{
			name:     "empty input preserves all existing",
			existing: map[string]string{"OPENAI_API_KEY": "sk-old", "GEMINI_API_KEY": "gem-old"},
			want:     map[string]string{"OPENAI_API_KEY": "sk-old", "GEMINI_API_KEY": "gem-old"},
		},
		{
			name:         "no existing, all new",
			existing:     map[string]string{},
			anthropicKey: "claude-new",
			openaiKey:    "sk-new",
			geminiKey:    "gem-new",
			want: map[string]string{
				"CLAUDE_CODE_OAUTH_TOKEN": "claude-new",
				"OPENAI_API_KEY":          "sk-new",
				"GEMINI_API_KEY":          "gem-new",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeAgentKeys(tc.existing, tc.anthropicKey, tc.openaiKey, tc.geminiKey)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d keys, want %d: %v", len(got), len(tc.want), got)
			}
			for k, wantV := range tc.want {
				if got[k] != wantV {
					t.Errorf("key %q: got %q, want %q", k, got[k], wantV)
				}
			}
		})
	}
}

func TestStoreAgentSecret_Create(t *testing.T) {
	var createCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		switch {
		case strings.HasSuffix(target, "DescribeSecret"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException","Message":"not found"}`))
		case strings.HasSuffix(target, "CreateSecret"):
			createCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123:secret:test","Name":"test"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"InvalidRequestException","Message":"unexpected"}`))
		}
	}))
	defer srv.Close()

	cfg := makeSecretsManagerConfig(srv.URL)
	err := storeAgentSecret(context.Background(), cfg, "/ai-desktops/testowner/agents", `{"CLAUDE_CODE_OAUTH_TOKEN":"sk"}`, "testowner", "dev")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !createCalled {
		t.Error("expected CreateSecret to be called")
	}
}

func TestStoreAgentSecret_Update(t *testing.T) {
	var putCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		switch {
		case strings.HasSuffix(target, "DescribeSecret"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123:secret:test","Name":"test"}`))
		case strings.HasSuffix(target, "PutSecretValue"):
			putCalled = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ARN":"arn:aws:secretsmanager:us-east-1:123:secret:test","Name":"test"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"__type":"InvalidRequestException","Message":"unexpected"}`))
		}
	}))
	defer srv.Close()

	cfg := makeSecretsManagerConfig(srv.URL)
	err := storeAgentSecret(context.Background(), cfg, "/ai-desktops/testowner/agents", `{"CLAUDE_CODE_OAUTH_TOKEN":"sk"}`, "testowner", "dev")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !putCalled {
		t.Error("expected PutSecretValue to be called")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// makeEC2Config returns an aws.Config wired to a local httptest server URL.
func makeEC2Config(serverURL string) aws.Config {
	return aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
		EndpointResolverWithOptions: aws.EndpointResolverWithOptionsFunc(
			func(service, region string, _ ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: serverURL, HostnameImmutable: true}, nil
			},
		),
	}
}

func TestImportEC2KeyPair_Success(t *testing.T) {
	var importCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("Action") == "ImportKeyPair" {
			importCalled = true
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ImportKeyPairResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>test-id</requestId>
  <keyName>ai-desktops-test</keyName>
  <keyFingerprint>ab:cd:ef</keyFingerprint>
</ImportKeyPairResponse>`))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	cfg := makeEC2Config(srv.URL)
	err := importEC2KeyPair(context.Background(), cfg, "ai-desktops-test", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !importCalled {
		t.Error("expected ImportKeyPair to be called")
	}
}

func TestImportEC2KeyPair_Duplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("Action") == "ImportKeyPair" {
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Errors>
    <Error>
      <Code>InvalidKeyPair.Duplicate</Code>
      <Message>The key pair &apos;ai-desktops-test&apos; already exists.</Message>
    </Error>
  </Errors>
  <RequestID>test-id</RequestID>
</Response>`))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	cfg := makeEC2Config(srv.URL)
	// Duplicate key pair should be treated as success (no error).
	err := importEC2KeyPair(context.Background(), cfg, "ai-desktops-test", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatalf("expected duplicate key pair to be treated as success, got: %v", err)
	}
}

func TestImportEC2KeyPair_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.FormValue("Action") == "ImportKeyPair" {
			w.Header().Set("Content-Type", "text/xml")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Errors>
    <Error>
      <Code>AuthFailure</Code>
      <Message>AWS was not able to validate the provided access credentials</Message>
    </Error>
  </Errors>
  <RequestID>test-id</RequestID>
</Response>`))
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	cfg := makeEC2Config(srv.URL)
	err := importEC2KeyPair(context.Background(), cfg, "ai-desktops-test", "ssh-ed25519 AAAA test")
	if err == nil {
		t.Fatal("expected an error for auth failure, got nil")
	}
}
