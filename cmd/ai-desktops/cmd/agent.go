package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/orchael/desktopctl/internal/agent"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/orchael/desktopctl/internal/tunnel"
	"github.com/spf13/cobra"
)

var (
	agentProvider   string
	agentRepo       string
	agentTunnelMode string
	agentTrustHost  bool
)

var agentCmd = &cobra.Command{
	Use:   "agent <desktop-id> <subcommand>",
	Short: "Control AI agent sessions on a desktop via bridge tunnel",
}

var agentStatusCmd = &cobra.Command{
	Use:   "status <desktop-id>",
	Short: "Show bridge status on the desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentStatus,
}

var agentProvidersCmd = &cobra.Command{
	Use:   "providers <desktop-id>",
	Short: "List available agent providers",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentProviders,
}

var agentStartCmd = &cobra.Command{
	Use:   "start <desktop-id>",
	Short: "Start an agent session",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentStart,
}

var agentStopSessionCmd = &cobra.Command{
	Use:   "stop <desktop-id> <session-id>",
	Short: "Stop a running agent session",
	Args:  cobra.ExactArgs(2),
	RunE:  runAgentStop,
}

var agentListSessionsCmd = &cobra.Command{
	Use:   "sessions <desktop-id>",
	Short: "List agent sessions",
	Args:  cobra.ExactArgs(1),
	RunE:  runAgentSessions,
}

func init() {
	agentCmd.AddCommand(agentStatusCmd)
	agentCmd.AddCommand(agentProvidersCmd)
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentStopSessionCmd)
	agentCmd.AddCommand(agentListSessionsCmd)

	agentStartCmd.Flags().StringVar(&agentProvider, "provider", "", "agent provider (codex, claude, gemini)")
	agentStartCmd.Flags().StringVar(&agentRepo, "repo", "", "repository to run in")
	agentCmd.PersistentFlags().StringVar(&agentTunnelMode, "tunnel", "ssm", "tunnel mode (ssm|ssh)")
	agentCmd.PersistentFlags().BoolVar(&agentTrustHost, "trust-host", false,
		"disable SSH host key verification for the tunnel (use for freshly provisioned desktops not yet in known_hosts)")

	rootCmd.AddCommand(agentCmd)
}

func getDesktopAndTunnel(ctx context.Context, id string) (*store.Desktop, *agent.Client, func(), error) {
	switch tunnel.Mode(agentTunnelMode) {
	case tunnel.ModeSSM:
		if err := requireTools("aws"); err != nil {
			return nil, nil, nil, err
		}
	case tunnel.ModeSSH:
		if err := requireTools("ssh"); err != nil {
			return nil, nil, nil, err
		}
	}

	s, err := openStore(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open store: %w", err)
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, nil, fmt.Errorf("desktop %q not found", id)
		}
		return nil, nil, nil, err
	}

	localPort, err := tunnel.EphemeralPort()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ephemeral port: %w", err)
	}

	tcfg := &tunnel.Config{
		DesktopID:  id,
		InstanceID: d.InstanceID,
		Hostname:   d.Hostname,
		SSHKeyPath: cfg.Desktop.SSHKeyPath,
		Region:     cfg.AWS.Region,
		Profile:    cfg.AWS.Profile,
		BridgePort: cfg.Agent.BridgePort,
		TrustHost:  agentTrustHost || cfg.Agent.TrustHost,
	}

	var tunnelProc *exec.Cmd

	mode := tunnel.Mode(agentTunnelMode)
	switch mode {
	case tunnel.ModeSSM:
		tunnelProc = tunnel.SSMCommand(tcfg, localPort)
		if err := tunnelProc.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("start SSM tunnel: %w", err)
		}
	case tunnel.ModeSSH:
		tunnelProc = tunnel.SSHCommand(tcfg, localPort)
		if err := tunnelProc.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("start SSH tunnel: %w", err)
		}
	default:
		return nil, nil, nil, fmt.Errorf("unknown tunnel mode %q (use ssm or ssh)", agentTunnelMode)
	}

	cleanup := func() {
		if tunnelProc != nil && tunnelProc.Process != nil {
			_ = tunnelProc.Process.Kill()
			_ = tunnelProc.Wait()
		}
	}

	// Wait until the local port is actually accepting connections before
	// sending the first bridge request — avoids "connection refused" races.
	if err := tunnel.WaitForPort(localPort, 30*time.Second); err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("tunnel not ready on port %d: %w", localPort, err)
	}

	bridgeURL := tunnel.BridgeURL(localPort)
	c := agent.NewClient(bridgeURL)

	return d, c, cleanup, nil
}

func runAgentStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	_, c, cleanup, err := getDesktopAndTunnel(ctx, args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	resp, err := c.Status(ctx)
	if err != nil {
		return fmt.Errorf("bridge status: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(resp)
	}
	fmt.Printf("Bridge status: %v\n", resp)
	return nil
}

func runAgentProviders(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	_, c, cleanup, err := getDesktopAndTunnel(ctx, args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	providers, err := c.Providers(ctx)
	if err != nil {
		return fmt.Errorf("bridge providers: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(providers)
	}
	for _, p := range providers {
		avail := "unavailable"
		if p.Available {
			avail = "available"
		}
		fmt.Printf("  %s: %s\n", p.Name, avail)
	}
	return nil
}

func runAgentStart(cmd *cobra.Command, args []string) error {
	if agentProvider == "" {
		return fmt.Errorf("--provider is required")
	}
	ctx := context.Background()

	_, c, cleanup, err := getDesktopAndTunnel(ctx, args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	sess, err := c.StartSession(ctx, agentProvider, agentRepo)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(sess)
	}
	fmt.Printf("Session ID : %s\n", sess.ID)
	fmt.Printf("Provider   : %s\n", sess.Provider)
	fmt.Printf("Status     : %s\n", sess.Status)
	return nil
}

func runAgentStop(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	sessionID := args[1]

	_, c, cleanup, err := getDesktopAndTunnel(ctx, args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	if err := c.StopSession(ctx, sessionID); err != nil {
		return fmt.Errorf("stop session: %w", err)
	}
	fmt.Printf("Session %s stopped.\n", sessionID)
	return nil
}

func runAgentSessions(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	_, c, cleanup, err := getDesktopAndTunnel(ctx, args[0])
	if err != nil {
		return err
	}
	defer cleanup()

	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(sessions)
	}
	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return nil
	}
	for _, sess := range sessions {
		fmt.Printf("  %s  provider=%s  status=%s\n", sess.ID, sess.Provider, sess.Status)
	}
	return nil
}
