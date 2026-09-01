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

func TestWorkspaceDetachRecoveryCommand(t *testing.T) {
	tests := []struct {
		name          string
		env           string
		workspaceName string
		want          string
	}{
		{
			name:          "default env",
			workspaceName: "factory-dev",
			want:          "ai-desktops workspace detach factory-dev --force",
		},
		{
			name:          "explicit env",
			env:           "prod",
			workspaceName: "factory-dev",
			want:          "ai-desktops workspace detach factory-dev --env prod --force",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workspaceDetachRecoveryCommand(tt.env, tt.workspaceName)
			if got != tt.want {
				t.Fatalf("workspaceDetachRecoveryCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}
