package provision

import (
	"strings"
	"testing"
)

func TestRenderCloudInit(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:     "d-001",
		Hostname:      "d-001.desktops.orchael.dev",
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
		NovncDesktopVersion,
		"ai-agent-bridge",
		AIAgentBridgeVersion,
		"docker",
		"tmux",
		"certbot",
		"dns-route53",
		"d-001.desktops.orchael.dev",
		"8443",
		"8080",
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
		Hostname:    "d-002.desktops.orchael.dev",
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
	if !strings.Contains(out, "8443") {
		t.Error("expected default noVNC HTTPS port 8443")
	}
	if !strings.Contains(out, "8080") {
		t.Error("expected default noVNC HTTP port 8080")
	}
}

func TestRenderCloudInit_noSecretInOutput(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:     "d-003",
		Hostname:      "d-003.desktops.orchael.dev",
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

func TestRenderCloudInit_versionPins(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-004",
		Hostname:    "d-004.desktops.orchael.dev",
		GitHubOwner: "acme",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	// Verify install scripts reference pinned versions, not "main".
	if strings.Contains(out, "/main/install.sh") {
		t.Error("install scripts must not reference the 'main' branch; pin to a release tag")
	}
	if !strings.Contains(out, NovncDesktopVersion) {
		t.Errorf("novnc-desktop install must reference version %s", NovncDesktopVersion)
	}
	if !strings.Contains(out, AIAgentBridgeVersion) {
		t.Errorf("ai-agent-bridge install must reference version %s", AIAgentBridgeVersion)
	}
}
