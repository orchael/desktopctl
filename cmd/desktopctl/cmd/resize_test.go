package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
)

func TestResizeCmd_flagsRegistered(t *testing.T) {
	if flag := resizeCmd.Flags().Lookup("instance-type"); flag == nil {
		t.Fatal("instance-type flag not registered")
	}
	if flag := resizeCmd.Flags().Lookup("start"); flag == nil {
		t.Fatal("start flag not registered")
	}
	if flag := resizeCmd.Flags().Lookup("no-start"); flag == nil {
		t.Fatal("no-start flag not registered")
	}
}

func TestFailedResizeRestartRecoveryRequiresMatchingInstance(t *testing.T) {
	d := &store.Desktop{InstanceType: "m8i.xlarge", RestoreSpec: &store.RestoreSpec{Config: map[string]string{"instanceType": "m8i.xlarge"}}}
	status := &awsx.EC2InstanceStatus{State: "stopped", InstanceType: "m8i.xlarge"}
	if err := validateResizeRecovery(d, status); err != nil {
		t.Fatal(err)
	}
	status.InstanceType = "m7i.xlarge"
	if err := validateResizeRecovery(d, status); err == nil {
		t.Fatal("accepted mismatched EC2 instance")
	}
	d.ResizeTargetType = "m7i.xlarge"
	if err := validateResizeRecovery(d, status); err != nil {
		t.Fatalf("modified EC2 type should be recoverable: %v", err)
	}
	status.InstanceType = d.InstanceType
	d.ResizeTargetType = ""
	d.RestoreSpec.Config["instanceType"] = "m7i.xlarge"
	if err := validateResizeRecovery(d, status); err == nil {
		t.Fatal("accepted mismatched restore configuration")
	}
}

func TestResizeTargetSurvivesMetadataWriteFailureAfterEC2Change(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateResizing, InstanceType: "m7i.xlarge", ResizeTargetType: "m8i.xlarge", RestoreSpec: &store.RestoreSpec{Config: map[string]string{"instanceType": "m7i.xlarge"}}}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	stale, _ := s.Get(ctx, d.DesktopID)
	fresh, _ := s.Get(ctx, d.DesktopID)
	if err := s.Update(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	stale.InstanceType = "m8i.xlarge"
	if err := s.Update(ctx, stale); !errors.Is(err, store.ErrStateConflict) {
		t.Fatalf("expected metadata update failure: %v", err)
	}
	current, _ := s.Get(ctx, d.DesktopID)
	if err := validateResizeRecovery(current, &awsx.EC2InstanceStatus{State: "stopped", InstanceType: "m8i.xlarge"}); err != nil {
		t.Fatalf("target lost after failed metadata write: %v", err)
	}
}

func TestValidateResizeTarget(t *testing.T) {
	tests := []struct {
		name       string
		desktop    *store.Desktop
		targetType string
		wantErr    string
	}{
		{
			name:       "valid on demand resize",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "t3.large", MarketType: store.MarketOnDemand},
			targetType: "m7i.xlarge",
		},
		{
			name:       "empty target",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "t3.large"},
			targetType: "",
			wantErr:    "--instance-type is required",
		},
		{
			name:       "unchanged target",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "t3.large"},
			targetType: "t3.large",
			wantErr:    "already uses instance type",
		},
		{
			name:       "spot rejected",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "m7i.xlarge", MarketType: store.MarketSpot},
			targetType: "m8i.xlarge",
			wantErr:    "Spot desktops",
		},
		{
			name:       "terminated rejected",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateTerminated, InstanceType: "t3.large"},
			targetType: "m7i.xlarge",
			wantErr:    "terminated",
		},
		{
			name:       "parked rejected",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateParked, InstanceType: "t3.large"},
			targetType: "m7i.xlarge",
			wantErr:    "parked",
		},
		{
			name:       "nested virt target must support nested virtualization",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "m8i.xlarge", NestedVirt: true},
			targetType: "t3.large",
			wantErr:    "nested virtualization requires",
		},
		{
			name:       "nested virt target supported",
			desktop:    &store.Desktop{DesktopID: "d-1", State: store.StateReady, InstanceType: "m8i.xlarge", NestedVirt: true},
			targetType: "c7i.xlarge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResizeTarget(tt.desktop, tt.targetType)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateResizeTarget: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestCanResizeFromInstanceState(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{state: "running", want: true},
		{state: "stopped", want: true},
		{state: "pending"},
		{state: "stopping"},
		{state: "terminated"},
		{state: ""},
	}

	for _, tt := range tests {
		if got := canResizeFromInstanceState(tt.state); got != tt.want {
			t.Errorf("canResizeFromInstanceState(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}
