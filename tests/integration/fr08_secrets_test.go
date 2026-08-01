//go:build integration

// FR-8 — Secret injection
//
// These tests verify that secrets passed via --secret at create time are
// injected into the desktop environment (AC-8 / issue #90).
//
// The suite always passes the github secret path so these tests run as part of
// the standard suite without requiring extra environment variables.
//
// Acceptance criteria tested here:
//
//	AC-8.1  /home/ubuntu/.desktop-secrets exists and is non-empty
//	AC-8.2  /home/ubuntu/.bashrc sources .desktop-secrets
//	AC-8.3  /home/ubuntu/.config/environment.d/desktop-secrets.conf exists and
//	        is non-empty (for systemd/PAM user-session injection)
package integration_test

import (
	"strings"
	"testing"
	"time"
)

// TestFR8_SecretFilePresent verifies that cloud-init wrote the secrets file
// to /home/ubuntu/.desktop-secrets (AC-8.1).
func TestFR8_SecretFilePresent(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify secret injection")
	}
	if len(fx.Secrets) == 0 {
		t.Skip("no secrets configured in fixture")
	}

	retrySSH(t, 3*time.Minute, func() error {
		_, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo ok")
		return err
	})

	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		"test -s /home/ubuntu/.desktop-secrets && echo present")
	if err != nil || !strings.Contains(out, "present") {
		t.Errorf(".desktop-secrets missing or empty on desktop (err=%v, output=%q)", err, out)
	}
}

// TestFR8_SecretBashrcSources verifies that .bashrc has an active (un-commented)
// line that sources .desktop-secrets so interactive SSH sessions inherit the
// injected variables (AC-8.2).
func TestFR8_SecretBashrcSources(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify secret injection")
	}
	if len(fx.Secrets) == 0 {
		t.Skip("no secrets configured in fixture")
	}

	retrySSH(t, 3*time.Minute, func() error {
		_, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo ok")
		return err
	})

	// grep for a non-commented line that sources .desktop-secrets.
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		`grep -Eq '^[^#].*(source|\.).*desktop-secrets' /home/ubuntu/.bashrc`)
	if err != nil {
		t.Errorf(".bashrc does not have an active (un-commented) line sourcing .desktop-secrets (err=%v, output=%q)", err, out)
	}
}

// TestFR8_SecretSystemdEnvPresent verifies that the systemd environment.d file
// was written so that services launched in the user session also see the
// injected variables (AC-8.3).
func TestFR8_SecretSystemdEnvPresent(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("no SSH key — cannot verify secret injection")
	}
	if len(fx.Secrets) == 0 {
		t.Skip("no secrets configured in fixture")
	}

	retrySSH(t, 3*time.Minute, func() error {
		_, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo ok")
		return err
	})

	out, err := sshRunE(fx.SSHTarget, fx.SSHKey,
		"test -s /home/ubuntu/.config/environment.d/desktop-secrets.conf && echo present")
	if err != nil || !strings.Contains(out, "present") {
		t.Errorf("environment.d/desktop-secrets.conf missing or empty (err=%v, output=%q)", err, out)
	}
}
