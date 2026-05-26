//go:build integration

// FR-1 — Desktop lifecycle
//
// Acceptance criteria tested here:
//
//	AC-1.1  create returns desktop_id, novnc_url, and ssh_target in JSON output
//	AC-1.2  DynamoDB lifecycle_state is "ready" after create completes
//	AC-1.3  list output includes the desktop ID
//	AC-1.4  status output contains all required fields (id, state, hostname, urls)
//	AC-1.5  stop transitions state to "stopped" (tested via fr7_persistence_test)
//	AC-1.6  start transitions state to "ready"  (tested via fr7_persistence_test)
//	AC-1.7  terminate permanently removes the desktop (cleanup in TestMain)
package integration_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestFR1_CreateOutput verifies that the create command produced a valid JSON
// result with all required fields (AC-1.1).
//
// Note: the actual create is performed by TestMain.  This test validates the
// fixture that TestMain populated.
func TestFR1_CreateOutput(t *testing.T) {
	if fx.ID == "" {
		t.Fatal("fixture desktop ID is empty — create must have failed")
	}
	if !strings.HasPrefix(fx.ID, "d-") {
		t.Errorf("desktop_id should start with 'd-', got %q", fx.ID)
	}
	if fx.Hostname == "" {
		t.Error("create output missing hostname")
	}
	if fx.NoVNCURL == "" {
		t.Error("create output missing novnc_url")
	}
	if !strings.HasPrefix(fx.NoVNCURL, "https://") {
		t.Errorf("novnc_url should be HTTPS, got %q", fx.NoVNCURL)
	}
	if fx.SSHTarget == "" {
		t.Error("create output missing ssh_target")
	}
	if !strings.HasPrefix(fx.SSHTarget, "ubuntu@") {
		t.Errorf("ssh_target should be ubuntu@..., got %q", fx.SSHTarget)
	}
}

// TestFR1_StateReady verifies that the DynamoDB record shows state=ready
// after creation (AC-1.2).
func TestFR1_StateReady(t *testing.T) {
	out, err := runCLI(context.Background(), 30*time.Second,
		"status", fx.ID, "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	var d struct {
		State    string `json:"lifecycle_state"`
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("parse status: %v\nraw: %s", err, out)
	}
	if d.State != "ready" {
		t.Errorf("lifecycle_state: got %q, want ready", d.State)
	}
}

// TestFR1_List verifies that `ai-desktops list` includes the test desktop
// (AC-1.3).
func TestFR1_List(t *testing.T) {
	out, err := runCLI(context.Background(), 30*time.Second,
		"list", "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	// list returns a JSON array of desktop records.
	var desktops []struct {
		DesktopID string `json:"desktop_id"`
	}
	if err := json.Unmarshal(out, &desktops); err != nil {
		t.Fatalf("parse list: %v\nraw: %s", err, out)
	}

	found := false
	for _, d := range desktops {
		if d.DesktopID == fx.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("desktop %q not found in list output", fx.ID)
	}
}

// TestFR1_StatusFields verifies that `status` returns all required fields
// (AC-1.4).
func TestFR1_StatusFields(t *testing.T) {
	out, err := runCLI(context.Background(), 30*time.Second,
		"status", fx.ID, "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	var d struct {
		DesktopID  string `json:"desktop_id"`
		State      string `json:"lifecycle_state"`
		Hostname   string `json:"hostname"`
		NoVNCURL   string `json:"novnc_url"`
		SSHTarget  string `json:"ssh_target"`
		Owner      string `json:"github_owner"`
		InstanceID string `json:"instance_id"`
		StackName  string `json:"stack_name"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("parse status: %v\nraw: %s", err, out)
	}

	if d.DesktopID != fx.ID {
		t.Errorf("desktop_id: got %q, want %q", d.DesktopID, fx.ID)
	}
	if d.State == "" {
		t.Error("status missing lifecycle_state")
	}
	if d.Hostname == "" {
		t.Error("status missing hostname")
	}
	if d.NoVNCURL == "" {
		t.Error("status missing novnc_url")
	}
	if d.SSHTarget == "" {
		t.Error("status missing ssh_target")
	}
	if d.Owner == "" {
		t.Error("status missing github_owner")
	}
	if d.InstanceID == "" {
		t.Error("status missing instance_id")
	}
	if d.StackName == "" {
		t.Error("status missing stack_name")
	}
}

// TestFR1_CreateRejectsWithoutOwner verifies that `create` fails clearly when
// --github-owner is not provided and no github.owner is set in config (AC-6.3 overlaps here).
func TestFR1_CreateRejectsWithoutOwner(t *testing.T) {
	// /dev/null yields an empty config (all zero values), so github.owner is
	// unset and the CLI must reject the invocation before touching any infra.
	_, err := runCLI(context.Background(), 30*time.Second,
		"create", "--config", "/dev/null", "--preview")
	if err == nil {
		t.Error("create without --github-owner should fail but succeeded")
	}
}
