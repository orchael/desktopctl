package provision

import (
	"strings"
	"testing"
)

func TestRenderCloudInit(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:     "d-001",
		GitHubOwner:   "acme",
		Repos:         []string{"github.com/acme/app-one", "github.com/acme/app-two"},
		WorkspacePath: "/workspace",
		BridgePort:    9445,
		PATSecretPath: "/ai-desktops/github/pat",
		AWSRegion:     "us-east-1",
		Environment:   "dev",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"#cloud-config",
		"d-001",
		"acme",
		"app-one",
		"app-two",
		"/workspace",
		"9445",
		"/ai-desktops/github/pat",
		"us-east-1",
		"novnc-desktop",
		"ai-agent-bridge",
		"docker",
		"tmux",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

func TestRenderCloudInit_defaults(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-002",
		GitHubOwner: "acme",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	if !strings.Contains(out, "/workspace") {
		t.Error("expected default workspace path /workspace")
	}
	if !strings.Contains(out, "9445") {
		t.Error("expected default bridge port 9445")
	}
}

func TestRenderCloudInit_noSecretInOutput(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:     "d-003",
		GitHubOwner:   "acme",
		PATSecretPath: "/ai-desktops/github/pat",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	// The rendered output should reference the SSM path, not a literal secret value.
	// In this template the PAT is retrieved at runtime via AWS CLI, so no literal
	// token should appear.
	if strings.Contains(out, "ghp_") {
		t.Error("rendered cloud-init must not contain a literal GitHub PAT")
	}
}
