package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"reflect"
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
	err := completePark(ctx, s, d, spec, "vol-old", false, func() error { destroyCalled = true; return errors.New("destroy interrupted") }, func() error { t.Fatal("verified after failed destroy"); return nil })
	if err == nil || !destroyCalled {
		t.Fatalf("destroy error = %v", err)
	}
	failed, _ := s.Get(ctx, d.DesktopID)
	if failed.State != store.StateFailed || failed.FailurePhase != "park" || failed.InstanceID != "i-old" || failed.ParkRootVolumeID != "vol-old" || failed.RestoreSpec == nil {
		t.Fatalf("failed park record: %+v", failed)
	}
	if err := completePark(ctx, s, failed, spec, "vol-old", false, func() error { return nil }, func() error { return errors.New("volume still exists") }); err == nil {
		t.Fatal("park accepted unconfirmed deletion")
	}
	failed, _ = s.Get(ctx, d.DesktopID)
	if err := completePark(ctx, s, failed, spec, "vol-old", true, func() error { return nil }, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	parked, _ := s.Get(ctx, d.DesktopID)
	if parked.State != store.StateParked || parked.InstanceID != "" || parked.NoVNCURL != "" || parked.ParkedAt == "" {
		t.Fatalf("parked record: %+v", parked)
	}
	if !parked.RecreateAVDsOnUnpark {
		t.Fatal("AVD recreation consent was not retained for unpark")
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

func TestUnparkRetryCannotOverlapOrFailNewOwner(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d, _ := parkedFixture()
	d.State = store.StateParked
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	err := completeUnpark(ctx, s, d, func() (map[string]string, error) {
		active, err := s.Get(ctx, d.DesktopID)
		if err != nil {
			t.Fatal(err)
		}
		if err := completeUnpark(ctx, s, active, func() (map[string]string, error) {
			t.Fatal("overlapping restore reached Pulumi")
			return nil, nil
		}, nil); err == nil {
			t.Fatalf("overlapping retry: %v", err)
		}
		active, err = s.Get(ctx, d.DesktopID)
		if err != nil {
			t.Fatal(err)
		}
		active.UnparkOperationToken = "new-owner"
		if err := s.Update(ctx, active); err != nil {
			t.Fatal(err)
		}
		return nil, errors.New("old worker failed")
	}, nil)
	if err == nil {
		t.Fatal("old worker error lost")
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != store.StateRestoring || current.UnparkOperationToken != "new-owner" {
		t.Fatalf("stale worker changed new owner: %+v", current)
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
	boot := &provision.BootstrapConfig{DesktopID: d.DesktopID, GitHubOwner: "orchael", Hostname: "desktop.example.test", WorkspaceMode: "efs", WorkspaceName: d.WorkspaceName, EFSFileSystemID: "fs-1", EFSAccessPointID: "fsap-1", AgentSecretPath: "/secrets/agents", BridgeEnrollmentSecretPath: "/secrets/retired-bridge", DesktopSecretPaths: []string{"/secrets/removed"}, AWSRegion: "us-east-1"}
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
	replacement := "/secrets/new-bridgectl"
	overridden, err := restoreStackConfigWithBridgeSecret(d, &replacement)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = base64.StdEncoding.DecodeString(overridden["userDataBase64"])
	if err != nil {
		t.Fatal(err)
	}
	reader, err = gzip.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	userData = string(decoded)
	if !strings.Contains(userData, base64.StdEncoding.EncodeToString([]byte(replacement))) || strings.Contains(userData, base64.StdEncoding.EncodeToString([]byte("/secrets/retired-bridge"))) {
		t.Fatal("restore did not replace retired Bridge enrollment secret")
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

func TestFailedUnparkRetryReplacesInstanceAndRerunsBootstrap(t *testing.T) {
	d, _ := parkedFixture()
	d.RestoreSpec = &store.RestoreSpec{Version: 1, BootstrapJSON: `{"DesktopID":"d-0123abcd","WorkspaceMode":"efs","WorkspaceName":"test","EFSAccessPointID":"fsap-1"}`, Config: map[string]string{"workspaceEFSAccessPointId": "fsap-1"}}
	// Use the fixture's actual workspace name so restore validation stays realistic.
	var boot provision.BootstrapConfig
	if err := json.Unmarshal([]byte(d.RestoreSpec.BootstrapJSON), &boot); err != nil {
		t.Fatal(err)
	}
	boot.DesktopID, boot.WorkspaceName, boot.EFSAccessPointID = d.DesktopID, d.WorkspaceName, "fsap-1"
	raw, err := json.Marshal(&boot)
	if err != nil {
		t.Fatal(err)
	}
	d.RestoreSpec.BootstrapJSON = string(raw)
	d.InstanceID, d.State, d.FailurePhase = "i-failed", store.StateFailed, "unpark"
	if !shouldReplaceFailedUnparkInstance(d) {
		t.Fatal("failed unpark instance must be replaced")
	}
	d.InstanceID = ""
	if !shouldReplaceFailedUnparkInstance(d) {
		t.Fatal("failed unpark may have an unrecorded Pulumi instance")
	}
	first, err := restoreStackConfigForRetry(d, nil, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := restoreStackConfigForRetry(d, nil, "second")
	if err != nil {
		t.Fatal(err)
	}
	if first["replaceInstanceOnRestore"] != "true" || first["userDataBase64"] == second["userDataBase64"] {
		t.Fatal("retry did not force a user data replacement")
	}
	d.State = store.StateParked
	if shouldReplaceFailedUnparkInstance(d) {
		t.Fatal("parked restore should not replace an existing instance")
	}
}

func TestRestoreBridgeSecretPathsUsesSavedPathAndOverride(t *testing.T) {
	d, _ := parkedFixture()
	d.Secrets = []string{"/desktop-env", "/agents"}
	d.RestoreSpec = &store.RestoreSpec{Version: 1, BootstrapJSON: `{"BridgeEnrollmentSecretPath":"/saved-bridge","GitHubSecretPath":"/github","AgentSecretPath":"/agents"}`}
	path, other, err := restoreBridgeSecretPaths(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/saved-bridge" || !containsString(other, "/desktop-env") || !containsString(other, "/github") || !containsString(other, "/agents") {
		t.Fatalf("saved secret isolation: %q %v", path, other)
	}
	override := "/new-bridge"
	path, _, err = restoreBridgeSecretPaths(d, &override)
	if err != nil || path != override {
		t.Fatalf("override secret isolation: %q, %v", path, err)
	}
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d.State = store.StateRestoring
	d.UnparkOperationToken = "owner"
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := saveRestoredBridgeSecret(ctx, s, d, override, "arn:aws:secretsmanager:us-east-1:123456789012:secret:new-bridge-123456"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err = restoreBridgeSecretPaths(saved, nil)
	if err != nil || path != override {
		t.Fatalf("saved override used by later secret operations: %q, %v", path, err)
	}
	if saved.RestoreSpec.BridgeEnrollmentSecretARN == "" {
		t.Fatal("canonical Bridge identity was not saved")
	}
}

func TestFailedUnparkKeepsEffectiveBridgeOverride(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d, _ := parkedFixture()
	d.State = store.StateParked
	d.RestoreSpec = &store.RestoreSpec{Version: 1, BootstrapJSON: `{"BridgeEnrollmentSecretPath":"/old-bridge"}`}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	const arn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:new-bridge-123456"
	err := completeUnparkWithPrepare(ctx, s, d,
		func(claimed *store.Desktop) error {
			return saveRestoredBridgeSecret(ctx, s, claimed, "/new-bridge", arn)
		},
		func() (map[string]string, error) {
			current, err := s.Get(ctx, d.DesktopID)
			if err != nil {
				return nil, err
			}
			path, _, err := restoreBridgeSecretPaths(current, nil)
			if err != nil || path != "/new-bridge" || current.RestoreSpec.BridgeEnrollmentSecretARN != arn {
				t.Fatalf("override was not recorded before provisioning: %q, %v", path, err)
			}
			return map[string]string{"instanceId": "i-new"}, nil
		},
		func(*store.Desktop) error { return errors.New("bootstrap failed") })
	if err == nil {
		t.Fatal("failed readiness was accepted")
	}
	failed, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := restoreBridgeSecretPaths(failed, nil)
	if err != nil || failed.State != store.StateFailed || path != "/new-bridge" || failed.RestoreSpec.BridgeEnrollmentSecretARN != arn {
		t.Fatalf("failed restore lost effective Bridge identity: %q, %v", path, err)
	}
}

func TestFailedNestedUnparkClearsPrelaunchBeforeRetry(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateRestoring, InstanceID: "i-failed", UnparkOperationToken: "owner"}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	var order []string
	if err := resetFailedNestedUnpark(ctx, s, d, func() error {
		order = append(order, "destroy")
		return nil
	}, func(id string) error {
		order = append(order, "terminate:"+id)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"destroy", "terminate:i-failed"}) {
		t.Fatalf("reset order: %v", order)
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if current.InstanceID != "" || d.InstanceID != "" {
		t.Fatal("failed prelaunch ID was reused")
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
	err = completePark(ctx, s, original, &store.RestoreSpec{Version: 2, Config: map[string]string{"volumeSize": "100"}}, "vol-original", false, func() error {
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
