package cmd

import (
	"strings"
	"testing"
)

func TestBuildSecretsReloadScript_ContainsRegion(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/myapp/secrets"}, "us-west-2")
	if !strings.Contains(script, `"us-west-2"`) {
		t.Errorf("script should contain the region, got:\n%s", script)
	}
}

func TestBuildSecretsReloadScript_ContainsSecretPaths(t *testing.T) {
	paths := []string{"/myapp/db", "/myapp/api-key"}
	script := buildSecretsReloadScript(paths, "us-east-1")
	for _, p := range paths {
		if !strings.Contains(script, p) {
			t.Errorf("script should contain secret path %q, got:\n%s", p, script)
		}
	}
}

func TestBuildSecretsReloadScript_WritesEnvFiles(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/s"}, "us-east-1")
	checks := []string{
		"~/.config/environment.d/desktop-secrets.conf",
		"~/.desktop-secrets",
		"systemctl --user daemon-reload",
		"export %s",
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("script should contain %q, got:\n%s", want, script)
		}
	}
}

func TestBuildSecretsReloadScript_RestartsServices(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/s"}, "us-east-1")
	if !strings.Contains(script, "bridgectl") {
		t.Errorf("script should reference bridgectl service, got:\n%s", script)
	}
}

func TestBuildSecretsReloadScript_MultipleSecrets(t *testing.T) {
	paths := []string{"/a/b", "/c/d", "/e/f"}
	script := buildSecretsReloadScript(paths, "eu-west-1")
	// Each path should appear multiple times (in the get-secret-value call and in the warnings).
	for _, p := range paths {
		count := strings.Count(script, p)
		if count < 2 {
			t.Errorf("secret path %q should appear at least twice in script, got %d occurrence(s)", p, count)
		}
	}
}

func TestBuildSecretsReloadScript_HandlesEmptyResult(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/secret"}, "us-east-1")
	if !strings.Contains(script, "no secret values retrieved") {
		t.Errorf("script should handle empty result gracefully, got:\n%s", script)
	}
}

func TestBuildSecretsReloadScript_SafeShell(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/s"}, "us-east-1")
	if !strings.HasPrefix(script, "set -euo pipefail") {
		t.Errorf("script should start with 'set -euo pipefail', got: %q", script[:min(30, len(script))])
	}
}

func TestBuildSecretsReloadScript_TempFileCleanup(t *testing.T) {
	script := buildSecretsReloadScript([]string{"/s"}, "us-east-1")
	if !strings.Contains(script, "trap") || !strings.Contains(script, "DESKTOP_ENV_TMP") {
		t.Errorf("script should set up trap for temp file cleanup, got:\n%s", script)
	}
}
