package pulumi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		[]string{"github.com/acme/app"}, 9445, 100,
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
	if cfg["volumeSize"] != "100" {
		t.Errorf("volumeSize: got %q", cfg["volumeSize"])
	}
}

func TestDesktopConfig_withAMI(t *testing.T) {
	cfg := DesktopConfig("us-east-1", "d-ami", "acme", "desktops.orchael.dev",
		"t3.large", "subnet-abc", "sg-abc", "my-profile", "",
		[]string{}, 9445, 0,
		"ami-0abc123", "my-user-data", "dev")

	if cfg["amiId"] != "ami-0abc123" {
		t.Errorf("amiId: got %q", cfg["amiId"])
	}
	if cfg["userDataBase64"] != "my-user-data" {
		t.Errorf("userDataBase64: got %q", cfg["userDataBase64"])
	}
	if _, ok := cfg["sshKeyName"]; ok {
		t.Error("sshKeyName should not be set when empty")
	}
}

func TestFoundationConfig(t *testing.T) {
	cfg := FoundationConfig("us-east-1", "desktops.orchael.dev", "ai-desktops-fleet", "0.0.0.0/0", "dev", false, 0)
	if cfg["zone"] != "desktops.orchael.dev" {
		t.Errorf("zone: got %q", cfg["zone"])
	}
	if _, ok := cfg["wireguardEnabled"]; ok {
		t.Error("wireguardEnabled should not be set when disabled")
	}
}

func TestFoundationConfig_WireGuard(t *testing.T) {
	cfg := FoundationConfig("us-east-1", "desktops.orchael.dev", "ai-desktops-fleet", "0.0.0.0/0", "dev", true, 51820)
	if cfg["wireguardEnabled"] != "true" {
		t.Errorf("wireguardEnabled: got %q", cfg["wireguardEnabled"])
	}
	if cfg["wireguardPort"] != "51820" {
		t.Errorf("wireguardPort: got %q", cfg["wireguardPort"])
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

func TestMergeConfig_WritesNamespacedKeys(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner()

	// Large base64-like value (simulates gzip+base64 user-data).
	largeVal := strings.Repeat("A", 4096)
	cfg := StackConfig{
		"aws:region":     "us-east-1",
		"desktopId":      "d-test",
		"userDataBase64": largeVal,
	}
	if err := r.mergeConfig(dir, "desktop", "desktop-d-test", cfg); err != nil {
		t.Fatalf("mergeConfig: %v", err)
	}

	yamlPath := filepath.Join(dir, "Pulumi.desktop-d-test.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "aws:region") {
		t.Error("expected aws:region in YAML")
	}
	if !strings.Contains(content, "desktop:desktopId") {
		t.Error("expected desktop:desktopId in YAML")
	}
	if !strings.Contains(content, "desktop:userDataBase64") {
		t.Error("expected desktop:userDataBase64 in YAML")
	}
	if !strings.Contains(content, largeVal) {
		t.Error("expected full large value in YAML")
	}
}

func TestMergeConfig_PreservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "Pulumi.foundation-dev.yaml")
	// Pre-existing YAML with encryptionsalt and a config key.
	existing := "encryptionsalt: v1:abc123==\nconfig:\n  foundation:zone: example.com\n"
	if err := os.WriteFile(yamlPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRunner()
	if err := r.mergeConfig(dir, "foundation", "foundation-dev", StackConfig{"fleetTable": "my-table"}); err != nil {
		t.Fatalf("mergeConfig: %v", err)
	}

	data, _ := os.ReadFile(yamlPath)
	content := string(data)
	if !strings.Contains(content, "encryptionsalt") {
		t.Error("encryptionsalt should be preserved")
	}
	if !strings.Contains(content, "foundation:zone") {
		t.Error("existing config key should be preserved")
	}
	if !strings.Contains(content, "foundation:fleetTable") {
		t.Error("new key should be added")
	}
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

func TestRunnerDestroy_LockedCancelRetrySuccess(t *testing.T) {
	// First destroy attempt fails with lock error; cancel succeeds; retry succeeds.
	dir := t.TempDir()
	counter := filepath.Join(dir, "destroy_count")
	script := `#!/bin/sh
cmd="$1"
if [ "$cmd" = "stack" ]; then exit 0; fi
if [ "$cmd" = "cancel" ]; then exit 0; fi
if [ "$cmd" = "destroy" ]; then
  count=0
  [ -f "` + counter + `" ] && count=$(cat "` + counter + `")
  count=$((count+1))
  printf '%d' $count > "` + counter + `"
  if [ "$count" = "1" ]; then
    printf 'error: the stack is currently locked by 1 lock(s)'
    exit 1
  fi
  exit 0
fi
exit 0
`
	p := filepath.Join(dir, "pulumi")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-locked", workDir)
	if err := NewRunner().Destroy(context.Background(), ref, nil); err != nil {
		t.Fatalf("expected success after cancel+retry, got: %v", err)
	}
}

func TestRunnerDestroy_LockedCancelFails(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
cmd="$1"
if [ "$cmd" = "stack" ]; then exit 0; fi
if [ "$cmd" = "cancel" ]; then exit 1; fi
if [ "$cmd" = "destroy" ]; then
  printf 'error: the stack is currently locked by 1 lock(s)'
  exit 1
fi
exit 0
`
	p := filepath.Join(dir, "pulumi")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	workDir := t.TempDir()
	ref := DesktopStackRef("s3://bucket", "d-locked", workDir)
	err := NewRunner().Destroy(context.Background(), ref, nil)
	if err == nil {
		t.Fatal("expected error when cancel fails")
	}
	if !strings.Contains(err.Error(), "locked") {
		t.Errorf("error should mention lock, got: %v", err)
	}
}
