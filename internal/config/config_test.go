package config

import (
	"os"
	"path/filepath"
	"testing"
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
	if c.Fleet.TableName != DefaultFleetTable {
		t.Errorf("default table: got %q", c.Fleet.TableName)
	}
	if c.Agent.BridgePort != DefaultBridgePort {
		t.Errorf("default bridge port: got %d", c.Agent.BridgePort)
	}
}

func TestValidate(t *testing.T) {
	c := &Config{Fleet: FleetConfig{Environment: EnvDev}}
	c.Defaults()
	if err := c.Validate(); err == nil {
		t.Error("expected error when backend_bucket is empty")
	}

	c.Pulumi.BackendBucket = "my-bucket"
	if err := c.Validate(); err == nil {
		t.Error("expected error when operator_cidr is empty")
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
  environment: prod
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
}
