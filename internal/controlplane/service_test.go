package controlplane

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	appconfig "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
)

// mockPulumiRunner is a controllable PulumiRunner double for unit tests.
type mockPulumiRunner struct {
	upFn      func(ref *pulumi.StackRef, cfg pulumi.StackConfig) (map[string]string, error)
	destroyFn func(ref *pulumi.StackRef) error
	outputsFn func(ref *pulumi.StackRef) (map[string]string, error)
}

func (m *mockPulumiRunner) Up(_ context.Context, ref *pulumi.StackRef, cfg pulumi.StackConfig, _ io.Writer) (map[string]string, error) {
	if m.upFn != nil {
		return m.upFn(ref, cfg)
	}
	return map[string]string{
		"instanceId": "i-mock001",
		"hostname":   ref.StackName + ".desktops.orchael.dev",
		"novncUrl":   "https://" + ref.StackName + ".desktops.orchael.dev:8443/novnc/vnc.html",
		"sshTarget":  "ubuntu@" + ref.StackName + ".desktops.orchael.dev",
	}, nil
}

func (m *mockPulumiRunner) Destroy(_ context.Context, ref *pulumi.StackRef, _ io.Writer) error {
	if m.destroyFn != nil {
		return m.destroyFn(ref)
	}
	return nil
}

func (m *mockPulumiRunner) Outputs(_ context.Context, ref *pulumi.StackRef) (map[string]string, error) {
	if m.outputsFn != nil {
		return m.outputsFn(ref)
	}
	return map[string]string{
		"subnetId":        "subnet-mock001",
		"securityGroupId": "sg-mock001",
		"instanceProfile": "mock-profile",
		"zoneId":          "ZMOCK001",
	}, nil
}

func newServiceWithRunner(cfg *appconfig.Config, s store.Store, runner PulumiRunner) *Service {
	return NewService(cfg, s, runner, false, true, time.Second, time.Minute)
}

func lifecycleTestConfig() *appconfig.Config {
	cfg := &appconfig.Config{}
	cfg.Defaults()
	cfg.Pulumi.BackendBucket = "test-bucket"
	cfg.Pulumi.InfraDir = "/tmp/infra"
	cfg.Desktop.OperatorCIDR = "127.0.0.1/32"
	return cfg
}

// seedDesktop creates a desktop record in the given store and returns its ID.
func seedDesktop(t *testing.T, s store.Store, orgID string, state store.LifecycleState) string {
	t.Helper()
	id := "d-test001"
	mgr := desktop.NewManager(s)
	if err := mgr.CreateRecord(context.Background(), id, &desktop.CreateRequest{
		OrganizationID: orgID,
		GitHubOwner:    "orchael",
		Zone:           "desktops.orchael.dev",
		BackendBucket:  "test-bucket",
		Region:         "us-east-1",
		Environment:    "dev",
	}); err != nil {
		t.Fatalf("seedDesktop CreateRecord: %v", err)
	}
	if state != store.StateCreating {
		d, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("seedDesktop Get: %v", err)
		}
		d.State = state
		d.InstanceID = "i-seeded001"
		if err := s.Update(context.Background(), d); err != nil {
			t.Fatalf("seedDesktop Update: %v", err)
		}
	}
	return id
}

func TestService_TerminateDesktop_NoPulumiRunner(t *testing.T) {
	t.Parallel()
	svc := newTestService(lifecycleTestConfig(), store.NewInMemoryStore())
	_, err := svc.TerminateDesktop(context.Background(), testOrganizationID, "d-001")
	if err == nil {
		t.Fatal("expected error when Pulumi runner is not configured")
	}
}

func TestService_TerminateDesktop_NotFound(t *testing.T) {
	t.Parallel()
	svc := newServiceWithRunner(lifecycleTestConfig(), store.NewInMemoryStore(), &mockPulumiRunner{})
	_, err := svc.TerminateDesktop(context.Background(), testOrganizationID, "d-missing")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestService_TerminateDesktop_WrongOrg(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	id := seedDesktop(t, s, testOrganizationID, store.StateReady)
	svc := newServiceWithRunner(lifecycleTestConfig(), s, &mockPulumiRunner{})
	_, err := svc.TerminateDesktop(context.Background(), "other-org", id)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong org: got %v, want ErrNotFound", err)
	}
}

func TestService_TerminateDesktop_AlreadyTerminated(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	id := seedDesktop(t, s, testOrganizationID, store.StateTerminated)
	svc := newServiceWithRunner(lifecycleTestConfig(), s, &mockPulumiRunner{})
	_, err := svc.TerminateDesktop(context.Background(), testOrganizationID, id)
	if err == nil {
		t.Fatal("expected error terminating already-terminated desktop")
	}
}

func TestService_TerminateDesktop_Success(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	id := seedDesktop(t, s, testOrganizationID, store.StateReady)
	destroyCalled := false
	runner := &mockPulumiRunner{
		destroyFn: func(_ *pulumi.StackRef) error {
			destroyCalled = true
			return nil
		},
	}
	svc := newServiceWithRunner(lifecycleTestConfig(), s, runner)
	result, err := svc.TerminateDesktop(context.Background(), testOrganizationID, id)
	if err != nil {
		t.Fatalf("TerminateDesktop: %v", err)
	}
	if !destroyCalled {
		t.Error("Pulumi Destroy should have been called")
	}
	if result.Action != "terminate" {
		t.Errorf("result.Action = %q, want %q", result.Action, "terminate")
	}
	if result.State != store.StateTerminated {
		t.Errorf("result.State = %q, want TERMINATED", result.State)
	}
	// Verify the store record is terminated.
	d, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get after terminate: %v", err)
	}
	if d.State != store.StateTerminated {
		t.Errorf("store state = %q, want TERMINATED", d.State)
	}
}

func TestService_TerminateDesktop_DestroyFails(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	id := seedDesktop(t, s, testOrganizationID, store.StateReady)
	runner := &mockPulumiRunner{
		destroyFn: func(_ *pulumi.StackRef) error {
			return errors.New("pulumi destroy failed")
		},
	}
	svc := newServiceWithRunner(lifecycleTestConfig(), s, runner)
	_, err := svc.TerminateDesktop(context.Background(), testOrganizationID, id)
	if err == nil {
		t.Fatal("expected error when Destroy fails")
	}
	// Record must be marked failed, not terminated.
	d, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get after failed terminate: %v", err)
	}
	if d.State != store.StateFailed && d.State != store.StateProvisioningFailed {
		t.Errorf("state after destroy failure = %q, want FAILED", d.State)
	}
}

func TestService_CreateDesktop_NoPulumiRunner(t *testing.T) {
	t.Parallel()
	svc := newTestService(lifecycleTestConfig(), store.NewInMemoryStore())
	_, err := svc.CreateDesktop(context.Background(), testOrganizationID, &CreateDesktopRequest{
		Owner: "orchael",
	})
	if err == nil {
		t.Fatal("expected error when Pulumi runner is not configured")
	}
}

func TestService_CreateDesktop_RequiresOwner(t *testing.T) {
	t.Parallel()
	svc := newServiceWithRunner(lifecycleTestConfig(), store.NewInMemoryStore(), &mockPulumiRunner{})
	_, err := svc.CreateDesktop(context.Background(), testOrganizationID, &CreateDesktopRequest{})
	if err == nil {
		t.Fatal("expected error for missing owner")
	}
}

func TestService_CreateDesktop_FoundationOutputsMissing(t *testing.T) {
	t.Parallel()
	runner := &mockPulumiRunner{
		outputsFn: func(_ *pulumi.StackRef) (map[string]string, error) {
			return nil, errors.New("foundation stack not found")
		},
	}
	svc := newServiceWithRunner(lifecycleTestConfig(), store.NewInMemoryStore(), runner)
	_, err := svc.CreateDesktop(context.Background(), testOrganizationID, &CreateDesktopRequest{
		Owner: "orchael",
	})
	if err == nil {
		t.Fatal("expected error when foundation outputs unavailable")
	}
}

func TestService_CreateDesktop_Success(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	upCalled := false
	runner := &mockPulumiRunner{
		upFn: func(ref *pulumi.StackRef, _ pulumi.StackConfig) (map[string]string, error) {
			upCalled = true
			return map[string]string{
				"instanceId": "i-new001",
				"hostname":   ref.StackName + ".desktops.orchael.dev",
				"novncUrl":   "https://" + ref.StackName + ".desktops.orchael.dev:8443/novnc/vnc.html",
				"sshTarget":  "ubuntu@" + ref.StackName + ".desktops.orchael.dev",
			}, nil
		},
	}
	svc := newServiceWithRunner(lifecycleTestConfig(), s, runner)
	result, err := svc.CreateDesktop(context.Background(), testOrganizationID, &CreateDesktopRequest{
		Owner:        "orchael",
		Repos:        []string{"orchael/ai-desktops"},
		InstanceType: "t3.large",
	})
	if err != nil {
		t.Fatalf("CreateDesktop: %v", err)
	}
	if !upCalled {
		t.Error("Pulumi Up should have been called")
	}
	if result.Action != "create" {
		t.Errorf("result.Action = %q, want %q", result.Action, "create")
	}
	if result.Desktop == nil {
		t.Fatal("result.Desktop must not be nil")
	}
	if result.Desktop.InstanceID != "i-new001" {
		t.Errorf("InstanceID = %q, want %q", result.Desktop.InstanceID, "i-new001")
	}
	if result.Desktop.OrganizationID != testOrganizationID {
		t.Errorf("OrganizationID = %q, want %q", result.Desktop.OrganizationID, testOrganizationID)
	}
}

func TestService_CreateDesktop_PulumiUpFails(t *testing.T) {
	t.Parallel()
	s := store.NewInMemoryStore()
	runner := &mockPulumiRunner{
		upFn: func(_ *pulumi.StackRef, _ pulumi.StackConfig) (map[string]string, error) {
			return nil, errors.New("pulumi up failed: capacity error")
		},
	}
	svc := newServiceWithRunner(lifecycleTestConfig(), s, runner)
	_, err := svc.CreateDesktop(context.Background(), testOrganizationID, &CreateDesktopRequest{
		Owner: "orchael",
	})
	if err == nil {
		t.Fatal("expected error when pulumi up fails")
	}
}
