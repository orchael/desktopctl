package wireguard_test

import (
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/wireguard"
)

func TestGeneratePrivateKey(t *testing.T) {
	key, err := wireguard.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("private key is not valid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("private key length: got %d, want 32", len(decoded))
	}
	// Verify clamping bits.
	if decoded[0]&7 != 0 {
		t.Error("clamping: low 3 bits of byte 0 must be 0")
	}
	if decoded[31]&128 != 0 {
		t.Error("clamping: high bit of byte 31 must be 0")
	}
	if decoded[31]&64 == 0 {
		t.Error("clamping: bit 6 of byte 31 must be 1")
	}
}

func TestPublicKey(t *testing.T) {
	priv, err := wireguard.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	pub, err := wireguard.PublicKey(priv)
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(pub)
	if err != nil {
		t.Fatalf("public key is not valid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("public key length: got %d, want 32", len(decoded))
	}
	// Same private key must produce same public key.
	pub2, err := wireguard.PublicKey(priv)
	if err != nil {
		t.Fatalf("PublicKey second call: %v", err)
	}
	if pub != pub2 {
		t.Error("public key derivation is not deterministic")
	}
	// Two different private keys must produce different public keys.
	priv2, _ := wireguard.GeneratePrivateKey()
	pub3, _ := wireguard.PublicKey(priv2)
	if pub == pub3 {
		t.Error("two distinct private keys produced the same public key")
	}
}

func TestAllocatePeerIP_Empty(t *testing.T) {
	ip, err := wireguard.AllocatePeerIP("10.99.0.0/24", nil)
	if err != nil {
		t.Fatalf("AllocatePeerIP: %v", err)
	}
	if ip != "10.99.0.2/32" {
		t.Errorf("first peer IP: got %q, want %q", ip, "10.99.0.2/32")
	}
}

func TestAllocatePeerIP_Sequential(t *testing.T) {
	peers := []config.WireGuardPeer{
		{Name: "p1", PublicKey: "x", AllowedIP: "10.99.0.2/32"},
		{Name: "p2", PublicKey: "y", AllowedIP: "10.99.0.3/32"},
	}
	ip, err := wireguard.AllocatePeerIP("10.99.0.0/24", peers)
	if err != nil {
		t.Fatalf("AllocatePeerIP: %v", err)
	}
	if ip != "10.99.0.4/32" {
		t.Errorf("next IP: got %q, want %q", ip, "10.99.0.4/32")
	}
}

func TestAllocatePeerIP_InvalidSubnet(t *testing.T) {
	_, err := wireguard.AllocatePeerIP("notacidr", nil)
	if err == nil {
		t.Error("expected error for invalid subnet, got nil")
	}
}

func TestAllocatePeerIP_SubnetBoundary(t *testing.T) {
	// /30 has 4 addresses: .0 (net), .1 (server), .2 (peer), .3 (broadcast).
	// Only .2 should be allocatable.
	ip, err := wireguard.AllocatePeerIP("10.99.0.0/30", nil)
	if err != nil {
		t.Fatalf("AllocatePeerIP /30: %v", err)
	}
	if ip != "10.99.0.2/32" {
		t.Errorf("got %q, want 10.99.0.2/32", ip)
	}
	// Now with .2 used, the subnet should be full.
	peers := []config.WireGuardPeer{{Name: "p1", PublicKey: "x", AllowedIP: "10.99.0.2/32"}}
	_, err = wireguard.AllocatePeerIP("10.99.0.0/30", peers)
	if err == nil {
		t.Error("expected full-subnet error, got nil")
	}
}

func TestRenderServerConfig(t *testing.T) {
	in := wireguard.ServerConfigInput{
		PrivateKey: "PRIVKEY==",
		ServerIP:   "10.99.0.1/24",
		Port:       51820,
		Interface:  "wg-aidesktops",
		Peers: []config.WireGuardPeer{
			{Name: "laptop", PublicKey: "PUBKEY==", AllowedIP: "10.99.0.2/32"},
		},
	}
	out, err := wireguard.RenderServerConfig(in)
	if err != nil {
		t.Fatalf("RenderServerConfig: %v", err)
	}
	for _, want := range []string{
		"PrivateKey = PRIVKEY==",
		"Address = 10.99.0.1/24",
		"ListenPort = 51820",
		"wg-aidesktops",
		"PublicKey = PUBKEY==",
		"AllowedIPs = 10.99.0.2/32",
		"laptop",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("server config missing %q\nfull output:\n%s", want, out)
		}
	}
	// Interface name should appear in PostUp/PostDown.
	if strings.Count(out, "wg-aidesktops") < 2 {
		t.Error("server config should reference wg-aidesktops in PostUp/PostDown")
	}
}

func TestRenderPeerConfig(t *testing.T) {
	in := wireguard.PeerConfigInput{
		PeerPrivateKey:      "PEERPRIV==",
		PeerIP:              "10.99.0.2/32",
		ServerPublicKey:     "SERVERPUB==",
		ServerEndpoint:      "d-abc123.desktops.orchael.dev:51820",
		AllowedIPs:          "10.99.0.0/24",
		PersistentKeepalive: 25,
	}
	out, err := wireguard.RenderPeerConfig(in)
	if err != nil {
		t.Fatalf("RenderPeerConfig: %v", err)
	}
	for _, want := range []string{
		"PrivateKey = PEERPRIV==",
		"Address = 10.99.0.2/32",
		"PublicKey = SERVERPUB==",
		"Endpoint = d-abc123.desktops.orchael.dev:51820",
		"AllowedIPs = 10.99.0.0/24",
		"PersistentKeepalive = 25",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("peer config missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderPeerConfig_Defaults(t *testing.T) {
	in := wireguard.PeerConfigInput{
		PeerPrivateKey:  "PEERPRIV==",
		PeerIP:          "10.99.0.2/32",
		ServerPublicKey: "SERVERPUB==",
		ServerEndpoint:  "host:51820",
	}
	out, err := wireguard.RenderPeerConfig(in)
	if err != nil {
		t.Fatalf("RenderPeerConfig defaults: %v", err)
	}
	if !strings.Contains(out, "AllowedIPs = 10.99.0.0/24") {
		t.Error("default AllowedIPs should be 10.99.0.0/24")
	}
	if !strings.Contains(out, "PersistentKeepalive = 25") {
		t.Error("default PersistentKeepalive should be 25")
	}
}

func TestInterfaceName(t *testing.T) {
	if wireguard.InterfaceName != "wg-aidesktops" {
		t.Errorf("InterfaceName: got %q, want %q", wireguard.InterfaceName, "wg-aidesktops")
	}
}

// TestIPNotCollideWithServer verifies the server IP (.1) is never allocated to a peer.
func TestIPNotCollideWithServer(t *testing.T) {
	ip, err := wireguard.AllocatePeerIP("10.99.0.0/24", nil)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, _ := net.ParseCIDR(ip)
	if parsed.String() == "10.99.0.1" {
		t.Error("peer was allocated the server IP .1")
	}
}
