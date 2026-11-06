package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

func TestLegacyRestoreReplacesChangedSecretSections(t *testing.T) {
	boot := &provision.BootstrapConfig{
		DesktopID: "d-0123abcd", AWSRegion: "us-east-2", AgentSecretPath: "/agents/old",
		DesktopSecretPaths: []string{"/desktop/removed", "/desktop/profile"}, ProfileSecretPath: "/desktop/profile",
	}
	original, err := provision.RenderCloudInit(boot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := gzipBase64UserData(original)
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Desktop{DesktopID: "d-0123abcd", Region: "us-east-2", Secrets: []string{"/desktop/profile", "/desktop/added"}, RestoreSpec: &store.RestoreSpec{Version: 2, Config: map[string]string{"userDataBase64": encoded, "amiId": "ami-original"}}}
	result, err := legacyRestoreConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := decodedUserData(result["userDataBase64"])
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"/agents/old", "/desktop/removed"} {
		if strings.Contains(cloud, old) {
			t.Errorf("removed secret path %q was replayed", old)
		}
	}
	for _, current := range []string{"/desktop/profile", "/desktop/added"} {
		if !strings.Contains(cloud, current) {
			t.Errorf("current secret path %q was omitted", current)
		}
	}
	if strings.Contains(cloud, legacyAgentMarker) {
		t.Error("removed agent section remained")
	}
	if result["amiId"] != "ami-original" || d.RestoreSpec.Config["userDataBase64"] != encoded {
		t.Error("original stack config was mutated")
	}
}

func TestLegacyRestoreRejectsUnknownCloudInit(t *testing.T) {
	encoded, err := gzipBase64UserData("#cloud-config\nruncmd:\n  - echo unknown\n")
	if err != nil {
		t.Fatal(err)
	}
	d := &store.Desktop{DesktopID: "d-0123abcd", RestoreSpec: &store.RestoreSpec{Version: 2, Config: map[string]string{"userDataBase64": encoded}}}
	if _, err := legacyRestoreConfig(d); err == nil {
		t.Fatal("unknown cloud-init was accepted")
	}
}

func TestLegacyCandidateRequiresMatchingFleetAndWorkspace(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateStopped, InstanceType: "m7i.large", AMIID: "ami-one", Region: "us-east-2", Environment: "dev", GitHubOwner: "orchael", WorkspaceMode: "efs", WorkspaceName: "work"}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	w := &store.Workspace{Environment: "dev", WorkspaceName: "work", EFSFileSystemID: "fs-one", EFSAccessPointID: "fsap-one", AttachedDesktopID: d.DesktopID, State: store.WorkspaceStateAttached}
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	config := pulumi.StackConfig{"desktopId": d.DesktopID, "instanceType": d.InstanceType, "amiId": d.AMIID, "environment": d.Environment, "githubOwner": d.GitHubOwner, "aws:region": d.Region, "workspaceMode": "efs", "workspaceName": "work", "workspaceEFSFileSystemId": "fs-one", "workspaceEFSAccessPointId": "fsap-one", "volumeSize": "100", "userDataBase64": "encoded", "subnetId": "subnet-one", "securityGroupId": "sg-one", "instanceProfile": "profile-one"}
	if err := validateLegacyRestoreCandidate(ctx, s, d, config); err != nil {
		t.Fatal(err)
	}
	config["workspaceEFSAccessPointId"] = "fsap-other"
	if err := validateLegacyRestoreCandidate(ctx, s, d, config); err == nil {
		t.Fatal("wrong access point was accepted")
	}
}
