package wireguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalManager manages the WireGuard interface on the operator's machine.
// It uses wg-quick to bring the interface up and wg/ip to add peers dynamically
// without requiring a full interface restart.
type LocalManager struct {
	// Interface is the WireGuard interface name (e.g. "wg-aidesktops").
	Interface string
	// ConfigPath is the path to the wg-quick config file
	// (e.g. ~/.ai-desktops/wg-aidesktops.conf).
	ConfigPath string
}

// NewLocalManager returns a LocalManager using the ai-desktops defaults.
// configDir should be ~/.ai-desktops.
func NewLocalManager(iface, configDir string) *LocalManager {
	return &LocalManager{
		Interface:  iface,
		ConfigPath: filepath.Join(configDir, iface+".conf"),
	}
}

// IsUp reports whether the WireGuard interface currently exists on the host.
func (m *LocalManager) IsUp() bool {
	cmd := exec.Command("ip", "link", "show", m.Interface) //nolint:gosec
	return cmd.Run() == nil
}

// Up writes cfg to the config file and brings the interface up with wg-quick.
// Requires sudo (wg-quick needs root).
func (m *LocalManager) Up(cfg PeerConfigInput) error {
	if err := m.WriteConfig(cfg); err != nil {
		return err
	}
	out, err := sudoRun("wg-quick", "up", m.ConfigPath)
	if err != nil {
		return fmt.Errorf("wg-quick up: %w\n%s", err, out)
	}
	return nil
}

// Down tears down the WireGuard interface.
func (m *LocalManager) Down() error {
	out, err := sudoRun("wg-quick", "down", m.ConfigPath)
	if err != nil {
		return fmt.Errorf("wg-quick down: %w\n%s", err, out)
	}
	return nil
}

// AddPeer adds a peer to the live interface using wg set + ip addr/route.
// serverPubKey is the base64 server public key.
// endpoint is host:port.
// clientIP is the client's VPN address in CIDR notation (e.g. "10.99.0.2/32").
// subnet is the VPN subnet (e.g. "10.99.0.0/24") used for route installation.
func (m *LocalManager) AddPeer(serverPubKey, endpoint, clientIP, subnet string) error {
	// Add the peer to the live interface.
	out, err := sudoRun("wg", "set", m.Interface,
		"peer", serverPubKey,
		"endpoint", endpoint,
		"allowed-ips", subnet,
		"persistent-keepalive", "25",
	)
	if err != nil {
		return fmt.Errorf("wg set peer: %w\n%s", err, out)
	}

	// Add the client address to the interface (best-effort; may already exist).
	sudoRun("ip", "addr", "add", clientIP, "dev", m.Interface) //nolint:errcheck

	// Add route for the subnet (best-effort; may already exist).
	clientAddr := strings.SplitN(clientIP, "/", 2)[0]
	sudoRun("ip", "route", "add", subnet, "dev", m.Interface, "src", clientAddr) //nolint:errcheck

	return nil
}

// WriteConfig renders and atomically writes the wg-quick config file.
func (m *LocalManager) WriteConfig(in PeerConfigInput) error {
	content, err := RenderPeerConfig(in)
	if err != nil {
		return fmt.Errorf("render peer config: %w", err)
	}

	dir := filepath.Dir(m.ConfigPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".wg-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) //nolint:errcheck

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}
	if err := os.Rename(tmpPath, m.ConfigPath); err != nil {
		return fmt.Errorf("install config: %w", err)
	}
	return nil
}

// sudoRun runs a command with sudo -n (non-interactive) and returns combined output.
func sudoRun(name string, args ...string) (string, error) {
	all := append([]string{"-n", name}, args...)
	cmd := exec.Command("sudo", all...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
