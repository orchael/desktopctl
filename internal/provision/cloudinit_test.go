package provision

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderCloudInit(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:        "d-001",
		Hostname:         "d-001.desktops.orchael.dev",
		GitHubOwner:      "acme",
		Repos:            []string{"github.com/acme/app-one", "github.com/acme/app-two"},
		WorkspacePath:    "/workspace",
		BridgePort:       9445,
		GitHubSecretPath: "/ai-desktops/acme/github",
		AWSRegion:        "us-east-1",
		Environment:      "dev",
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
		"/ai-desktops/acme/github",
		"us-east-1",
		"ai-agent-bridge",
		AIAgentBridgeVersion,
		"docker",
		"tmux",
		"certbot",
		"dns-route53",
		"d-001.desktops.orchael.dev",
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
}

func TestRenderCloudInit_noSecretInOutput(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:        "d-003",
		Hostname:         "d-003.desktops.orchael.dev",
		GitHubOwner:      "acme",
		GitHubSecretPath: "/ai-desktops/acme/github",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	// The rendered output should reference the secret path, not a literal secret value.
	// In this template the token is retrieved at runtime via AWS CLI, so no literal
	// token should appear.
	if strings.Contains(out, "ghp_") {
		t.Error("rendered cloud-init must not contain a literal GitHub PAT")
	}
}

func TestRenderCloudInit_validYAML(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:        "d-yaml",
		Hostname:         "d-yaml.desktops.orchael.dev",
		GitHubOwner:      "acme",
		Repos:            []string{"github.com/acme/myrepo"},
		WorkspacePath:    "/workspace",
		AWSRegion:        "us-east-1",
		Environment:      "dev",
		GitHubSecretPath: "/ai-desktops/acme/github",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	var v any
	if err := yaml.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("rendered cloud-init is not valid YAML: %v", err)
	}
}

func TestRenderCloudInit_runcmdEntriesAreStrings(t *testing.T) {
	for _, preInstalled := range []bool{false, true} {
		cfg := &BootstrapConfig{
			DesktopID:            "d-runcmd",
			Hostname:             "d-runcmd.desktops.orchael.dev",
			GitHubOwner:          "acme",
			Repos:                []string{"github.com/acme/repo"},
			WorkspacePath:        "/workspace",
			AWSRegion:            "us-east-1",
			GitHubSecretPath:     "/ai-desktops/acme/github",
			PackagesPreInstalled: preInstalled,
		}
		out, err := RenderCloudInit(cfg)
		if err != nil {
			t.Fatalf("RenderCloudInit (preInstalled=%v): %v", preInstalled, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("YAML parse (preInstalled=%v): %v", preInstalled, err)
		}
		runcmd, ok := doc["runcmd"].([]any)
		if !ok {
			t.Fatalf("runcmd is not a list (preInstalled=%v)", preInstalled)
		}
		for i, entry := range runcmd {
			switch entry.(type) {
			case string:
				// valid
			case []any:
				// valid — array form
			default:
				t.Errorf("runcmd[%d] is %T, not a string or array (preInstalled=%v); value: %v", i, entry, preInstalled, entry)
			}
		}
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
	if !strings.Contains(out, AIAgentBridgeVersion) {
		t.Errorf("ai-agent-bridge install must reference version %s", AIAgentBridgeVersion)
	}
}

func TestRenderCloudInit_packagesPreInstalled(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:            "d-ami",
		Hostname:             "d-ami.desktops.orchael.dev",
		GitHubOwner:          "acme",
		Repos:                []string{"github.com/acme/repo"},
		PackagesPreInstalled: true,
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	// Package list should be absent when pre-installed
	if strings.Contains(out, "packages:") {
		t.Error("packages: block should be absent when PackagesPreInstalled is true")
	}

	// package_update should be absent
	if strings.Contains(out, "package_update:") {
		t.Error("package_update should be absent when PackagesPreInstalled is true")
	}

	// Snap nvim install should be absent
	if strings.Contains(out, "snap install nvim") {
		t.Error("snap nvim install should be absent when PackagesPreInstalled is true")
	}

	// novnc-desktop curl install should be absent
	if strings.Contains(out, "raw.githubusercontent.com/orchael/novnc-desktop") {
		t.Error("novnc-desktop install curl should be absent when PackagesPreInstalled is true")
	}

	// ai-agent-bridge Docker pull should be absent for AMI path (bridge is pre-installed)
	if strings.Contains(out, "ghcr.io/markcallen/ai-agent-bridge") {
		t.Error("ai-agent-bridge Docker pull should be absent when PackagesPreInstalled is true")
	}

	// ai-desktops-setup-tls should be invoked (handles TLS, nginx, certbot)
	if !strings.Contains(out, "ai-desktops-setup-tls") {
		t.Error("ai-desktops-setup-tls should be invoked when PackagesPreInstalled is true")
	}
	if !strings.Contains(out, "NOVNC_HTTP_PORT") {
		t.Error("NOVNC_HTTP_PORT should be set for ai-desktops-setup-tls")
	}

	// systemctl enable/start for novnc-desktop and bridge should be present
	if !strings.Contains(out, "systemctl enable novnc-desktop") {
		t.Error("should enable novnc-desktop service when PackagesPreInstalled is true")
	}
	if !strings.Contains(out, "systemctl start novnc-desktop") {
		t.Error("should start novnc-desktop service when PackagesPreInstalled is true")
	}
	if !strings.Contains(out, "systemctl enable ai-agent-bridge") {
		t.Error("should enable ai-agent-bridge service")
	}
	if !strings.Contains(out, "systemctl start ai-agent-bridge") {
		t.Error("should start ai-agent-bridge service")
	}

	// Repo cloning should still be present
	if !strings.Contains(out, "github.com/acme/repo") {
		t.Error("repo cloning should still be present")
	}
}

func TestRenderCloudInit_packagesNotPreInstalled(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:            "d-cloud",
		Hostname:             "d-cloud.desktops.orchael.dev",
		GitHubOwner:          "acme",
		PackagesPreInstalled: false,
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	// Package list should be present when not pre-installed
	if !strings.Contains(out, "packages:") {
		t.Error("packages: block should be present when PackagesPreInstalled is false")
	}
	if !strings.Contains(out, "package_update:") {
		t.Error("package_update should be present when PackagesPreInstalled is false")
	}

	// Snap nvim install should be present
	if !strings.Contains(out, "snap install nvim") {
		t.Error("snap nvim install should be present when PackagesPreInstalled is false")
	}

	// ai-agent-bridge Docker pull should be present (no curl installer; Docker image used)
	if !strings.Contains(out, "ghcr.io/markcallen/ai-agent-bridge") {
		t.Error("ai-agent-bridge Docker pull should be present when PackagesPreInstalled is false")
	}

	// nginx TLS config should be absent (novnc-desktop install handles it)
	if strings.Contains(out, "novnc-desktop-tls.conf") {
		t.Error("nginx TLS config should be absent when PackagesPreInstalled is false")
	}
}
