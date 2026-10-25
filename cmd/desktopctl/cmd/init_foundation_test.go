package cmd

import (
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/pulumi"
)

func TestSetFoundationZoneIDClearsPreviousDelegation(t *testing.T) {
	stackCfg := pulumi.StackConfig{"zoneId": "ZOLD"}
	setFoundationZoneID(stackCfg, "", "ZOLD")
	if zoneID, ok := stackCfg["zoneId"]; !ok || zoneID != "" {
		t.Fatalf("no-DNS update must set an empty zoneId, got %q (present %t)", zoneID, ok)
	}
	setFoundationZoneID(stackCfg, "desktops.example.com", "ZNEW")
	if stackCfg["zoneId"] != "ZNEW" {
		t.Fatalf("DNS update must restore the pinned zone ID, got %q", stackCfg["zoneId"])
	}
}

func TestEncodeAllowedPorts(t *testing.T) {
	tests := []struct {
		name  string
		ports []int
		want  string
		err   string
	}{
		{name: "none clears prior config", want: "[]"},
		{name: "multiple and duplicate", ports: []int{3000, 9000, 3000}, want: "[3000,9000]"},
		{name: "minimum and maximum", ports: []int{1, 65535}, want: "[1,65535]"},
		{name: "zero", ports: []int{0}, err: "between 1 and 65535"},
		{name: "ssh stays restricted", ports: []int{22}, err: "operator_cidr"},
		{name: "too high", ports: []int{65536}, err: "between 1 and 65535"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := encodeAllowedPorts(tt.ports)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("encodeAllowedPorts(%v) = %q, %v; want %q", tt.ports, got, err, tt.want)
			}
		})
	}
}
