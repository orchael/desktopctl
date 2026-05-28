//go:build integration

package integration_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// sshRun executes a command on the desktop over SSH and returns combined
// stdout+stderr.  It uses the operator SSH key from the fixture.
// StrictHostKeyChecking is disabled because test desktops are freshly
// provisioned with unknown host keys.
func sshRun(t interface {
	Helper()
	Fatalf(string, ...any)
}, host, keyPath, command string) string {
	t.Helper()
	out, err := sshRunE(host, keyPath, command)
	if err != nil {
		t.Fatalf("ssh %s %q: %v\noutput: %s", host, command, err, out)
	}
	return strings.TrimSpace(out)
}

// sshRunE is the error-returning variant of sshRun.
func sshRunE(host, keyPath, command string) (string, error) {
	args := []string{
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=30",
		"-o", "BatchMode=yes",
		"-o", "LogLevel=ERROR",
		host, // ubuntu@hostname
		command,
	}
	cmd := exec.Command("ssh", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// retrySSH runs fn until it returns no error or the timeout elapses.  It logs
// each failure so the test output shows progress without failing immediately.
func retrySSH(t interface {
	Helper()
	Logf(string, ...any)
	Fatalf(string, ...any)
}, timeout time.Duration, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		} else {
			lastErr = err
			t.Logf("retrying: %v", err)
			time.Sleep(10 * time.Second)
		}
	}
	t.Fatalf("timed out after %s: %v", timeout, lastErr)
}

// httpsGet performs an HTTPS GET to url, optionally skipping TLS verification
// (needed when using self-signed certs in dev).  Returns the status code.
func httpsGet(ctx context.Context, url string, skipVerify bool) (int, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipVerify}, //nolint:gosec
	}
	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// retryHTTPS polls url until it returns a 2xx/3xx or the timeout elapses.
func retryHTTPS(t interface {
	Helper()
	Logf(string, ...any)
	Fatalf(string, ...any)
}, url string, timeout time.Duration, skipVerify bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		code, err := httpsGet(context.Background(), url, skipVerify)
		if err == nil && code < 500 {
			t.Logf("HTTPS %s => %d", url, code)
			return
		}
		t.Logf("HTTPS %s: code=%d err=%v (retrying)", url, code, err)
		time.Sleep(15 * time.Second)
	}
	t.Fatalf("HTTPS %s not reachable after %s", url, timeout)
}

// assertSystemdActive asserts that the named systemd unit is active.
func assertSystemdActive(t interface {
	Helper()
	Errorf(string, ...any)
	Fatalf(string, ...any)
}, host, keyPath, unit string) {
	t.Helper()
	out, err := sshRunE(host, keyPath, fmt.Sprintf("systemctl is-active %s 2>&1", unit))
	state := strings.TrimSpace(out)
	if err != nil || state != "active" {
		t.Errorf("systemd unit %q: got %q (err=%v), want active", unit, state, err)
	}
}

// assertCommandExists asserts that a command is on PATH with the given version
// flag producing output (e.g. --version).
func assertCommandExists(t interface {
	Helper()
	Errorf(string, ...any)
}, host, keyPath, cmd, versionFlag string) {
	t.Helper()
	out, err := sshRunE(host, keyPath, fmt.Sprintf("which %s && %s %s 2>&1 | head -1", cmd, cmd, versionFlag))
	if err != nil {
		t.Errorf("command %q not found: %v\noutput: %s", cmd, err, out)
	}
}
