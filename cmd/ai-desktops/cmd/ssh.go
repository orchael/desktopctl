package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/orchael/ai-desktops/internal/tunnel"
	"github.com/orchael/ai-desktops/internal/wireguard"
	"github.com/spf13/cobra"
)

var sshTunnelMode string

var sshCmd = &cobra.Command{
	Use:   "ssh <desktop-id>",
	Short: "Open an SSH session to the desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runSSH,
}

func init() {
	sshCmd.Flags().StringVar(&sshTunnelMode, "tunnel", "", "tunnel mode (ssm|ssh) - use for connections that can't reach hostname directly (e.g., corporate networks)")
	rootCmd.AddCommand(sshCmd)
}

func runSSH(cmd *cobra.Command, args []string) error {
	tools := []string{"ssh"}
	if sshTunnelMode == "ssm" {
		tools = append(tools, "aws")
	}
	if err := requireTools(tools...); err != nil {
		return err
	}

	ctx := context.Background()
	id := args[0]

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	// If WireGuard is enabled and no tunnel mode specified, auto-connect the VPN
	// and SSH via the desktop's VPN IP (the tunnel only routes VPN-subnet traffic).
	if sshTunnelMode == "" && cfg.WireGuard.Enabled {
		if err := connectWireGuard(id); err != nil {
			fmt.Fprintf(os.Stderr, "WireGuard auto-connect failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "Falling back — SSH may fail if VPN is required.\n")
			fmt.Fprintf(os.Stderr, "Run 'ai-desktops wireguard connect %s' to retry manually.\n\n", id)
		} else {
			vpnIP, err := wireguard.ServerIP(cfg.WireGuard.Subnet)
			if err == nil {
				d.SSHTarget = "ubuntu@" + vpnIP
				fmt.Fprintf(os.Stderr, "Connecting via VPN: ssh %s\n", d.SSHTarget)
				// Probe port 22 through the tunnel to give a fast, clear error if
				// the VPN handshake hasn't completed yet.
				conn, dialErr := net.DialTimeout("tcp", vpnIP+":22", 10*time.Second)
				if dialErr != nil {
					fmt.Fprintf(os.Stderr, "VPN tunnel not routing yet (%v).\n", dialErr)
					fmt.Fprintf(os.Stderr, "The desktop's WireGuard service may still be starting. Try again in a moment.\n")
					fmt.Fprintf(os.Stderr, "To diagnose: sudo wg show %s\n", cfg.WireGuard.Interface)
					return fmt.Errorf("VPN not reachable: %w", dialErr)
				}
				conn.Close()
			}
		}
	}

	// If no tunnel mode specified, SSH directly to the hostname
	if sshTunnelMode == "" {
		sshArgs := []string{
			"-i", cfg.Desktop.SSHKeyPath,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			d.SSHTarget,
		}
		sshProc := exec.Command("ssh", sshArgs...)
		sshProc.Stdin = os.Stdin
		sshProc.Stdout = os.Stdout
		sshProc.Stderr = os.Stderr

		// Handle Ctrl+C gracefully
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigChan
			if sshProc.Process != nil {
				sshProc.Process.Kill()
			}
			os.Exit(130)
		}()

		return sshProc.Run()
	}

	// Setup tunnel for --tunnel ssm or --tunnel ssh
	localPort, err := tunnel.EphemeralPort()
	if err != nil {
		return fmt.Errorf("find local port: %w", err)
	}

	tcfg := &tunnel.Config{
		DesktopID:  d.DesktopID,
		InstanceID: d.InstanceID,
		Hostname:   "127.0.0.1",
		SSHKeyPath: cfg.Desktop.SSHKeyPath,
		SSHUser:    "ubuntu",
		Region:     cfg.AWS.Region,
		Profile:    cfg.AWS.Profile,
		BridgePort: 22,
		Mode:       tunnel.ModeSSM,
	}

	if sshTunnelMode == "ssh" {
		tcfg.Mode = tunnel.ModeSSH
		tcfg.Hostname = d.Hostname
	} else if sshTunnelMode != "ssm" {
		return fmt.Errorf("unknown tunnel mode %q (use 'ssm' or 'ssh')", sshTunnelMode)
	}

	var tunnelProc *exec.Cmd
	switch tcfg.Mode {
	case tunnel.ModeSSM:
		tunnelProc = tunnel.SSMCommand(tcfg, localPort)
	case tunnel.ModeSSH:
		tunnelProc = tunnel.SSHCommand(tcfg, localPort)
	}

	tunnelProc.Stdout = os.Stderr
	tunnelProc.Stderr = os.Stderr

	if err := tunnelProc.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	defer tunnelProc.Process.Kill()

	// Poll for tunnel readiness
	tunnelReady := false
	for i := 0; i < 100; i++ {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
		if err == nil {
			conn.Close()
			tunnelReady = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tunnelReady {
		return fmt.Errorf("tunnel port %d failed to become reachable after 10 seconds", localPort)
	}

	sshArgs := []string{
		"-i", cfg.Desktop.SSHKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-p", fmt.Sprintf("%d", localPort),
		"ubuntu@127.0.0.1",
	}

	sshProc := exec.Command("ssh", sshArgs...)
	sshProc.Stdin = os.Stdin
	sshProc.Stdout = os.Stdout
	sshProc.Stderr = os.Stderr

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		if tunnelProc.Process != nil {
			tunnelProc.Process.Kill()
		}
		if sshProc.Process != nil {
			sshProc.Process.Kill()
		}
		os.Exit(130)
	}()

	err = sshProc.Run()
	tunnelProc.Process.Kill()

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nSSH connection failed (likely SSH key auth issue). You can use an interactive SSM shell instead:\n\n")
		fmt.Fprintf(os.Stderr, "  aws ssm start-session --target %s --region %s\n\n", d.InstanceID, cfg.AWS.Region)
		if cfg.AWS.Profile != "" {
			fmt.Fprintf(os.Stderr, "  Or with your AWS profile:\n")
			fmt.Fprintf(os.Stderr, "  aws ssm start-session --target %s --region %s --profile %s\n\n", d.InstanceID, cfg.AWS.Region, cfg.AWS.Profile)
		}
		return err
	}

	return nil
}
