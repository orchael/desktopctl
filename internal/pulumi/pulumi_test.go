package pulumi

import "testing"

func TestFoundationStackRef(t *testing.T) {
	ref := FoundationStackRef("s3://my-bucket", "dev", "/infra/pulumi/foundation")
	if ref.Project != "ai-desktops" {
		t.Errorf("project: got %q", ref.Project)
	}
	if ref.StackName != "foundation-dev" {
		t.Errorf("stack: got %q", ref.StackName)
	}
	if ref.FullName() != "ai-desktops/foundation-dev" {
		t.Errorf("full name: got %q", ref.FullName())
	}
}

func TestDesktopStackRef(t *testing.T) {
	ref := DesktopStackRef("s3://my-bucket", "d-001abc", "/infra/pulumi/desktop")
	if ref.StackName != "desktop-d-001abc" {
		t.Errorf("stack: got %q", ref.StackName)
	}
}

func TestDesktopConfig(t *testing.T) {
	cfg := DesktopConfig("us-east-1", "d-001", "acme", "desktops.orchael.dev",
		"t3.large", "10.0.0.0/8", "/home/user/.ssh/id_rsa", "/ai-desktops/github/pat",
		[]string{"github.com/acme/app"})

	if cfg["desktopId"] != "d-001" {
		t.Errorf("desktopId: got %q", cfg["desktopId"])
	}
	if cfg["repos"] != "github.com/acme/app" {
		t.Errorf("repos: got %q", cfg["repos"])
	}
}

func TestFoundationConfig(t *testing.T) {
	cfg := FoundationConfig("us-east-1", "desktops.orchael.dev", "ai-desktops-fleet", "0.0.0.0/0")
	if cfg["zone"] != "desktops.orchael.dev" {
		t.Errorf("zone: got %q", cfg["zone"])
	}
}

func TestParseOutputs(t *testing.T) {
	raw := map[string]any{
		"instanceId": "i-abc123",
		"hostname":   "d-001.desktops.orchael.dev",
		"count":      42,
		"nested":     map[string]any{"k": "v"},
	}
	out := ParseOutputs(raw)
	if out["instanceId"] != "i-abc123" {
		t.Errorf("instanceId: got %q", out["instanceId"])
	}
	if _, ok := out["count"]; ok {
		t.Error("non-string value should be excluded")
	}
}

func TestValidateFoundationOutputs(t *testing.T) {
	good := map[string]string{
		OutputSubnetID:         "subnet-abc",
		OutputSGID:             "sg-abc",
		OutputInstanceProfile:  "my-profile",
		OutputZoneID:           "Z12345",
	}
	if err := ValidateFoundationOutputs(good); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	bad := map[string]string{OutputSubnetID: "subnet-abc"}
	if err := ValidateFoundationOutputs(bad); err == nil {
		t.Error("expected error for incomplete outputs")
	}
}
