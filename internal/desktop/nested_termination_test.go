package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/orchael/desktopctl/internal/store"
)

func TestReconcileNestedInstancesIncludesPrelaunchAndRecordedInstance(t *testing.T) {
	d := &store.Desktop{DesktopID: "d-one", Environment: "dev", Region: "us-east-2", NestedVirt: true, InstanceID: "i-recorded"}
	var terminated []string
	err := ReconcileNestedInstances(context.Background(), d, "", func(_ context.Context, _, _ string, _ *store.Desktop) ([]string, error) {
		return []string{"i-prelaunched", "i-recorded"}, nil
	}, func(_ context.Context, _, _, id string) error { terminated = append(terminated, id); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(terminated) != 2 || terminated[0] != "i-prelaunched" || terminated[1] != "i-recorded" {
		t.Fatalf("terminated: %v", terminated)
	}
}

func TestReconcileNestedInstancesBlocksUnknownOrLivePrelaunch(t *testing.T) {
	d := &store.Desktop{DesktopID: "d-one", NestedVirt: true}
	list := func(_ context.Context, _, _ string, _ *store.Desktop) ([]string, error) { return nil, nil }
	terminate := func(_ context.Context, _, _, _ string) error { return nil }
	if err := ReconcileNestedInstances(context.Background(), d, "", list, terminate); err == nil {
		t.Fatal("unknown launch outcome accepted")
	}
	list = func(_ context.Context, _, _ string, _ *store.Desktop) ([]string, error) {
		return []string{"i-prelaunched"}, nil
	}
	terminate = func(_ context.Context, _, _, _ string) error { return errors.New("still running") }
	if err := ReconcileNestedInstances(context.Background(), d, "", list, terminate); err == nil {
		t.Fatal("live prelaunch accepted")
	}
}
