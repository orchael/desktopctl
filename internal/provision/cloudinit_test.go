package provision

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/config"
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
		"bridgectl",
		"docker",
		"tmux",
		"certbot",
		"dns-route53",
		"d-001.desktops.orchael.dev",
		"npm.pkg.github.com",
		"/home/ubuntu/.npmrc",
		"/home/ubuntu/.config/gh/hosts.yml",
		"gh auth setup-git --hostname github.com",
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
	cases := []struct {
		name string
		cfg  *BootstrapConfig
	}{
		{
			name: "without agent secret",
			cfg: &BootstrapConfig{
				DesktopID:        "d-yaml",
				Hostname:         "d-yaml.desktops.orchael.dev",
				GitHubOwner:      "acme",
				Repos:            []string{"github.com/acme/myrepo"},
				WorkspacePath:    "/workspace",
				AWSRegion:        "us-east-1",
				Environment:      "dev",
				GitHubSecretPath: "/ai-desktops/acme/github",
			},
		},
		{
			name: "with agent secret",
			cfg: &BootstrapConfig{
				DesktopID:        "d-yaml",
				Hostname:         "d-yaml.desktops.orchael.dev",
				GitHubOwner:      "acme",
				Repos:            []string{"github.com/acme/myrepo"},
				WorkspacePath:    "/workspace",
				AWSRegion:        "us-east-1",
				Environment:      "dev",
				GitHubSecretPath: "/ai-desktops/acme/github",
				AgentSecretPath:  "/ai-desktops/acme/agents",
			},
		},
		{
			name: "with tailscale and step-ca",
			cfg: &BootstrapConfig{
				DesktopID:           "d-yaml",
				Hostname:            "d-yaml.desktops.orchael.dev",
				GitHubOwner:         "acme",
				WorkspacePath:       "/workspace",
				AWSRegion:           "us-east-1",
				Environment:         "dev",
				GitHubSecretPath:    "/ai-desktops/acme/github",
				TailscaleNetwork:    "acme-tailnet",
				TailscaleSecretPath: "/ai-desktops/acme/tailscale/acme-tailnet",
				StepCAServerDNS:     "ca.tailnet.ts.net",
				StepCAProvisioner:   "ai-desktops",
				StepCASecretPath:    "/ai-desktops/acme/step-ca/ca.tailnet.ts.net",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := RenderCloudInit(tc.cfg)
			if err != nil {
				t.Fatalf("RenderCloudInit: %v", err)
			}

			var v any
			if err := yaml.Unmarshal([]byte(out), &v); err != nil {
				t.Errorf("rendered cloud-init is not valid YAML: %v", err)
			}
		})
	}
}

func TestRenderCloudInit_tailscale(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:           "d-ts",
		Hostname:            "d-ts.desktops.orchael.dev",
		GitHubOwner:         "acme",
		GitHubSecretPath:    "/ai-desktops/acme/github",
		AWSRegion:           "us-east-1",
		TailscaleNetwork:    "acme-tailnet",
		TailscaleSecretPath: "/ai-desktops/acme/tailscale/acme-tailnet",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"tailscale.com/install.sh",
		"TAILSCALE_INSTALL=$(mktemp)",
		"systemctl enable tailscaled",
		"/ai-desktops/acme/tailscale/acme-tailnet",
		"TS_AUTHKEY",
		"tailscale up",
		"--hostname \"$TAILSCALE_HOSTNAME\"",
		"--ssh=true",
		"TAILSCALE_NETWORK",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("Tailscale block missing %q", want)
		}
	}
	if strings.Contains(out, "curl -fsSL https://tailscale.com/install.sh | sh") {
		t.Error("Tailscale install should not pipe curl directly into sh")
	}
	if strings.Contains(out, "--ssh=false") {
		t.Error("Tailscale SSH should be enabled so tailscale ssh can verify desktops")
	}
}

func TestRenderCloudInit_stepCAWaitsForDNSAndRestartsAfterTailscale(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:           "d-ca",
		Hostname:            "d-ca.desktops.orchael.dev",
		GitHubOwner:         "acme",
		GitHubSecretPath:    "/ai-desktops/acme/github",
		AWSRegion:           "us-east-1",
		TailscaleNetwork:    "acme-tailnet",
		TailscaleSecretPath: "/ai-desktops/acme/tailscale/acme-tailnet",
		StepCAServerDNS:     "ca.tailnet.ts.net",
		StepCAFingerprint:   "abcdef",
		StepCAProvisioner:   "ai-desktops",
		StepCASecretPath:    "/ai-desktops/acme/step-ca/ca.tailnet.ts.net",
		StepCAClients: []StepCAClient{
			{Issuer: "mark-macbook", PublicKey: "ssh-ed25519 AAAA mark", Required: true},
		},
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"getent hosts \"$STEP_CA\"",
		"packages.smallstep.com/stable/debian",
		"STEP_CA_ROOT=\"/root/.step/certs/root_ca.crt\"",
		"STEP_CA_API_ROOT=\"$STEP_CA_ROOT\"",
		"step ca health --ca-url \"https://${STEP_CA}\" --root \"$STEP_CA_ROOT\"",
		"step ca bootstrap --ca-url \"https://${STEP_CA}\" --fingerprint \"$STEP_CA_FINGERPRINT\" --install --force",
		"/etc/ssl/certs/ISRG_Root_X1.pem",
		"/etc/ssl/certs/ISRG_Root_X2.pem",
		"python3-yaml",
		"STEP_CA_PROVISIONER_PASSWORD",
		"STEP_CA_PASSWORD_FILE=$(mktemp)",
		"STEP_CA_TOKEN_FILE=$(mktemp)",
		"STEP_CA_CSR_FILE=$(mktemp)",
		"STEP_CA_SIGN_REQUEST=$(mktemp)",
		"STEP_CA_SIGN_RESPONSE=$(mktemp)",
		"TAILSCALE_DNS_NAME=$(tailscale status --json",
		"DNS:${TAILSCALE_DNS_NAME}",
		"--san \"$TAILSCALE_DNS_NAME\"",
		"step ca token",
		"--root \"$STEP_CA_API_ROOT\"",
		"openssl req -new",
		"-addext \"subjectAltName=${CSR_SANS}\"",
		"json.dump({\"csr\": csr, \"ott\": token}, output_file)",
		"\"https://${STEP_CA}/1.0/sign\"",
		"cert_file.write(response[\"crt\"])",
		"server.crt",
		"install -o ubuntu -g ubuntu -m 0644 \"$STEP_CA_ROOT\" \"$CERT_DIR/step-ca-root.crt\"",
		"bridgectl.service.d/step-ca.conf",
		"EnvironmentFile=-%%h/.config/bridgectl/step-ca.env",
		"TAILSCALE_IP=$(tailscale ip -4 | head -n 1)",
		"server[\"listen\"]",
		"\"$TAILSCALE_IP:9445\"",
		"/home/ubuntu/.ai-agent-bridge/certs/jwt-clients",
		"mark-macbook.pub",
		"STEP_CA_CLIENTS_JSON_B64",
		"step_ca_config[\"clients\"]",
		"step-ca-root.crt",
		"config[\"tls\"]",
		"server.key",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("step-ca block missing %q", want)
		}
	}
	if strings.Contains(out, "/tmp/step-ca-password") {
		t.Error("step-ca password file should use mktemp, not a fixed /tmp path")
	}
	if strings.Contains(out, "step ca certificate") {
		t.Error("step-ca certificate issuance should use the sign API, not step ca certificate")
	}
	if strings.Contains(out, "install -o ubuntu -g ubuntu -m 0644 \"$STEP_CA_API_ROOT\" \"$CERT_DIR/step-ca-root.crt\"") {
		t.Error("bridgectl client CA bundle must use the Step CA root, not the API fallback root")
	}

	if strings.Index(out, "Tailscale network attachment") > strings.Index(out, "step-ca trust/bootstrap") {
		t.Error("step-ca block should render after Tailscale so private CA DNS can become available first")
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

	// Verify install scripts do not reference floating "main" branches.
	if strings.Contains(out, "/main/install.sh") {
		t.Error("install scripts must not reference the 'main' branch; pin to a release tag")
	}
	// Cloud-init verifies the AMI's baked package and corrects drift to the
	// exact version expected by the CLI.
	wantVersion := strings.TrimPrefix(AIAgentBridgeVersion, "v")
	if !strings.Contains(out, `EXPECTED_BRIDGE_VERSION="`+wantVersion+`"`) {
		t.Errorf("cloud-init should include expected bridge package version %s", wantVersion)
	}
	if !strings.Contains(out, `"ai-agent-bridge=${EXPECTED_BRIDGE_VERSION}"`) {
		t.Error("cloud-init should install the exact ai-agent-bridge package version when the AMI drifts")
	}
	if !strings.Contains(out, "install-provider-runtime") {
		t.Error("cloud-init should refresh provider runtime after ai-agent-bridge version correction")
	}
	// cloud-init must not pull or start the old system bridge daemon.
	if strings.Contains(out, "systemctl enable ai-agent-bridge") {
		t.Error("cloud-init must not enable the ai-agent-bridge system daemon")
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

	// ai-agent-bridge Docker pull must be absent (bridge daemon is replaced by bridgectl)
	if strings.Contains(out, "ghcr.io/markcallen/ai-agent-bridge") {
		t.Error("ai-agent-bridge Docker pull must be absent (bridge daemon replaced by bridgectl user service)")
	}

	// ai-agent-bridge system daemon must not be enabled or started
	if strings.Contains(out, "systemctl enable ai-agent-bridge") {
		t.Error("must not enable ai-agent-bridge system daemon (replaced by bridgectl user service)")
	}
	if strings.Contains(out, "systemctl start ai-agent-bridge") {
		t.Error("must not start ai-agent-bridge system daemon (replaced by bridgectl user service)")
	}

	// ballast update should run to fetch the latest version
	if !strings.Contains(out, "ballast update") {
		t.Error("ballast update should be present when PackagesPreInstalled is true")
	}

	// ai-desktops-setup-tls should be invoked (handles TLS, nginx, certbot)
	if !strings.Contains(out, "ai-desktops-setup-tls") {
		t.Error("ai-desktops-setup-tls should be invoked when PackagesPreInstalled is true")
	}
	if !strings.Contains(out, "NOVNC_HTTP_PORT") {
		t.Error("NOVNC_HTTP_PORT should be set for ai-desktops-setup-tls")
	}

	// novnc-desktop service should still be enabled and started
	if !strings.Contains(out, "systemctl enable novnc-desktop") {
		t.Error("should enable novnc-desktop service when PackagesPreInstalled is true")
	}
	if !strings.Contains(out, "systemctl start novnc-desktop") {
		t.Error("should start novnc-desktop service when PackagesPreInstalled is true")
	}

	// bridgectl user service should be enabled and started
	if !strings.Contains(out, "systemctl --user enable bridgectl") {
		t.Error("should enable bridgectl systemd user service")
	}
	if !strings.Contains(out, "systemctl --user start bridgectl") {
		t.Error("should start bridgectl systemd user service")
	}

	// Repo cloning should still be present
	if !strings.Contains(out, "github.com/acme/repo") {
		t.Error("repo cloning should still be present")
	}
}

func TestRenderCloudInit_desktopSecretPaths(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:          "d-sec",
		Hostname:           "d-sec.desktops.orchael.dev",
		GitHubOwner:        "acme",
		GitHubSecretPath:   "/ai-desktops/acme/github",
		AWSRegion:          "us-east-1",
		Environment:        "dev",
		DesktopSecretPaths: []string{"/ai-desktops/acme/myapp", "/ai-desktops/acme/shared"},
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"/ai-desktops/acme/myapp",
		"/ai-desktops/acme/shared",
		"environment.d",
		"desktop-secrets.conf",
		".desktop-secrets",
		".bashrc",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}

	var v any
	if err := yaml.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("rendered cloud-init with desktop secrets is not valid YAML: %v", err)
	}
}

func TestRenderCloudInit_noDesktopSecretPaths(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:        "d-nosec",
		Hostname:         "d-nosec.desktops.orchael.dev",
		GitHubOwner:      "acme",
		GitHubSecretPath: "/ai-desktops/acme/github",
		AWSRegion:        "us-east-1",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	if strings.Contains(out, "desktop-secrets.conf") {
		t.Error("desktop-secrets.conf should be absent when DesktopSecretPaths is empty")
	}
	if strings.Contains(out, ".desktop-secrets") {
		t.Error(".desktop-secrets should be absent when DesktopSecretPaths is empty")
	}
}

func TestRenderCloudInit_swapPresent(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-swap",
		Hostname:    "d-swap.desktops.orchael.dev",
		GitHubOwner: "acme",
		AWSRegion:   "us-east-1",
		SwapSizeGB:  16,
	}
	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"swapfile",
		"fallocate",
		"mkswap",
		"swapon",
		"/etc/fstab",
		"vm.swappiness",
		"16",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("swap block missing %q", want)
		}
	}

	var v any
	if err := yaml.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("rendered cloud-init with swap is not valid YAML: %v", err)
	}
}

func TestRenderCloudInit_swapAbsent(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-noswap",
		Hostname:    "d-noswap.desktops.orchael.dev",
		GitHubOwner: "acme",
		AWSRegion:   "us-east-1",
		SwapSizeGB:  0,
	}
	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}
	if strings.Contains(out, "fallocate") || strings.Contains(out, "mkswap") {
		t.Error("swap commands should be absent when SwapSizeGB is 0")
	}
}

func TestRenderCloudInit_cloudWatch(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-cw",
		Hostname:    "d-cw.desktops.orchael.dev",
		GitHubOwner: "acme",
		AWSRegion:   "us-east-1",
	}
	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"amazon-cloudwatch-agent",
		"ai-desktops/syslog",
		"ai-desktops/cloud-init",
		"retention_in_days",
		"mem_used_percent",
		"disk_used_percent",
		"swap_used_percent",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("CloudWatch block missing %q", want)
		}
	}

	var v any
	if err := yaml.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("rendered cloud-init with CloudWatch is not valid YAML: %v", err)
	}
}

func TestRenderCloudInit_withAVDs(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:            "d-avd",
		Hostname:             "d-avd.desktops.orchael.dev",
		GitHubOwner:          "acme",
		AWSRegion:            "us-east-1",
		PackagesPreInstalled: true,
		AVDs: []config.AVDConfig{
			{Name: "flutter_dev", Image: "system-images;android-35;google_apis;x86_64", Device: "pixel_6"},
			{Name: "wear_dev", Image: "system-images;android-33;google_apis;x86_64"},
		},
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	checks := []string{
		"ansible-playbook",
		"/opt/ai-desktops/desktop-setup.yml",
		"base64 -d",
		"avd-vars.json",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("AVD block missing %q", want)
		}
	}

	// Decode the base64 blob and verify AVD content is present in the JSON.
	re := regexp.MustCompile(`echo '([A-Za-z0-9+/=]+)' \| base64 -d`)
	m := re.FindStringSubmatch(out)
	if len(m) < 2 {
		t.Fatal("could not find base64-encoded AVD vars in rendered output")
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	jsonChecks := []string{
		"flutter_dev",
		"system-images;android-35;google_apis;x86_64",
		"pixel_6",
		"wear_dev",
		"system-images;android-33;google_apis;x86_64",
	}
	for _, want := range jsonChecks {
		if !strings.Contains(string(decoded), want) {
			t.Errorf("decoded AVD JSON missing %q", want)
		}
	}

	var v any
	if err := yaml.Unmarshal([]byte(out), &v); err != nil {
		t.Errorf("rendered cloud-init with AVDs is not valid YAML: %v", err)
	}
}

func TestRenderCloudInit_noAVDs(t *testing.T) {
	cfg := &BootstrapConfig{
		DesktopID:   "d-noavd",
		Hostname:    "d-noavd.desktops.orchael.dev",
		GitHubOwner: "acme",
		AWSRegion:   "us-east-1",
	}

	out, err := RenderCloudInit(cfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}

	if strings.Contains(out, "avdmanager") {
		t.Error("avdmanager should be absent when no AVDs are configured")
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

	// ai-agent-bridge system daemon must not appear (replaced by bridgectl user service)
	if strings.Contains(out, "ghcr.io/markcallen/ai-agent-bridge") {
		t.Error("ai-agent-bridge Docker pull must be absent (bridge daemon replaced by bridgectl user service)")
	}
	if strings.Contains(out, "systemctl enable ai-agent-bridge") {
		t.Error("must not enable ai-agent-bridge system daemon (replaced by bridgectl user service)")
	}

	// nginx TLS config should be absent (novnc-desktop install handles it)
	if strings.Contains(out, "novnc-desktop-tls.conf") {
		t.Error("nginx TLS config should be absent when PackagesPreInstalled is false")
	}

	// ballast update should not run when packages are not pre-installed (no AMI, no brew)
	if strings.Contains(out, "ballast update") {
		t.Error("ballast update should be absent when PackagesPreInstalled is false")
	}
}
