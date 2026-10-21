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

	"github.com/orchael/desktopctl/internal/store"
	"github.com/orchael/desktopctl/internal/tunnel"
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

func desktopSSHMode(noDNS bool, requested string) (string, error) {
	if requested != "" && requested != "ssm" && requested != "ssh" {
		return "", fmt.Errorf("unknown tunnel mode %q (use 'ssm' or 'ssh')", requested)
	}
	if noDNS {
		if requested == "ssh" {
			return "", fmt.Errorf("desktop has no DNS hostname; use an SSM tunnel")
		}
		return "ssm", nil
	}
	return requested, nil
}

func runSSH(cmd *cobra.Command, args []string) error {
	if err := requireTools("ssh"); err != nil {
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
	mode, err := desktopSSHMode(d.NoDNS, sshTunnelMode)
	if err != nil {
		return fmt.Errorf("desktop %q: %w", id, err)
	}
	if mode == "ssm" {
		if err := requireTools("aws"); err != nil {
			return err
		}
	}

	// If no tunnel mode specified, SSH directly to the hostname
	if mode == "" {
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

	if mode == "ssh" {
		tcfg.Mode = tunnel.ModeSSH
		tcfg.Hostname = d.Hostname
	} else if mode != "ssm" {
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
