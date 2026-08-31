package cmd

import (
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestTerminateWarningLocalWorkspace(t *testing.T) {
	got := terminateWarning(&store.Desktop{WorkspaceMode: workspaceModeLocal})
	want := "WARNING: This will permanently destroy the EC2 instance and EBS volume."
	if got != want {
		t.Fatalf("terminateWarning = %q, want %q", got, want)
	}
}

func TestTerminateWarningEFSWorkspace(t *testing.T) {
	got := terminateWarning(&store.Desktop{
		WorkspaceMode: workspaceModeEFS,
		WorkspaceName: "orchael-factory-dev",
	})
	if !strings.Contains(got, `EFS workspace "orchael-factory-dev" will not be deleted`) {
		t.Fatalf("terminateWarning = %q", got)
	}
}
