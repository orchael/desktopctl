package pulumi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFoundationStackRef(t *testing.T) {
	ref := FoundationStackRef("s3://my-bucket", "dev", "/infra/pulumi/foundation")
	if ref.Project != "foundation" {
		t.Errorf("project: got %q", ref.Project)
	}
	if ref.StackName != "foundation-dev" {
		t.Errorf("stack: got %q", ref.StackName)
	}
	if ref.FullName() != "foundation/foundation-dev" {
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
		"t3.large", "subnet-abc", "sg-abc", "my-profile", "my-keypair",
		"/ai-desktops/github/pat", []string{"github.com/acme/app"}, 9445,
		"", "", "dev")

	if cfg["desktopId"] != "d-001" {
		t.Errorf("desktopId: got %q", cfg["desktopId"])
	}
	if cfg["repos"] != "github.com/acme/app" {
		t.Errorf("repos: got %q", cfg["repos"])
	}
	if cfg["subnetId"] != "subnet-abc" {
		t.Errorf("subnetId: got %q", cfg["subnetId"])
	}
	if cfg["sshKeyName"] != "my-keypair" {
		t.Errorf("sshKeyName: got %q", cfg["sshKeyName"])
	}
}

func TestDesktopConfig_withAMI(t *testing.T) {
	cfg := DesktopConfig("us-east-1", "d-ami", "acme", "desktops.orchael.dev",
		"t3.large", "subnet-abc", "sg-abc", "my-profile", "",
		"/ai-desktops/github/pat", []string{}, 9445,
		"ami-0abc123", "my-user-data", "dev")

	if cfg["amiId"] != "ami-0abc123" {
		t.Errorf("amiId: got %q", cfg["amiId"])
	}
	if cfg["userData"] != "my-user-data" {
		t.Errorf("userData: got %q", cfg["userData"])
	}
	if _, ok := cfg["sshKeyName"]; ok {
		t.Error("sshKeyName should not be set when empty")
	}
}

func TestFoundationConfig(t *testing.T) {
	cfg := FoundationConfig("us-east-1", "desktops.orchael.dev", "ai-desktops-fleet", "0.0.0.0/0", "dev")
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
		OutputSubnetID:        "subnet-abc",
		OutputSGID:            "sg-abc",
		OutputInstanceProfile: "my-profile",
		OutputZoneID:          "Z12345",
	}
	if err := ValidateFoundationOutputs(good); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	bad := map[string]string{OutputSubnetID: "subnet-abc"}
	if err := ValidateFoundationOutputs(bad); err == nil {
		t.Error("expected error for incomplete outputs")
	}
}

// fakePulumiDir writes a shell script named "pulumi" into a temp directory
// and prepends it to PATH so exec.Command("pulumi", ...) runs it instead of
// the real binary. exitCode controls the exit status; stdout is printed on
// every invocation.
func fakePulumiDir(t *testing.T, exitCode int, stdout string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s'\nexit " + string(rune('0'+exitCode)) + "\n"
	if stdout != "" {
		script = "#!/bin/sh\nprintf '" + stdout + "'\nexit " + string(rune('0'+exitCode)) + "\n"
	}
	p := filepath.Join(dir, "pulumi")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func TestNewRunner(t *testing.T) {
	r := NewRunner()
	if r == nil {
		t.Error("expected non-nil runner")
	}
}

func TestRunnerUp_Success(t *testing.T) {
	fakePulumiDir(t, 0, `{}`)
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-test", workDir)
	outputs, err := NewRunner().Up(context.Background(), ref, StackConfig{"aws:region": "us-east-1"}, nil)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if outputs == nil {
		t.Error("expected non-nil outputs map")
	}
}

func TestRunnerUp_Failure(t *testing.T) {
	fakePulumiDir(t, 1, "")
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-test", workDir)
	_, err := NewRunner().Up(context.Background(), ref, StackConfig{}, nil)
	if err == nil {
		t.Error("expected error when pulumi exits non-zero")
	}
}

func TestRunnerPreview_Success(t *testing.T) {
	fakePulumiDir(t, 0, "")
	workDir := t.TempDir()
	ref := FoundationStackRef("s3://bucket", "dev", workDir)
	if err := NewRunner().Preview(context.Background(), ref, StackConfig{}, nil); err != nil {
		t.Fatalf("Preview: %v", err)
	}
}

func TestRunnerDestroy_Success(t *testing.T) {
	fakePulumiDir(t, 0, "")
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-test", workDir)
	if err := NewRunner().Destroy(context.Background(), ref, nil); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

func TestRunnerDestroy_Failure(t *testing.T) {
	fakePulumiDir(t, 1, "")
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-test", workDir)
	if err := NewRunner().Destroy(context.Background(), ref, nil); err == nil {
		t.Error("expected error when pulumi exits non-zero")
	}
}
