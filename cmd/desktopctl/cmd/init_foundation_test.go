package cmd

import (
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
