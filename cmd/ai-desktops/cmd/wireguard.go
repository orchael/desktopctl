package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/orchael/ai-desktops/internal/wireguard"
	"github.com/spf13/cobra"
)

var wireguardCmd = &cobra.Command{
	Use:   "wireguard",
	Short: "Manage WireGuard VPN peers for ai-desktops",
	Long: `wireguard provides subcommands for managing WireGuard VPN peer configuration.

WireGuard is the preferred way to secure operator access to desktops. When
enabled, SSH/noVNC access is restricted to the VPN subnet (10.99.0.0/24 by
default) and the security group opens UDP 51820 for handshakes.

One-time setup:
  1. ai-desktops wireguard init                    — enable WireGuard in config.yaml
  2. ai-desktops wireguard add-peer <hostname>     — generate a peer for this machine
  3. ai-desktops wireguard set-local-peer <name>   — mark that peer as this machine's peer
  4. ai-desktops init-foundation                   — apply updated security groups

Per desktop (automatic when using 'ai-desktops ssh'):
  ai-desktops wireguard connect <desktop-id>       — configure and bring up the VPN tunnel`,
}

// ---- wireguard init ----

var wireguardInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Enable WireGuard VPN in config.yaml with sensible defaults",
	Long: `init enables WireGuard VPN support and writes the defaults into config.yaml.
After running init, add at least one peer (wireguard add-peer) and re-run
init-foundation to update the security groups before creating desktops.`,
	Args: cobra.NoArgs,
	RunE: runWireGuardInit,
}

func runWireGuardInit(_ *cobra.Command, _ []string) error {
	if cfg.WireGuard.Enabled {
		fmt.Println("WireGuard is already enabled in config.")
		printWireGuardStatus()
		return nil
	}
	cfg.WireGuard.Enabled = true
	cfg.Defaults() // fills Port, Subnet, Interface defaults
	if err := cfg.Save(cfgFile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("WireGuard enabled.")
	printWireGuardStatus()
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  ai-desktops wireguard add-peer <name>   — add an operator device")
	fmt.Println("  ai-desktops init-foundation              — update security groups")
	return nil
}

func printWireGuardStatus() {
	fmt.Printf("  Interface : %s\n", cfg.WireGuard.Interface)
	fmt.Printf("  Subnet    : %s\n", cfg.WireGuard.Subnet)
	fmt.Printf("  Port      : %d\n", cfg.WireGuard.Port)
	fmt.Printf("  Peers     : %d\n", len(cfg.WireGuard.Peers))
}

// ---- wireguard add-peer ----

var (
	addPeerDesktop string
)

var wireguardAddPeerCmd = &cobra.Command{
	Use:   "add-peer <name>",
	Short: "Add a WireGuard peer (operator device) and print the client config",
	Long: `add-peer generates a new WireGuard keypair for an operator device, stores
the public key in config.yaml, and saves the private key to
~/.ai-desktops/peers/<name>.key (mode 0600).

Use --desktop to also apply the peer live to a running desktop without reboot.`,
	Args: cobra.ExactArgs(1),
	RunE: runWireGuardAddPeer,
}

func init() {
	wireguardAddPeerCmd.Flags().StringVar(&addPeerDesktop, "desktop", "",
		"also apply the peer live to this running desktop (desktop ID)")
}

func runWireGuardAddPeer(_ *cobra.Command, args []string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled; run 'ai-desktops wireguard init' first")
	}
	name := args[0]
	for _, p := range cfg.WireGuard.Peers {
		if p.Name == name {
			return fmt.Errorf("peer %q already exists; use a different name or remove it first", name)
		}
	}

	privKey, err := wireguard.GeneratePrivateKey()
	if err != nil {
		return err
	}
	pubKey, err := wireguard.PublicKey(privKey)
	if err != nil {
		return err
	}
	peerIP, err := wireguard.AllocatePeerIP(cfg.WireGuard.Subnet, cfg.WireGuard.Peers)
	if err != nil {
		return err
	}

	// Save the private key to disk before updating config so we never lose it.
	keyPath, err := savePeerKey(name, privKey)
	if err != nil {
		return fmt.Errorf("save peer key: %w", err)
	}

	cfg.WireGuard.Peers = append(cfg.WireGuard.Peers, config.WireGuardPeer{
		Name:      name,
		PublicKey: pubKey,
		AllowedIP: peerIP,
	})
	if err := cfg.Save(cfgFile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Printf("Peer %q added (IP: %s). Private key saved to %s\n", name, peerIP, keyPath)

	if addPeerDesktop != "" {
		if err := applyPeerToDesktop(addPeerDesktop, name, privKey, pubKey, peerIP); err != nil {
			return err
		}
	} else {
		fmt.Printf("Run 'ai-desktops wireguard show-config %s --desktop <id>' to get the full client config.\n", name)
	}
	return nil
}

func applyPeerToDesktop(desktopID, peerName, privKey, pubKey, peerIP string) error {
	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, desktopID)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("desktop %q not found", desktopID)
		}
		return err
	}

	// Fetch the server public key from SSM.
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ssmKey := wireGuardSSMKeyPath(desktopID)
	serverPrivKey, err := awsx.GetSecret(ctx, awsCfg, ssmKey)
	if err != nil {
		return fmt.Errorf("fetch server private key from SSM (%s): %w", ssmKey, err)
	}
	serverPubKey, err := wireguard.PublicKey(serverPrivKey)
	if err != nil {
		return fmt.Errorf("derive server public key: %w", err)
	}

	// Apply the peer live over SSH.
	sshKey := cfg.Desktop.SSHKeyPath
	host := d.Hostname
	if err := applyWireGuardPeerSSH(host, sshKey, cfg.WireGuard.Interface, pubKey, peerIP); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: live apply failed (%v); peer is saved to config but not active on the desktop.\n", err)
		fmt.Fprintf(os.Stderr, "         Reboot or re-run add-peer --desktop %s to retry.\n", desktopID)
	} else {
		fmt.Printf("Peer applied live to %s.\n", desktopID)
	}

	// Render and print full client config.
	peerCfg, err := wireguard.RenderPeerConfig(wireguard.PeerConfigInput{
		PeerPrivateKey:      privKey,
		PeerIP:              peerIP,
		ServerPublicKey:     serverPubKey,
		ServerEndpoint:      fmt.Sprintf("%s:%d", host, cfg.WireGuard.Port),
		AllowedIPs:          cfg.WireGuard.Subnet,
		PersistentKeepalive: 25,
	})
	if err != nil {
		return fmt.Errorf("render peer config: %w", err)
	}
	fmt.Printf("Client config for %q (desktop %s):\n", peerName, desktopID)
	return wireguard.PrintQR(peerCfg)
}

// applyWireGuardPeerSSH SSHes into the desktop and adds the peer live via wg set,
// then persists it to the config file so it survives reboots.
func applyWireGuardPeerSSH(host, keyPath, iface, peerPubKey, allowedIP string) error {
	if keyPath == "" {
		return fmt.Errorf("no SSH key configured (desktop.ssh_key_path)")
	}
	addCmd := fmt.Sprintf(
		"sudo wg set %s peer %s allowed-ips %s && "+
			"sudo wg-quick save %s 2>/dev/null || "+
			"(sudo wg showconf %s > /tmp/wg-aidesktops.conf && sudo mv /tmp/wg-aidesktops.conf /etc/wireguard/%s.conf)",
		iface, peerPubKey, allowedIP, iface, iface, iface,
	)
	return runSSHCommand(host, keyPath, addCmd)
}

func printPeerConfigPlaceholder(name, privKey, _, peerIP string) {
	fmt.Printf("# WireGuard client config for %q\n", name)
	fmt.Printf("# Replace <SERVER_PUBLIC_KEY> and <DESKTOP_HOSTNAME> with values from the desktop.\n")
	fmt.Printf("# Run: ai-desktops wireguard show-config %s --desktop <id>  to get the full config.\n\n", name)
	fmt.Println("[Interface]")
	fmt.Printf("PrivateKey = %s\n", privKey)
	fmt.Printf("Address = %s\n", peerIP)
	fmt.Println()
	fmt.Println("[Peer]")
	fmt.Println("PublicKey = <SERVER_PUBLIC_KEY>")
	fmt.Printf("Endpoint = <DESKTOP_HOSTNAME>:%d\n", cfg.WireGuard.Port)
	fmt.Printf("AllowedIPs = %s\n", cfg.WireGuard.Subnet)
	fmt.Println("PersistentKeepalive = 25")
}

// ---- wireguard remove-peer ----

var removePeerDesktop string

var wireguardRemovePeerCmd = &cobra.Command{
	Use:   "remove-peer <name>",
	Short: "Remove a WireGuard peer from config (and optionally from a live desktop)",
	Args:  cobra.ExactArgs(1),
	RunE:  runWireGuardRemovePeer,
}

func init() {
	wireguardRemovePeerCmd.Flags().StringVar(&removePeerDesktop, "desktop", "",
		"also remove the peer live from this running desktop")
}

func runWireGuardRemovePeer(_ *cobra.Command, args []string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled")
	}
	name := args[0]

	var found *config.WireGuardPeer
	remaining := cfg.WireGuard.Peers[:0]
	for i := range cfg.WireGuard.Peers {
		if cfg.WireGuard.Peers[i].Name == name {
			found = &cfg.WireGuard.Peers[i]
		} else {
			remaining = append(remaining, cfg.WireGuard.Peers[i])
		}
	}
	if found == nil {
		return fmt.Errorf("peer %q not found", name)
	}

	if removePeerDesktop != "" {
		ctx := context.Background()
		s, err := openStore(ctx)
		if err != nil {
			return err
		}
		d, err := s.Get(ctx, removePeerDesktop)
		if err != nil {
			if isNotFound(err) {
				return fmt.Errorf("desktop %q not found", removePeerDesktop)
			}
			return err
		}
		if err := removeWireGuardPeerSSH(d.Hostname, cfg.Desktop.SSHKeyPath, cfg.WireGuard.Interface, found.PublicKey); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: live remove failed (%v); peer removed from config only.\n", err)
		} else {
			fmt.Printf("Peer %q removed live from %s.\n", name, removePeerDesktop)
		}
	}

	cfg.WireGuard.Peers = remaining
	if err := cfg.Save(cfgFile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Peer %q removed from config.\n", name)

	// Best-effort: delete the saved private key.
	if keyPath, err := peerKeyPath(name); err == nil {
		if err := os.Remove(keyPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: could not delete peer key %s: %v\n", keyPath, err)
		}
	}
	return nil
}

func removeWireGuardPeerSSH(host, keyPath, iface, peerPubKey string) error {
	if keyPath == "" {
		return fmt.Errorf("no SSH key configured")
	}
	rmCmd := fmt.Sprintf(
		"sudo wg set %s peer %s remove && "+
			"(sudo wg-quick save %s 2>/dev/null || "+
			"(sudo wg showconf %s > /tmp/wg-aidesktops.conf && sudo mv /tmp/wg-aidesktops.conf /etc/wireguard/%s.conf))",
		iface, peerPubKey, iface, iface, iface,
	)
	return runSSHCommand(host, keyPath, rmCmd)
}

// ---- wireguard list-peers ----

var wireguardListPeersCmd = &cobra.Command{
	Use:   "list-peers",
	Short: "List WireGuard peers configured in config.yaml",
	Args:  cobra.NoArgs,
	RunE:  runWireGuardListPeers,
}

func runWireGuardListPeers(_ *cobra.Command, _ []string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled; run 'ai-desktops wireguard init' first")
	}
	if len(cfg.WireGuard.Peers) == 0 {
		fmt.Println("No peers configured. Run 'ai-desktops wireguard add-peer <name>' to add one.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tALLOWED IP\tPUBLIC KEY (truncated)")
	fmt.Fprintln(w, "----\t----------\t----------------------")
	for _, p := range cfg.WireGuard.Peers {
		pubShort := p.PublicKey
		if len(pubShort) > 16 {
			pubShort = pubShort[:16] + "…"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", p.Name, p.AllowedIP, pubShort)
	}
	return w.Flush()
}

// ---- wireguard show-config ----

var showConfigDesktop string

var wireguardShowConfigCmd = &cobra.Command{
	Use:   "show-config <peer-name>",
	Short: "Render and display the WireGuard client config for a peer",
	Long: `show-config renders the complete WireGuard client config for a named peer.
The private key is loaded automatically from ~/.ai-desktops/peers/<name>.key.

Use --desktop to look up the server public key and endpoint automatically.
Without --desktop, a placeholder config is printed.`,
	Args: cobra.ExactArgs(1),
	RunE: runWireGuardShowConfig,
}

func init() {
	wireguardShowConfigCmd.Flags().StringVar(&showConfigDesktop, "desktop", "",
		"desktop ID to retrieve server public key and endpoint")
}

func runWireGuardShowConfig(_ *cobra.Command, args []string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled")
	}
	name := args[0]

	var peer *config.WireGuardPeer
	for i := range cfg.WireGuard.Peers {
		if cfg.WireGuard.Peers[i].Name == name {
			peer = &cfg.WireGuard.Peers[i]
			break
		}
	}
	if peer == nil {
		return fmt.Errorf("peer %q not found in config", name)
	}

	privKey, err := loadPeerKey(name)
	if err != nil {
		return fmt.Errorf("load peer key: %w", err)
	}

	if showConfigDesktop == "" {
		fmt.Println("No --desktop specified. Printing placeholder config.")
		printPeerConfigPlaceholder(name, privKey, peer.PublicKey, peer.AllowedIP)
		return nil
	}

	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, showConfigDesktop)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("desktop %q not found", showConfigDesktop)
		}
		return err
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	serverPrivKey, err := awsx.GetSecret(ctx, awsCfg, wireGuardSSMKeyPath(showConfigDesktop))
	if err != nil {
		return fmt.Errorf("fetch server key: %w", err)
	}
	serverPubKey, err := wireguard.PublicKey(serverPrivKey)
	if err != nil {
		return fmt.Errorf("derive server public key: %w", err)
	}

	peerCfg, err := wireguard.RenderPeerConfig(wireguard.PeerConfigInput{
		PeerPrivateKey:      privKey,
		PeerIP:              peer.AllowedIP,
		ServerPublicKey:     serverPubKey,
		ServerEndpoint:      fmt.Sprintf("%s:%d", d.Hostname, cfg.WireGuard.Port),
		AllowedIPs:          cfg.WireGuard.Subnet,
		PersistentKeepalive: 25,
	})
	if err != nil {
		return fmt.Errorf("render peer config: %w", err)
	}
	fmt.Printf("Client config for %q (desktop %s):\n", name, showConfigDesktop)
	return wireguard.PrintQR(peerCfg)
}

// ---- wireguard set-local-peer ----

var wireguardSetLocalPeerCmd = &cobra.Command{
	Use:   "set-local-peer <name>",
	Short: "Mark a peer as the WireGuard peer for this machine",
	Long: `set-local-peer records which peer in config.yaml belongs to this machine.
The named peer's private key must exist at ~/.ai-desktops/peers/<name>.key.
Once set, 'ai-desktops wireguard connect' and 'ai-desktops ssh' will use this
peer automatically — no manual wg-quick invocation needed.`,
	Args: cobra.ExactArgs(1),
	RunE: runWireGuardSetLocalPeer,
}

func runWireGuardSetLocalPeer(_ *cobra.Command, args []string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled; run 'ai-desktops wireguard init' first")
	}
	name := args[0]

	var found bool
	for _, p := range cfg.WireGuard.Peers {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("peer %q not found in config; run 'ai-desktops wireguard add-peer %s' first", name, name)
	}

	if _, err := loadPeerKey(name); err != nil {
		return fmt.Errorf("private key for peer %q not found on this machine: %w", name, err)
	}

	cfg.WireGuard.LocalPeer = name
	if err := cfg.Save(cfgFile); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Local peer set to %q. Run 'ai-desktops wireguard connect <desktop-id>' to connect.\n", name)
	return nil
}

// ---- wireguard connect ----

var wireguardConnectCmd = &cobra.Command{
	Use:   "connect <desktop-id>",
	Short: "Configure and bring up the WireGuard tunnel to a desktop",
	Long: `connect fetches the desktop's WireGuard server key, writes the local client
config to ~/.ai-desktops/wg-aidesktops.conf, and brings up the tunnel using
wg-quick (if the interface is not running) or wg set (if already running).

Requires sudo for wg-quick/wg/ip. Configure sudoers with NOPASSWD for these
commands to avoid a password prompt:
  %sudo ALL=(ALL) NOPASSWD: /usr/bin/wg, /usr/bin/wg-quick, /usr/sbin/ip`,
	Args: cobra.ExactArgs(1),
	RunE: runWireGuardConnect,
}

func runWireGuardConnect(_ *cobra.Command, args []string) error {
	return connectWireGuard(args[0])
}

// connectWireGuard is the shared implementation used by both 'wireguard connect'
// and the automatic pre-SSH connection.
func connectWireGuard(desktopID string) error {
	if !cfg.WireGuard.Enabled {
		return fmt.Errorf("WireGuard is not enabled in config")
	}

	// Resolve the local peer.
	peerName, peer, privKey, err := resolveLocalPeer()
	if err != nil {
		return err
	}

	// Fetch the desktop record.
	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, desktopID)
	if err != nil {
		if isNotFound(err) {
			return fmt.Errorf("desktop %q not found", desktopID)
		}
		return err
	}

	// Fetch server private key from SSM and derive the public key.
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	serverPrivKey, err := awsx.GetSecret(ctx, awsCfg, wireGuardSSMKeyPath(desktopID))
	if err != nil {
		return fmt.Errorf("fetch server key from SSM: %w", err)
	}
	serverPubKey, err := wireguard.PublicKey(serverPrivKey)
	if err != nil {
		return fmt.Errorf("derive server public key: %w", err)
	}

	endpoint := fmt.Sprintf("%s:%d", d.Hostname, cfg.WireGuard.Port)
	peerCfgIn := wireguard.PeerConfigInput{
		PeerPrivateKey:      privKey,
		PeerIP:              peer.AllowedIP,
		ServerPublicKey:     serverPubKey,
		ServerEndpoint:      endpoint,
		AllowedIPs:          cfg.WireGuard.Subnet,
		PersistentKeepalive: 25,
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	mgr := wireguard.NewLocalManager(cfg.WireGuard.Interface, filepath.Join(home, ".ai-desktops"))

	if mgr.IsUp() {
		fmt.Printf("Interface %s is already up — adding peer for %s...\n", cfg.WireGuard.Interface, desktopID)
		if err := mgr.AddPeer(serverPubKey, endpoint, peer.AllowedIP, cfg.WireGuard.Subnet); err != nil {
			return fmt.Errorf("add peer: %w", err)
		}
	} else {
		fmt.Printf("Bringing up %s for peer %q...\n", cfg.WireGuard.Interface, peerName)
		if err := mgr.Up(peerCfgIn); err != nil {
			return fmt.Errorf("bring up WireGuard interface: %w\n"+
				"Tip: ensure sudo NOPASSWD is configured for wg-quick, wg, and ip.", err)
		}
	}

	vpnIP, _ := wireguard.ServerIP(cfg.WireGuard.Subnet)
	if vpnIP != "" {
		fmt.Printf("Connected. SSH via VPN: ssh ubuntu@%s\n", vpnIP)
	} else {
		fmt.Printf("Connected. SSH is now available: ssh ubuntu@%s\n", d.Hostname)
	}
	return nil
}

// resolveLocalPeer finds the local peer name, peer record, and private key.
// It uses cfg.WireGuard.LocalPeer if set, otherwise looks for any peer with
// a key file on disk. Errors if none or more than one candidate exists.
func resolveLocalPeer() (string, *config.WireGuardPeer, string, error) {
	name := cfg.WireGuard.LocalPeer

	if name == "" {
		// Auto-detect: find peers with a key on disk.
		var candidates []string
		for _, p := range cfg.WireGuard.Peers {
			if keyPath, err := peerKeyPath(p.Name); err == nil {
				if _, err := os.Stat(keyPath); err == nil {
					candidates = append(candidates, p.Name)
				}
			}
		}
		switch len(candidates) {
		case 0:
			return "", nil, "", fmt.Errorf("no local peer configured and no peer key found on disk\n" +
				"Run 'ai-desktops wireguard add-peer <name>' to create a peer for this machine,\n" +
				"then 'ai-desktops wireguard set-local-peer <name>' to mark it as local.")
		case 1:
			name = candidates[0]
		default:
			return "", nil, "", fmt.Errorf("multiple peer keys found on this machine (%v); "+
				"run 'ai-desktops wireguard set-local-peer <name>' to specify which one to use", candidates)
		}
	}

	var peer *config.WireGuardPeer
	for i := range cfg.WireGuard.Peers {
		if cfg.WireGuard.Peers[i].Name == name {
			peer = &cfg.WireGuard.Peers[i]
			break
		}
	}
	if peer == nil {
		return "", nil, "", fmt.Errorf("peer %q (local_peer) not found in config", name)
	}

	privKey, err := loadPeerKey(name)
	if err != nil {
		return "", nil, "", fmt.Errorf("load private key for peer %q: %w", name, err)
	}

	return name, peer, privKey, nil
}

// ---- helpers ----

// peerKeyPath returns the filesystem path for a named peer's private key.
func peerKeyPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ai-desktops", "peers", name+".key"), nil
}

// savePeerKey writes privKey to ~/.ai-desktops/peers/<name>.key (mode 0600).
func savePeerKey(name, privKey string) (string, error) {
	p, err := peerKeyPath(name)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(privKey+"\n"), 0600); err != nil {
		return "", err
	}
	return p, nil
}

// loadPeerKey reads the private key for a named peer from disk.
func loadPeerKey(name string) (string, error) {
	p, err := peerKeyPath(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("key file not found at %s; was this peer created on a different machine?", p)
		}
		return "", err
	}
	key := string(data)
	// Trim any trailing newline added by savePeerKey.
	for len(key) > 0 && (key[len(key)-1] == '\n' || key[len(key)-1] == '\r') {
		key = key[:len(key)-1]
	}
	return key, nil
}

// wireGuardSSMKeyPath returns the SSM Parameter Store path for a desktop's
// WireGuard server private key.
func wireGuardSSMKeyPath(desktopID string) string {
	return fmt.Sprintf("/ai-desktops/%s/wireguard/server-key", desktopID)
}

// isNotFound reports whether err is a store.ErrNotFound.
func isNotFound(err error) bool {
	return err != nil && err.Error() == store.ErrNotFound.Error()
}

// sshRun runs `ssh` with the given arguments and returns combined output and any error.
func sshRun(args ...string) (string, error) {
	cmd := exec.Command("ssh", args...) //nolint:gosec
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runSSHCommand runs a command on a remote host via SSH (fire-and-forget style).
func runSSHCommand(host, keyPath, command string) error {
	args := []string{
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"ubuntu@" + host,
		command,
	}
	out, err := sshRun(args...)
	if err != nil {
		return fmt.Errorf("ssh %s: %w\n%s", host, err, out)
	}
	return nil
}

func init() {
	wireguardCmd.AddCommand(wireguardInitCmd)
	wireguardCmd.AddCommand(wireguardAddPeerCmd)
	wireguardCmd.AddCommand(wireguardListPeersCmd)
	wireguardCmd.AddCommand(wireguardRemovePeerCmd)
	wireguardCmd.AddCommand(wireguardShowConfigCmd)
	wireguardCmd.AddCommand(wireguardSetLocalPeerCmd)
	wireguardCmd.AddCommand(wireguardConnectCmd)
	rootCmd.AddCommand(wireguardCmd)
}
