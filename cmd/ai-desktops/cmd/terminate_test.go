package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

type trackingDestroyRunner struct{ called bool }

func (r *trackingDestroyRunner) Destroy(context.Context, *pulumi.StackRef, io.Writer) error {
	r.called = true
	return nil
}

func TestDestroyDesktopResourcesRecoversUnimportedInstance(t *testing.T) {
	runner := &trackingDestroyRunner{}
	d := &store.Desktop{DesktopID: "d-0123abcd", NestedVirt: true, InstanceID: "i-123", Region: "us-east-2"}
	called := false
	err := destroyDesktopResources(context.Background(), runner, &pulumi.StackRef{}, d, "customer", func(context.Context, string, string, *store.Desktop) ([]string, error) {
		return nil, nil
	}, func(_ context.Context, region, profile, id string) error {
		called = true
		if !runner.called || region != "us-east-2" || profile != "customer" || id != "i-123" {
			t.Fatalf("termination called with unexpected state: region=%q profile=%q id=%q", region, profile, id)
		}
		return errors.New("instance still running")
	})
	if !called || err == nil {
		t.Fatalf("cleanup called = %v, error = %v", called, err)
	}
}

func TestDestroyDesktopResourcesReconcilesUnknownLaunch(t *testing.T) {
	runner := &trackingDestroyRunner{}
	d := &store.Desktop{DesktopID: "d-0123abcd", Environment: "dev", NestedVirt: true, Region: "us-east-2"}
	called := false
	err := destroyDesktopResources(context.Background(), runner, &pulumi.StackRef{}, d, "customer", func(_ context.Context, region, profile string, desktop *store.Desktop) ([]string, error) {
		if !runner.called || region != "us-east-2" || profile != "customer" || desktop != d {
			t.Fatal("tag reconciliation did not follow stack destroy with correct scope")
		}
		return []string{"i-orphan"}, nil
	}, func(_ context.Context, _, _, id string) error {
		called = true
		if id != "i-orphan" {
			t.Fatalf("terminated %q", id)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("reconcile error = %v, terminated = %v", err, called)
	}
}

func TestDestroyDesktopResourcesRetainsUnknownLaunchWithoutMatch(t *testing.T) {
	d := &store.Desktop{DesktopID: "d-0123abcd", Environment: "dev", NestedVirt: true, Region: "us-east-2"}
	err := destroyDesktopResources(context.Background(), &trackingDestroyRunner{}, &pulumi.StackRef{}, d, "customer", func(context.Context, string, string, *store.Desktop) ([]string, error) {
		return nil, nil
	}, func(context.Context, string, string, string) error {
		t.Fatal("should not terminate an unknown instance")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "cannot confirm") {
		t.Fatalf("expected unreconciled launch error, got %v", err)
	}
}

func TestTerminateWarningLocalWorkspace(t *testing.T) {
	got := terminateWarning(&store.Desktop{WorkspaceMode: workspaceModeLocal})
	want := "WARNING: This will permanently destroy the EC2 instance and EBS volume."
	if got != want {
		t.Fatalf("terminateWarning = %q, want %q", got, want)
	}
}

func TestTerminateRejectsInvalidIDBeforeStackRecovery(t *testing.T) {
	if err := runTerminate(nil, []string{"foundation-dev"}); err == nil || !strings.Contains(err.Error(), "desktop ID") {
		t.Fatalf("terminate invalid ID error = %v, want desktop ID validation", err)
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
