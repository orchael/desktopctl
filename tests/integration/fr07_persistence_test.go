//go:build integration

// FR-7 — Persistence (and FR-1.3, FR-1.4 lifecycle stop/start)
//
// This file covers stop → start → verify because the lifecycle state changes
// must happen in sequence and the fixture must remain usable after the tests.
//
// Acceptance criteria tested here:
//
//	AC-1.3 / FR-1.3  stop transitions lifecycle_state to "stopped"
//	AC-1.4 / FR-1.4  start transitions lifecycle_state back to "ready"
//	AC-7.1           /workspace files survive stop+start
//	AC-7.2           Installed tools survive stop+start
//	AC-7.3           Terminated desktop disappears from list (verified in TestMain cleanup)
//
// WARNING: these tests mutate the shared fixture.  They run in alphabetical
// order within the file (Go test runner processes tests in file order, files
// alphabetically).  Do not add tests to this file that assume a running
// desktop without a preceding start.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFR7_01_Stop stops the desktop and verifies the DynamoDB state transitions
// to "stopped" (FR-1.3 / AC-1.3 / AC-7.1).
func TestFR7_01_Stop(t *testing.T) {
	if fx.ownedByTest == false {
		t.Skip("skipping stop/start mutation on adopted desktop (set AI_DESKTOPS_EXISTING_ID to skip)")
	}

	_, err := runCLI(context.Background(), 5*time.Minute,
		"stop", fx.ID, "--config", configPath)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Wait for DynamoDB state=stopped (EC2 stop is asynchronous).
	if err := waitForState(fx.ID, "stopped", 5*time.Minute); err != nil {
		t.Fatalf("timed out waiting for stopped: %v", err)
	}

	out, err := runCLI(context.Background(), 30*time.Second,
		"status", fx.ID, "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("status after stop: %v", err)
	}
	var d struct {
		State string `json:"lifecycle_state"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if d.State != "stopped" {
		t.Errorf("state after stop: got %q, want stopped", d.State)
	}
}

// TestFR7_02_Start starts the previously stopped desktop and verifies it
// returns to "ready" (FR-1.4 / AC-1.4).
func TestFR7_02_Start(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("skipping stop/start mutation on adopted desktop")
	}

	_, err := runCLI(context.Background(), 5*time.Minute,
		"start", fx.ID, "--config", configPath)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait for state=ready (EC2 start + health checks take time).
	if err := waitForState(fx.ID, "ready", 10*time.Minute); err != nil {
		t.Fatalf("timed out waiting for ready: %v", err)
	}

	out, err := runCLI(context.Background(), 30*time.Second,
		"status", fx.ID, "--config", configPath, "--json")
	if err != nil {
		t.Fatalf("status after start: %v", err)
	}
	var d struct {
		State string `json:"lifecycle_state"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if d.State != "ready" {
		t.Errorf("state after start: got %q, want ready", d.State)
	}
}

// TestFR7_03_WorkspacePersists verifies that /workspace contents survive the
// stop+start cycle (AC-7.1, AC-7.2).
func TestFR7_03_WorkspacePersists(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("skipping persistence check on adopted desktop (no stop+start performed)")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify workspace persistence")
	}
	if len(fx.Repos) == 0 {
		t.Skip("no repos configured in fixture — set AI_DESKTOPS_TEST_REPO")
	}

	// Wait for SSH to be responsive after start.  After a stop+start cycle the
	// instance gets a new IP and sshd needs time to start; 5 minutes is enough.
	retrySSH(t, 5*time.Minute, func() error {
		_, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo ok")
		return err
	})

	for _, repoURL := range fx.Repos {
		repoName := repoBaseName(repoURL)
		workspacePath := filepath.Join("/workspace", repoName)

		out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
			fmt.Sprintf("test -d %s && echo present", workspacePath))
		if err != nil || !strings.Contains(out, "present") {
			t.Errorf("repo %q missing after stop+start at %s", repoName, workspacePath)
		}
	}
}

// TestFR7_04_ToolsPersist verifies that the base toolchain survives stop+start
// (AC-7.2).
func TestFR7_04_ToolsPersist(t *testing.T) {
	if !fx.ownedByTest {
		t.Skip("skipping toolchain persistence check on adopted desktop")
	}
	if fx.SSHKey == "" {
		t.Skip("no SSH key")
	}

	// Ensure SSH is up first.  Give 5 minutes in case TestFR7_03 was skipped
	// and this is the first SSH attempt after the stop+start cycle.
	retrySSH(t, 5*time.Minute, func() error {
		_, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo ok")
		return err
	})

	tools := []struct {
		cmd, flag string
	}{
		{"git", "--version"},
		{"docker", "--version"},
		{"nvim", "--version"},
		{"tmux", "-V"},
	}
	for _, tool := range tools {
		assertCommandExists(t, fx.SSHTarget, fx.SSHKey, tool.cmd, tool.flag)
	}
}
