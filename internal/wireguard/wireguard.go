// Package wireguard provides key generation, config rendering, and QR code
// output for WireGuard VPN peer management in ai-desktops.
//
// Interface name: wg-aidesktops (distinct from any other WireGuard interfaces
// that may exist on the operator machine).
package wireguard

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"text/template"

	"golang.org/x/crypto/curve25519"

	"github.com/orchael/ai-desktops/internal/config"
)

const (
	// InterfaceName is the WireGuard interface name used on ai-desktops desktops.
	InterfaceName = config.DefaultWireGuardInterface
	// ServerIP is the VPN address assigned to the desktop's WireGuard server.
	ServerIPPrefix = config.DefaultWireGuardServerIP
)

// GeneratePrivateKey generates a new WireGuard private key and returns it
// base64-encoded.
func GeneratePrivateKey() (string, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", fmt.Errorf("generate wireguard private key: %w", err)
	}
	// Apply X25519 clamping as required by WireGuard.
	key[0] &= 248
	key[31] = (key[31] & 127) | 64
	return base64.StdEncoding.EncodeToString(key[:]), nil
}

// PublicKey derives the WireGuard public key from a base64-encoded private key.
func PublicKey(privateKeyB64 string) (string, error) {
	privBytes, err := base64.StdEncoding.DecodeString(privateKeyB64)
	if err != nil {
		return "", fmt.Errorf("decode private key: %w", err)
	}
	if len(privBytes) != 32 {
		return "", fmt.Errorf("private key must be 32 bytes, got %d", len(privBytes))
	}
	pubBytes, err := curve25519.X25519(privBytes, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("derive public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pubBytes), nil
}

// ServerIP returns the VPN IP address of the WireGuard server for the given
// subnet (always the first host address, i.e. network+1). Returns an error if
// the subnet cannot be parsed.
func ServerIP(subnet string) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse wireguard subnet %q: %w", subnet, err)
	}
	ip := cloneIP(ipNet.IP)
	incrementIP(ip)
	return ip.String(), nil
}

// AllocatePeerIP finds the next unused /32 address in the subnet for a new peer.
// The server always holds .1; peers are assigned .2, .3, etc. in order.
// Returns the peer IP in CIDR notation (e.g. "10.99.0.2/32").
func AllocatePeerIP(subnet string, existingPeers []config.WireGuardPeer) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("parse wireguard subnet %q: %w", subnet, err)
	}

	used := map[string]bool{}
	for _, p := range existingPeers {
		ip, _, _ := net.ParseCIDR(p.AllowedIP)
		if ip != nil {
			used[ip.String()] = true
		}
	}

	// Compute broadcast: network | ^mask.
	broadcast := make(net.IP, len(ipNet.IP))
	for i := range ipNet.IP {
		broadcast[i] = ipNet.IP[i] | ^ipNet.Mask[i]
	}

	// Server holds .1; start peer allocation at .2.
	ip := cloneIP(ipNet.IP)
	incrementIP(ip) // .1 (server)
	for {
		incrementIP(ip)
		if !ipNet.Contains(ip) || ip.Equal(broadcast) {
			return "", fmt.Errorf("wireguard subnet %s is full", subnet)
		}
		if !used[ip.String()] {
			return ip.String() + "/32", nil
		}
	}
}

// ServerConfigInput holds everything needed to render the server-side
// wg-aidesktops.conf that is written to the desktop at first boot.
type ServerConfigInput struct {
	PrivateKey string
	ServerIP   string // e.g. "10.99.0.1/24"
	Port       int
	Interface  string
	Peers      []config.WireGuardPeer
}

const serverConfigTpl = `[Interface]
PrivateKey = {{ .PrivateKey }}
Address = {{ .ServerIP }}
ListenPort = {{ .Port }}
PostUp = iptables -A FORWARD -i {{ .Interface }} -j ACCEPT; iptables -A FORWARD -o {{ .Interface }} -j ACCEPT; iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE
PostDown = iptables -D FORWARD -i {{ .Interface }} -j ACCEPT; iptables -D FORWARD -o {{ .Interface }} -j ACCEPT; iptables -t nat -D POSTROUTING -o eth0 -j MASQUERADE
{{ range .Peers }}
[Peer]
# {{ .Name }}
PublicKey = {{ .PublicKey }}
AllowedIPs = {{ .AllowedIP }}
{{ end }}`

// RenderServerConfig renders the WireGuard server configuration file.
func RenderServerConfig(in ServerConfigInput) (string, error) {
	if in.Interface == "" {
		in.Interface = InterfaceName
	}
	tmpl, err := template.New("wg-server").Parse(serverConfigTpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// PeerConfigInput holds the data for a single operator/client config.
type PeerConfigInput struct {
	// Client side
	PeerPrivateKey string
	PeerIP         string // e.g. "10.99.0.2/32"
	DNSServer      string // optional; leave empty for no DNS line

	// Server side
	ServerPublicKey     string
	ServerEndpoint      string // host:port
	AllowedIPs          string // e.g. "10.99.0.0/24" or "0.0.0.0/0"
	PersistentKeepalive int
}

const peerConfigTpl = `[Interface]
PrivateKey = {{ .PeerPrivateKey }}
Address = {{ .PeerIP }}
{{ if .DNSServer }}DNS = {{ .DNSServer }}
{{ end }}
[Peer]
PublicKey = {{ .ServerPublicKey }}
Endpoint = {{ .ServerEndpoint }}
AllowedIPs = {{ .AllowedIPs }}
{{ if .PersistentKeepalive }}PersistentKeepalive = {{ .PersistentKeepalive }}
{{ end }}`

// RenderPeerConfig renders the WireGuard client configuration for an operator device.
func RenderPeerConfig(in PeerConfigInput) (string, error) {
	if in.AllowedIPs == "" {
		in.AllowedIPs = "10.99.0.0/24"
	}
	if in.PersistentKeepalive == 0 {
		in.PersistentKeepalive = 25
	}
	tmpl, err := template.New("wg-peer").Parse(peerConfigTpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()) + "\n", nil
}

// PrintQR prints the given WireGuard config as a UTF-8 QR code to stdout so
// it can be scanned by a mobile WireGuard app.
// It uses a simple block-character renderer so no external library is needed.
func PrintQR(configText string) error {
	// Delegate to an internal pure-Go QR renderer.
	return printQRBlocks(configText)
}

// --- helpers ---

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
