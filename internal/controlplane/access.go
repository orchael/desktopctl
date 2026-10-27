package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

// fetchDesktopAccessURL asks the desktop to issue a fresh, one-session URL.
// The stored noVNC URL is only a base address and has no access token.
func fetchDesktopAccessURL(ctx context.Context, desktop *store.Desktop, configuredKey string) (string, error) {
	key := os.Getenv("CONTROL_PLANE_SSH_KEY_PATH")
	if key == "" {
		key = configuredKey
	}
	if key == "" || desktop.SSHTarget == "" || desktop.Hostname == "" {
		return "", errors.New("desktop SSH access is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ssh", //nolint:gosec
		"-i", key,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/tmp/desktopctl-known-hosts",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
		"--", desktop.SSHTarget, "novnc-desktop-url",
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("request desktop access URL: %w", err)
	}
	return parseDesktopAccessURL(string(output), desktop.Hostname)
}

func parseDesktopAccessURL(output, hostname string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		candidate, ok := strings.CutPrefix(strings.TrimSpace(line), "Desktop URL :")
		if !ok {
			continue
		}
		candidate = strings.TrimSpace(candidate)
		parsed, err := url.Parse(candidate)
		if err == nil && parsed.Scheme == "https" && strings.EqualFold(parsed.Hostname(), hostname) && parsed.Path == "/access" && parsed.Query().Get("token") != "" && parsed.User == nil {
			return candidate, nil
		}
	}
	return "", errors.New("desktop returned no valid access URL")
}
