package packer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAMIDoctorChecksPrivateCodexHome(t *testing.T) {
	doctor, err := os.ReadFile("files/ai-desktops-doctor")
	if err != nil {
		t.Fatalf("read AMI doctor: %v", err)
	}
	contents := string(doctor)
	for _, want := range []string{
		`CODEX_HOME_DIR="${CODEX_HOME_DIR:-/home/ubuntu/.codex}"`,
		`[ ! -L "$CODEX_HOME_DIR" ]`,
		`[ -d "$CODEX_HOME_DIR" ]`,
		`stat -c '%U:%G:%a' "$CODEX_HOME_DIR"`,
		`"ubuntu:ubuntu:700"`,
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("AMI doctor missing private Codex home check %q", want)
		}
	}
}

func TestAMIDoctorReportsCodexUpdateSetting(t *testing.T) {
	for _, tc := range []struct {
		name, contents, want string
	}{
		{"disabled", "model = \"gpt-5\"\ncheck_for_update_on_startup = false\n", "[OK]   Codex update prompt: disabled"},
		{"enabled", "check_for_update_on_startup = true\n", "[FAIL] Codex update prompt: set check_for_update_on_startup = false"},
		{"missing", "model = \"gpt-5\"\n", "[FAIL] Codex update prompt: set check_for_update_on_startup = false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "files/ai-desktops-doctor")
			cmd.Env = append(os.Environ(), "CODEX_BRIDGE_HOME_DIR="+dir, "CONFIG_FILE="+filepath.Join(dir, "missing.yaml"))
			output, _ := cmd.CombinedOutput() // Unrelated host checks can fail outside an AMI.
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("doctor output missing %q:\n%s", tc.want, output)
			}
		})
	}
}

func TestAMIDoctorChecksCodexUpdateSetting(t *testing.T) {
	doctor, err := os.ReadFile("files/ai-desktops-doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CODEX_BRIDGE_HOME_DIR",
		"check_for_update_on_startup",
		"Codex update prompt",
	} {
		if !strings.Contains(string(doctor), want) {
			t.Fatalf("doctor missing %q", want)
		}
	}
}
