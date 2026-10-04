package cmd

import (
	"strings"
	"testing"

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
