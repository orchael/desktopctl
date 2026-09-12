package packer

import (
	"os"
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
		`stat -c '%U:%G:%a' "$CODEX_HOME_DIR"`,
		`"ubuntu:ubuntu:700"`,
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("AMI doctor missing private Codex home check %q", want)
		}
	}
}
