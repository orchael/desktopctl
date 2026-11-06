package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/store"
)

func parkedFixture() (*store.Desktop, *store.Workspace) {
	d := &store.Desktop{DesktopID: "d-0123abcd", WorkspaceMode: workspaceModeEFS, WorkspaceName: "factory", WorkspaceID: "workspace:dev:factory", State: store.StateReady}
	w := &store.Workspace{WorkspaceName: "factory", State: store.WorkspaceStateAttached, AttachedDesktopID: d.DesktopID, EFSFileSystemID: "fs-1", EFSAccessPointID: "fsap-1"}
	return d, w
}

func TestParkedDesktopAppearsInListAndStatus(t *testing.T) {
	d, _ := parkedFixture()
	d.State = store.StateParked
	d.ParkedAt = "2026-11-06T12:00:00Z"
	d.InstanceID = ""
	listed := filterListedDesktops([]*store.Desktop{d}, false, "")
	if len(listed) != 1 || listed[0].State != store.StateParked {
		t.Fatalf("parked desktop hidden from list: %+v", listed)
	}
	var out bytes.Buffer
	printDesktopStatus(&out, d, "us-east-1", "", "")
	if !strings.Contains(out.String(), "State        : parked") || !strings.Contains(out.String(), d.ParkedAt) {
		t.Fatalf("status omitted parked state or time: %s", out.String())
	}
	encoded, err := json.Marshal(desktopStatusJSON(d, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"lifecycle_state":"parked"`) || !strings.Contains(string(encoded), `"parked_at":"2026-11-06T12:00:00Z"`) {
		t.Fatalf("JSON status: %s", encoded)
	}
}

func TestParkFailureRetryAndWorkspaceClaim(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d, w := parkedFixture()
	d.InstanceID = "i-old"
	d.NoVNCURL = "https://old"
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	w.Environment = "dev"
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	spec := &store.RestoreSpec{Version: 1, Config: map[string]string{"volumeSize": "150"}}
	destroyCalled := false
	err := completePark(ctx, s, d, spec, "vol-old", func() error { destroyCalled = true; return errors.New("destroy interrupted") }, func() error { t.Fatal("verified after failed destroy"); return nil })
	if err == nil || !destroyCalled {
		t.Fatalf("destroy error = %v", err)
	}
	failed, _ := s.Get(ctx, d.DesktopID)
	if failed.State != store.StateFailed || failed.FailurePhase != "park" || failed.InstanceID != "i-old" || failed.ParkRootVolumeID != "vol-old" || failed.RestoreSpec == nil {
		t.Fatalf("failed park record: %+v", failed)
	}
	if err := completePark(ctx, s, failed, spec, "vol-old", func() error { return nil }, func() error { return errors.New("volume still exists") }); err == nil {
		t.Fatal("park accepted unconfirmed deletion")
	}
	failed, _ = s.Get(ctx, d.DesktopID)
	if err := completePark(ctx, s, failed, spec, "vol-old", func() error { return nil }, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	parked, _ := s.Get(ctx, d.DesktopID)
	if parked.State != store.StateParked || parked.InstanceID != "" || parked.NoVNCURL != "" || parked.ParkedAt == "" {
		t.Fatalf("parked record: %+v", parked)
	}
	claimed, _ := s.GetWorkspace(ctx, "dev", "factory")
	if claimed.AttachedDesktopID != d.DesktopID {
		t.Fatal("workspace claim was lost")
	}
}

func TestUnparkFailureRetryAndReadiness(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d, w := parkedFixture()
	d.State = store.StateParked
	d.RestoreSpec = &store.RestoreSpec{Version: 1, Config: map[string]string{"amiId": "ami-old"}}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	w.Environment = "dev"
	if err := s.CreateWorkspace(ctx, w); err != nil {
		t.Fatal(err)
	}
	err := completeUnpark(ctx, s, d, func() (map[string]string, error) { return nil, errors.New("up interrupted") }, func(*store.Desktop) error { t.Fatal("readiness ran after failed up"); return nil })
	if err == nil {
		t.Fatal("restore failure ignored")
	}
	failed, _ := s.Get(ctx, d.DesktopID)
	if failed.State != store.StateFailed || failed.FailurePhase != "unpark" {
		t.Fatalf("failed restore: %+v", failed)
	}
	err = completeUnpark(ctx, s, failed, func() (map[string]string, error) { return map[string]string{"instanceId": "i-new"}, nil }, func(*store.Desktop) error { return errors.New("EFS not mounted") })
	if err == nil {
		t.Fatal("restore passed failed readiness")
	}
	failed, _ = s.Get(ctx, d.DesktopID)
	if failed.InstanceID != "i-new" || failed.State != store.StateFailed {
		t.Fatalf("partial restore not recoverable: %+v", failed)
	}
	if err := completeUnpark(ctx, s, failed, func() (map[string]string, error) { return map[string]string{"instanceId": "i-new"}, nil }, func(restored *store.Desktop) error {
		if restored.InstanceID != "i-new" {
			t.Fatal("wrong instance checked")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ready, _ := s.Get(ctx, d.DesktopID)
	if ready.State != store.StateReady || ready.DesktopID != d.DesktopID || ready.InstanceID != "i-new" {
		t.Fatalf("restored identity: %+v", ready)
	}
	claimed, _ := s.GetWorkspace(ctx, "dev", "factory")
	if claimed.AttachedDesktopID != d.DesktopID {
		t.Fatal("workspace claim lost")
	}
}

func TestParkEligibilityProtectsWorkspaceAndLocalState(t *testing.T) {
	d, w := parkedFixture()
	if err := parkEligible(d, w); err != nil {
		t.Fatalf("valid EFS desktop: %v", err)
	}
	tests := []struct {
		name   string
		change func(*store.Desktop, *store.Workspace)
		want   string
	}{
		{"local", func(d *store.Desktop, _ *store.Workspace) { d.WorkspaceMode = workspaceModeLocal }, "local workspace"},
		{"nested", func(d *store.Desktop, _ *store.Workspace) { d.NestedVirt = true }, "nested virtualization"},
		{"avd", func(d *store.Desktop, _ *store.Workspace) { d.AVDNames = []string{"phone"} }, "AVD"},
		{"missing", func(_ *store.Desktop, w *store.Workspace) { w.EFSAccessPointID = "" }, "missing"},
		{"foreign claim", func(_ *store.Desktop, w *store.Workspace) { w.AttachedDesktopID = "d-deadbeef" }, "not exclusively claimed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desktop, workspace := parkedFixture()
			tt.change(desktop, workspace)
			err := parkEligible(desktop, workspace)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRestoreSpecRequiresRecordedWorkspaceAndLaunchInputs(t *testing.T) {
	d, w := parkedFixture()
	spec := &store.RestoreSpec{Version: 1, BootstrapJSON: `{}`, Config: map[string]string{
		"desktopId": d.DesktopID, "amiId": "ami-1", "volumeSize": "150", "userDataBase64": "cloud-init", "instanceType": "m7i.large", "subnetId": "subnet-1", "securityGroupId": "sg-1", "instanceProfile": "profile", "workspaceMode": "efs", "workspaceName": "factory", "workspaceEFSFileSystemId": "fs-1", "workspaceEFSAccessPointId": "fsap-1",
	}}
	if err := validateRestoreSpec(d, spec, w); err != nil {
		t.Fatal(err)
	}
	legacy := &store.RestoreSpec{Version: 2, Config: spec.Config}
	if err := validateRestoreSpec(d, legacy, w); err != nil {
		t.Fatalf("migrated restore specification was rejected: %v", err)
	}
	spec.Config["volumeSize"] = ""
	if err := validateRestoreSpec(d, spec, w); err == nil {
		t.Fatal("missing root size was accepted")
	}
	spec.Config["volumeSize"] = "150"
	spec.Config["workspaceEFSAccessPointId"] = "fsap-other"
	if err := validateRestoreSpec(d, spec, w); err == nil {
		t.Fatal("different EFS access point was accepted")
	}
}

func TestRestoreRegeneratesCurrentSecretPaths(t *testing.T) {
	d, _ := parkedFixture()
	d.Secrets = []string{"/secrets/agents", "/secrets/added"}
	boot := &provision.BootstrapConfig{DesktopID: d.DesktopID, GitHubOwner: "orchael", Hostname: "desktop.example.test", WorkspaceMode: "efs", WorkspaceName: d.WorkspaceName, EFSFileSystemID: "fs-1", EFSAccessPointID: "fsap-1", AgentSecretPath: "/secrets/agents", DesktopSecretPaths: []string{"/secrets/removed"}, AWSRegion: "us-east-1"}
	raw, err := json.Marshal(boot)
	if err != nil {
		t.Fatal(err)
	}
	d.RestoreSpec = &store.RestoreSpec{Version: 1, BootstrapJSON: string(raw), Config: map[string]string{"workspaceEFSAccessPointId": "fsap-1", "userDataBase64": "old-user-data"}}
	config, err := restoreStackConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	if config["userDataBase64"] == "old-user-data" {
		t.Fatal("reused stale user data")
	}
	encoded, err := base64.StdEncoding.DecodeString(config["userDataBase64"])
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	userData := string(decoded)
	if strings.Contains(userData, "/secrets/removed") || !strings.Contains(userData, "/secrets/added") || !strings.Contains(userData, "/secrets/agents") {
		t.Fatalf("secret sources not regenerated from fleet list")
	}
	d.Secrets = []string{"/secrets/added"}
	config, err = restoreStackConfig(d)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = base64.StdEncoding.DecodeString(config["userDataBase64"])
	if err != nil {
		t.Fatal(err)
	}
	reader, err = gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoded, err = io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(decoded), "/secrets/agents") {
		t.Fatal("removed agent secret was restored")
	}
}

func TestParkRetryFencesPreviousWorker(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateReady, InstanceID: "i-original"}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	original, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	awaited := false
	err = completePark(ctx, s, original, &store.RestoreSpec{Version: 2, Config: map[string]string{"volumeSize": "100"}}, "vol-original", func() error {
		claimed, err := s.Get(ctx, d.DesktopID)
		if err != nil {
			return err
		}
		claimed.ParkOperationToken = "new-worker-token"
		return s.Update(ctx, claimed)
	}, func() error { awaited = true; return nil })
	if !errors.Is(err, store.ErrStateConflict) {
		t.Fatalf("stale worker was not fenced: %v", err)
	}
	if awaited {
		t.Fatal("stale worker continued to final verification")
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != store.StateParking || current.InstanceID != "i-original" {
		t.Fatal("stale worker modified fleet lifecycle")
	}
	if err := s.RecordParkFailure(ctx, d.DesktopID, "old-worker-token", "stale failure"); !errors.Is(err, store.ErrStateConflict) {
		t.Fatalf("stale failure was not fenced: %v", err)
	}
}
