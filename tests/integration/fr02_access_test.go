//go:build integration

// FR-2 — Desktop access
//
// Acceptance criteria tested here:
//
//	AC-2.1  noVNC HTTPS endpoint (port 8443) returns a successful response
//	AC-2.2  SSH port 22 is reachable on the desktop hostname
//	AC-2.3  Browser URL is primary; SSH is secondary (structural, verified by AC-2.1/2.2)
//	AC-2.4  Create output includes novnc_url and ssh_target (covered in FR-1 tests)
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"net"
)

// TestFR2_NoVNCHTTPSReachable verifies that the noVNC HTTPS endpoint on port
// 8443 returns a non-5xx response (AC-2.1).
//
// TLS verification is skipped because dev desktops may use Let's Encrypt certs
// that haven't fully propagated, or self-signed certs.
func TestFR2_NoVNCHTTPSReachable(t *testing.T) {
	if fx.NoVNCURL == "" {
		t.Skip("novnc_url not set in fixture")
	}
	if !strings.Contains(fx.NoVNCURL, ":8443") {
		t.Errorf("novnc_url must include port 8443, got %q", fx.NoVNCURL)
	}
	retryHTTPS(t, fx.NoVNCURL, 5*time.Minute, true /* skipVerify for dev */)
}

// TestFR2_SSHPortReachable verifies that port 22 on the desktop is reachable
// with a TCP connection (AC-2.2).  We do not authenticate; a successful TCP
// handshake is sufficient to confirm the port is open.
func TestFR2_SSHPortReachable(t *testing.T) {
	if fx.Hostname == "" {
		t.Skip("hostname not set in fixture")
	}

	addr := fmt.Sprintf("%s:22", fx.Hostname)
	retrySSH(t, 3*time.Minute, func() error {
		conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	})
}

// TestFR2_SSHAuthentication verifies that the SSH key from the fixture can
// successfully authenticate to the desktop (required for all SSH-based checks
// in subsequent tests).
func TestFR2_SSHAuthentication(t *testing.T) {
	if fx.SSHKey == "" {
		t.Skip("AI_DESKTOPS_SSH_KEY not set — skipping SSH auth test")
	}
	out, err := sshRunE(fx.SSHTarget, fx.SSHKey, "echo integration-ok")
	if err != nil {
		t.Fatalf("SSH auth failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "integration-ok") {
		t.Errorf("SSH echo test: unexpected output %q", out)
	}
}
