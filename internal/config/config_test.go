package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDNSZone(t *testing.T) {
	tests := []struct {
		env      string
		wantZone string
		wantErr  bool
	}{
		{EnvProd, ZoneProd, false},
		{EnvDev, ZoneDev, false},
		{"", ZoneDev, false},
		{"staging", "", true},
	}
	for _, tt := range tests {
		c := &Config{Fleet: FleetConfig{Environment: tt.env}}
		got, err := c.DNSZone()
		if (err != nil) != tt.wantErr {
			t.Errorf("env=%q: unexpected error %v", tt.env, err)
		}
		if got != tt.wantZone {
			t.Errorf("env=%q: got zone %q, want %q", tt.env, got, tt.wantZone)
		}
	}
}

func TestDefaults(t *testing.T) {
	c := &Config{}
	c.Defaults()
	if c.AWS.Region != "us-east-1" {
		t.Errorf("default region: got %q", c.AWS.Region)
	}
	if c.Fleet.Environment != EnvDev {
		t.Errorf("default env: got %q", c.Fleet.Environment)
	}
	if c.Fleet.TableName != DefaultFleetTablePrefix+"-"+EnvDev {
		t.Errorf("default table: got %q", c.Fleet.TableName)
	}
	if c.Fleet.AMITableName != DefaultAMITablePrefix+"-"+EnvDev {
		t.Errorf("default AMI table: got %q", c.Fleet.AMITableName)
	}
	if c.Agent.BridgePort != DefaultBridgePort {
		t.Errorf("default bridge port: got %d", c.Agent.BridgePort)
	}
	if len(c.Desktop.InstanceTypes) != len(DefaultSpotInstanceTypes) {
		t.Fatalf("default spot instance types: got %v", c.Desktop.InstanceTypes)
	}
	for i, want := range DefaultSpotInstanceTypes {
		if c.Desktop.InstanceTypes[i] != want {
			t.Fatalf("default spot instance types: got %v, want %v", c.Desktop.InstanceTypes, DefaultSpotInstanceTypes)
		}
	}
	if c.PKI.StepCAProvisioner != "admin" {
		t.Errorf("default step-ca provisioner: got %q", c.PKI.StepCAProvisioner)
	}
}

func TestDefaultVolumeSizes(t *testing.T) {
	if DefaultVolumeSize != 64 {
		t.Errorf("DefaultVolumeSize = %d, want 64", DefaultVolumeSize)
	}
	if DefaultMobileVolumeSize != 200 {
		t.Errorf("DefaultMobileVolumeSize = %d, want 200", DefaultMobileVolumeSize)
	}
}

func TestValidate(t *testing.T) {
	c := &Config{Fleet: FleetConfig{Environment: EnvDev}}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Error("expected error when backend_bucket is empty")
	}

	c.Pulumi.BackendBucket = "my-bucket"
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error with empty operator_cidr (should default to 0.0.0.0/0): %v", err)
	}
	if c.Desktop.OperatorCIDR != "0.0.0.0/0" {
		t.Errorf("expected operator_cidr to default to 0.0.0.0/0, got %q", c.Desktop.OperatorCIDR)
	}

	c.Desktop.OperatorCIDR = "203.0.113.1/32"
	if err := c.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadOrDefault_noFile(t *testing.T) {
	c, err := LoadOrDefault("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil config")
	}
	// Defaults should be applied.
	if c.AWS.Region != "us-east-1" {
		t.Errorf("got region %q", c.AWS.Region)
	}
}

func TestLoad(t *testing.T) {
	yaml := `
aws:
  region: us-west-2
  profile: myprofile
pulumi:
  backend_bucket: my-state-bucket
fleet:
  table_name: my-fleet
  organization_id: 00000000-0000-4000-8000-000000000001
  environment: prod
github:
  owner: acme
  npm_github_scopes:
    - private-tools
network:
  tailscale_network: acme-tailnet
operator:
  secret: /ai-desktops/acme-ops
pki:
  step_ca_server: ca.acme-tailnet.ts.net
  step_ca_provisioner: ops
  step_ca_fingerprint: abcdef
  step_ca_clients:
    - issuer: mark-macbook
      public_key_path: /Users/mark/.config/bridgectl/certs/jwt-signing.pub
      required: true
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Fleet.Environment != "prod" {
		t.Errorf("env: got %q", c.Fleet.Environment)
	}
	if c.AWS.Region != "us-west-2" {
		t.Errorf("region: got %q", c.AWS.Region)
	}
	if c.Pulumi.BackendBucket != "my-state-bucket" {
		t.Errorf("bucket: got %q", c.Pulumi.BackendBucket)
	}
	if c.Fleet.TableName != "my-fleet" {
		t.Errorf("table: got %q", c.Fleet.TableName)
	}
	if c.Fleet.OrganizationID != "00000000-0000-4000-8000-000000000001" {
		t.Errorf("organization ID: got %q", c.Fleet.OrganizationID)
	}
	if c.Network.TailscaleNetwork != "acme-tailnet" {
		t.Errorf("tailscale network: got %q", c.Network.TailscaleNetwork)
	}
	if c.Operator.Secret != "/ai-desktops/acme-ops" {
		t.Errorf("operator secret: got %q", c.Operator.Secret)
	}
	if len(c.GitHub.NPMGitHubScopes) != 1 || c.GitHub.NPMGitHubScopes[0] != "private-tools" {
		t.Errorf("npm GitHub scopes: got %#v", c.GitHub.NPMGitHubScopes)
	}
	if c.PKI.StepCAServer != "ca.acme-tailnet.ts.net" {
		t.Errorf("step-ca server: got %q", c.PKI.StepCAServer)
	}
	if c.PKI.StepCAProvisioner != "ops" {
		t.Errorf("step-ca provisioner: got %q", c.PKI.StepCAProvisioner)
	}
	if c.PKI.StepCAFingerprint != "abcdef" {
		t.Errorf("step-ca fingerprint: got %q", c.PKI.StepCAFingerprint)
	}
	if len(c.PKI.StepCAClients) != 1 {
		t.Fatalf("step-ca clients: got %d", len(c.PKI.StepCAClients))
	}
	if c.PKI.StepCAClients[0].Issuer != "mark-macbook" {
		t.Errorf("step-ca client issuer: got %q", c.PKI.StepCAClients[0].Issuer)
	}
	if c.PKI.StepCAClients[0].PublicKeyPath != "/Users/mark/.config/bridgectl/certs/jwt-signing.pub" {
		t.Errorf("step-ca client public key path: got %q", c.PKI.StepCAClients[0].PublicKeyPath)
	}
	if !c.PKI.StepCAClients[0].Required {
		t.Error("step-ca client required should be true")
	}
}

func TestSave_roundTrip(t *testing.T) {
	c := &Config{
		AWS:    AWSConfig{Region: "us-west-2", Profile: "prod"},
		Pulumi: PulumiConfig{BackendBucket: "my-bucket"},
		Fleet:  FleetConfig{OrganizationID: "00000000-0000-4000-8000-000000000002"},
		Desktop: DesktopConfig{
			InstanceType:  "t3.large",
			InstanceTypes: []string{"m6i.xlarge", "m5.xlarge"},
			OperatorCIDR:  "203.0.113.1/32",
			ActiveAMI: map[string]string{
				"us-east-1": "ami-0abc123",
				"us-west-2": "ami-0def456",
			},
		},
		Network:  NetworkConfig{TailscaleNetwork: "acme-tailnet"},
		Operator: OperatorConfig{Secret: "/ai-desktops/acme"},
		PKI: PKIConfig{
			StepCAServer:      "ca.acme-tailnet.ts.net",
			StepCAProvisioner: "admin",
			StepCAFingerprint: "abcdef",
			StepCAClients: []StepCAClientConfig{
				{
					Issuer:        "mark-macbook",
					PublicKeyPath: "/Users/mark/.config/bridgectl/certs/jwt-signing.pub",
					Required:      true,
				},
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// Save config
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load it back
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify round-trip
	if loaded.AWS.Region != "us-west-2" {
		t.Errorf("region round-trip: got %q", loaded.AWS.Region)
	}
	if loaded.Desktop.InstanceType != "t3.large" {
		t.Errorf("instance type round-trip: got %q", loaded.Desktop.InstanceType)
	}
	if loaded.Fleet.OrganizationID != "00000000-0000-4000-8000-000000000002" {
		t.Errorf("organization ID round-trip: got %q", loaded.Fleet.OrganizationID)
	}
	if len(loaded.Desktop.InstanceTypes) != 2 || loaded.Desktop.InstanceTypes[0] != "m6i.xlarge" || loaded.Desktop.InstanceTypes[1] != "m5.xlarge" {
		t.Errorf("instance types round-trip: got %v", loaded.Desktop.InstanceTypes)
	}
	if loaded.Desktop.ActiveAMI == nil {
		t.Error("ActiveAMI should not be nil after round-trip")
	} else {
		if amiID, ok := loaded.Desktop.ActiveAMI["us-east-1"]; !ok {
			t.Error("us-east-1 AMI not found in round-trip")
		} else if amiID != "ami-0abc123" {
			t.Errorf("us-east-1 AMI round-trip: got %q", amiID)
		}
	}
	if loaded.Network.TailscaleNetwork != "acme-tailnet" {
		t.Errorf("tailscale network round-trip: got %q", loaded.Network.TailscaleNetwork)
	}
	if loaded.Operator.Secret != "/ai-desktops/acme" {
		t.Errorf("operator secret round-trip: got %q", loaded.Operator.Secret)
	}
	if loaded.PKI.StepCAServer != "ca.acme-tailnet.ts.net" {
		t.Errorf("step-ca server round-trip: got %q", loaded.PKI.StepCAServer)
	}
	if loaded.PKI.StepCAProvisioner != "admin" {
		t.Errorf("step-ca provisioner round-trip: got %q", loaded.PKI.StepCAProvisioner)
	}
	if loaded.PKI.StepCAFingerprint != "abcdef" {
		t.Errorf("step-ca fingerprint round-trip: got %q", loaded.PKI.StepCAFingerprint)
	}
	if len(loaded.PKI.StepCAClients) != 1 || loaded.PKI.StepCAClients[0].Issuer != "mark-macbook" {
		t.Errorf("step-ca clients round-trip: got %+v", loaded.PKI.StepCAClients)
	}
}

func TestDefaults_AMIs(t *testing.T) {
	c := &Config{}
	c.Defaults()
	// Defaults should not initialize an empty ActiveAMI map
	if c.Desktop.ActiveAMI != nil {
		t.Error("ActiveAMI should be nil after Defaults()")
	}
}

func TestConfigExampleVolumeSizeMatchesDefault(t *testing.T) {
	data, err := os.ReadFile("../../config.example.yaml")
	if err != nil {
		t.Fatalf("read config.example.yaml: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "root_volume_size") {
		t.Fatal("config.example.yaml must use desktop.volume_size, not root_volume_size")
	}
	// The field must be documented (even if commented out to show the default is 0/auto).
	if !strings.Contains(text, "volume_size:") {
		t.Fatal("config.example.yaml must document desktop.volume_size")
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		t.Fatalf("parse config.example.yaml: %v", err)
	}
	// The example intentionally leaves volume_size unset (0) so context-sensitive
	// defaults apply: 64 GiB for normal desktops, 200 GiB for mobile/AVD.
	if c.Desktop.VolumeSize != 0 {
		t.Fatalf("config.example.yaml desktop.volume_size = %d, want 0 (unset/auto)", c.Desktop.VolumeSize)
	}
}

func TestDefaults_GitHubSecret(t *testing.T) {
	// No owner: fall back to legacy path
	c := &Config{}
	c.Defaults()
	if c.GitHub.GitHubSecret != "/ai-desktops/github/pat" {
		t.Errorf("no-owner default: got %q", c.GitHub.GitHubSecret)
	}

	// Owner set: derive per-owner path
	c = &Config{GitHub: GitHubConfig{Owner: "myorg"}}
	c.Defaults()
	if c.GitHub.GitHubSecret != "/ai-desktops/myorg/github" {
		t.Errorf("owner default: got %q", c.GitHub.GitHubSecret)
	}
	if c.Operator.Secret != "/ai-desktops/myorg" {
		t.Errorf("operator secret default: got %q", c.Operator.Secret)
	}

	// Legacy pat_secret migrates to github_secret
	c = &Config{GitHub: GitHubConfig{PATSecret: "/ai-desktops/github/pat"}}
	c.Defaults()
	if c.GitHub.GitHubSecret != "/ai-desktops/github/pat" {
		t.Errorf("migration: got %q", c.GitHub.GitHubSecret)
	}

	// Explicit github_secret takes priority
	c = &Config{GitHub: GitHubConfig{GitHubSecret: "/ai-desktops/myorg/github", Owner: "myorg"}}
	c.Defaults()
	if c.GitHub.GitHubSecret != "/ai-desktops/myorg/github" {
		t.Errorf("explicit: got %q", c.GitHub.GitHubSecret)
	}
}

func TestLoad_LegacyPATSecret(t *testing.T) {
	yaml := `
github:
  owner: myorg
  pat_secret: /ai-desktops/github/pat
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Legacy pat_secret should be migrated to github_secret
	if c.GitHub.GitHubSecret != "/ai-desktops/github/pat" {
		t.Errorf("legacy migration: got %q", c.GitHub.GitHubSecret)
	}
}

func TestSupportsNestedVirt(t *testing.T) {
	tests := []struct {
		instanceType string
		want         bool
	}{
		// 5th-gen Intel (launch support, Feb 2026)
		{"c8i.large", true},
		{"c8i.xlarge", true},
		{"m8i.xlarge", true},
		{"m8i.4xlarge", true},
		{"r8i.2xlarge", true},
		{"x8i.xlarge", true},
		// 4th-gen Intel (added June 2026)
		{"c7i.xlarge", true},
		{"c7i.2xlarge", true},
		{"m7i.xlarge", true},
		{"r7i.xlarge", true},
		{"i7i.xlarge", true},
		// unsupported
		{"t3.large", false},
		{"t3.medium", false},
		{"m5.xlarge", false},
		{"c7g.xlarge", false}, // ARM64
		{"c8i", false},        // family prefix only, no size
		{"", false},
	}
	for _, tt := range tests {
		got := SupportsNestedVirt(tt.instanceType)
		if got != tt.want {
			t.Errorf("SupportsNestedVirt(%q) = %v, want %v", tt.instanceType, got, tt.want)
		}
	}
}
