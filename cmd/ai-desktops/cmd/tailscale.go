package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultTailscaleAPIBaseURL = "https://api.tailscale.com"

var errTailscaleAPIKeyMissing = errors.New("TAILSCALE_API_KEY is required to remove Tailscale devices")

type tailscaleDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
}

type tailscaleDevicesResponse struct {
	Devices []tailscaleDevice `json:"devices"`
}

func removeTailscaleDesktopDevice(ctx context.Context, tailnet, desktopID string) error {
	tailnet = strings.TrimSpace(tailnet)
	desktopID = strings.TrimSpace(desktopID)
	if tailnet == "" || desktopID == "" {
		return nil
	}

	apiKey := strings.TrimSpace(os.Getenv("TAILSCALE_API_KEY"))
	if apiKey == "" {
		return errTailscaleAPIKeyMissing
	}

	baseURL := strings.TrimSpace(os.Getenv("TAILSCALE_API_BASE_URL"))
	if baseURL == "" {
		baseURL = defaultTailscaleAPIBaseURL
	}

	client := &tailscaleAPIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	return client.removeDeviceByDesktopID(ctx, tailnet, desktopID)
}

type tailscaleAPIClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func (c *tailscaleAPIClient) removeDeviceByDesktopID(ctx context.Context, tailnet, desktopID string) error {
	devices, err := c.listDevices(ctx, tailnet)
	if err != nil {
		return err
	}

	var matches []tailscaleDevice
	for _, device := range devices {
		if tailscaleDeviceMatchesDesktop(device, desktopID) {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	for _, device := range matches {
		if strings.TrimSpace(device.ID) == "" {
			return fmt.Errorf("tailscale device %q matched desktop %q but has no id", device.Name, desktopID)
		}
		if err := c.deleteDevice(ctx, device.ID); err != nil {
			return err
		}
	}
	return nil
}

func (c *tailscaleAPIClient) listDevices(ctx context.Context, tailnet string) ([]tailscaleDevice, error) {
	u := c.baseURL + "/api/v2/tailnet/" + url.PathEscape(tailnet) + "/devices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.apiKey, "")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list tailscale devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list tailscale devices: unexpected status %s", resp.Status)
	}

	var out tailscaleDevicesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tailscale devices: %w", err)
	}
	return out.Devices, nil
}

func (c *tailscaleAPIClient) deleteDevice(ctx context.Context, deviceID string) error {
	u := c.baseURL + "/api/v2/device/" + url.PathEscape(deviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.apiKey, "")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete tailscale device %q: %w", deviceID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete tailscale device %q: unexpected status %s", deviceID, resp.Status)
	}
	return nil
}

func tailscaleDeviceMatchesDesktop(device tailscaleDevice, desktopID string) bool {
	candidates := []string{device.Name, device.Hostname}
	for _, candidate := range candidates {
		if tailscaleNameMatchesDesktop(candidate, desktopID) {
			return true
		}
	}
	return false
}

func tailscaleNameMatchesDesktop(name, desktopID string) bool {
	name = strings.TrimSpace(strings.TrimSuffix(name, "."))
	if name == "" {
		return false
	}
	if name == desktopID {
		return true
	}
	prefix, _, found := strings.Cut(name, ".")
	return found && prefix == desktopID
}
