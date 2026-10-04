package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	efssdk "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/orchael/desktopctl/internal/store"
)

type fakeEFSWorkspaceClient struct {
	createInput *efssdk.CreateAccessPointInput
	deleteInput *efssdk.DeleteAccessPointInput
	createErr   error
	deleteErr   error
}

func (f *fakeEFSWorkspaceClient) CreateAccessPoint(ctx context.Context, in *efssdk.CreateAccessPointInput, optFns ...func(*efssdk.Options)) (*efssdk.CreateAccessPointOutput, error) {
	f.createInput = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	id := "fsap-123"
	return &efssdk.CreateAccessPointOutput{AccessPointId: &id}, nil
}

func (f *fakeEFSWorkspaceClient) DeleteAccessPoint(ctx context.Context, in *efssdk.DeleteAccessPointInput, optFns ...func(*efssdk.Options)) (*efssdk.DeleteAccessPointOutput, error) {
	f.deleteInput = in
	return &efssdk.DeleteAccessPointOutput{}, f.deleteErr
}

func TestValidateWorkspaceName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "orchael-factory-dev"},
		{name: "dev_01.workspace"},
		{name: "", wantErr: true},
		{name: "-starts-bad", wantErr: true},
		{name: "bad/name", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkspaceName(tt.name)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCreateWorkspaceAccessPoint(t *testing.T) {
	client := &fakeEFSWorkspaceClient{}
	w := &store.Workspace{
		WorkspaceName:   "factory-dev",
		Environment:     "dev",
		GitHubOwner:     "acme",
		RepoFingerprint: "repo-fingerprint",
		EFSFileSystemID: "fs-123",
	}
	id, err := createWorkspaceAccessPoint(context.Background(), client, w)
	if err != nil {
		t.Fatalf("createWorkspaceAccessPoint: %v", err)
	}
	if id != "fsap-123" {
		t.Fatalf("id = %q, want fsap-123", id)
	}
	if client.createInput == nil {
		t.Fatal("CreateAccessPoint not called")
	}
	if *client.createInput.FileSystemId != "fs-123" {
		t.Fatalf("FileSystemId = %q", *client.createInput.FileSystemId)
	}
	if got := *client.createInput.RootDirectory.Path; got != "/workspaces/factory-dev" {
		t.Fatalf("root path = %q", got)
	}
	if got := *client.createInput.RootDirectory.CreationInfo.Permissions; got != "0755" {
		t.Fatalf("permissions = %q", got)
	}
	if *client.createInput.PosixUser.Uid != ubuntuUID || *client.createInput.PosixUser.Gid != ubuntuGID {
		t.Fatalf("posix user = %+v", client.createInput.PosixUser)
	}
}

func TestCreateWorkspaceAccessPointError(t *testing.T) {
	client := &fakeEFSWorkspaceClient{createErr: errors.New("boom")}
	_, err := createWorkspaceAccessPoint(context.Background(), client, &store.Workspace{EFSFileSystemID: "fs-123"})
	if err == nil || !strings.Contains(err.Error(), "create EFS access point") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeleteWorkspaceAccessPointIgnoresNotFound(t *testing.T) {
	client := &fakeEFSWorkspaceClient{deleteErr: &efstypes.AccessPointNotFound{}}
	if err := deleteWorkspaceAccessPoint(context.Background(), client, "fsap-missing"); err != nil {
		t.Fatalf("deleteWorkspaceAccessPoint: %v", err)
	}
}

func TestWorkspaceRepoMutationAddsReposWhenDetached(t *testing.T) {
	w := &store.Workspace{
		WorkspaceName: "factory-dev",
		Environment:   "dev",
		GitHubOwner:   "acme",
		Repos:         []string{"github.com/acme/app"},
		State:         store.WorkspaceStateAvailable,
	}
	got, err := workspaceRepoMutation(w, []string{"acme/api", "github.com/acme/app"}, false)
	if err != nil {
		t.Fatalf("workspaceRepoMutation add: %v", err)
	}
	want := []string{"github.com/acme/api", "github.com/acme/app"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("repos = %v, want %v", got, want)
	}
}

func TestWorkspaceRepoMutationRemovesReposWhenDetached(t *testing.T) {
	w := &store.Workspace{
		WorkspaceName: "factory-dev",
		Environment:   "dev",
		GitHubOwner:   "acme",
		Repos:         []string{"github.com/acme/api", "github.com/acme/app"},
		State:         store.WorkspaceStateAvailable,
	}
	got, err := workspaceRepoMutation(w, []string{"acme/api"}, true)
	if err != nil {
		t.Fatalf("workspaceRepoMutation remove: %v", err)
	}
	want := []string{"github.com/acme/app"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("repos = %v, want %v", got, want)
	}
}

func TestWorkspaceRepoMutationRequiresDetachedWorkspace(t *testing.T) {
	w := &store.Workspace{
		WorkspaceName:     "factory-dev",
		Environment:       "dev",
		GitHubOwner:       "acme",
		Repos:             []string{"github.com/acme/app"},
		State:             store.WorkspaceStateAttached,
		AttachedDesktopID: "d-001",
	}
	_, err := workspaceRepoMutation(w, []string{"acme/api"}, false)
	if err == nil || !strings.Contains(err.Error(), "detach or terminate") {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkspaceRepoMutationRejectsDifferentOwner(t *testing.T) {
	w := &store.Workspace{
		WorkspaceName: "factory-dev",
		Environment:   "dev",
		GitHubOwner:   "acme",
		Repos:         []string{"github.com/acme/app"},
		State:         store.WorkspaceStateAvailable,
	}
	_, err := workspaceRepoMutation(w, []string{"other/api"}, false)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkspaceRepoMutationRemoveMissingRepo(t *testing.T) {
	w := &store.Workspace{
		WorkspaceName: "factory-dev",
		Environment:   "dev",
		GitHubOwner:   "acme",
		Repos:         []string{"github.com/acme/app"},
		State:         store.WorkspaceStateAvailable,
	}
	_, err := workspaceRepoMutation(w, []string{"acme/api"}, true)
	if err == nil || !strings.Contains(err.Error(), "does not contain") {
		t.Fatalf("error = %v", err)
	}
}
