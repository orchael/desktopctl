package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/orchael/ai-desktops/internal/tunnel"
)

var sshTunnelMode string

var sshCmd = &cobra.Command{
	Use:   "ssh <desktop-id>",
	Short: "Open an SSH session to the desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runSSH,
}

func init() {
	sshCmd.Flags().StringVar(&sshTunnelMode, "tunnel", "ssm", "tunnel mode (ssm|ssh) - ssm is more reliable when SSH hostname doesn't resolve")
	rootCmd.AddCommand(sshCmd)
}

func runSSH(cmd *cobra.Command, args []string) error {
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

	// Use SSM port-forward tunnel to reach the desktop via SSH
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
		BridgePort: 22, // Forward SSH port (22) on the desktop to local tunnel
		Mode:       tunnel.ModeSSM,
	}

	if sshTunnelMode == "ssh" {
		tcfg.Mode = tunnel.ModeSSH
		tcfg.Hostname = d.SSHTarget
	}

	var tunnelProc *exec.Cmd
	switch tcfg.Mode {
	case tunnel.ModeSSM:
		tunnelProc = tunnel.SSMCommand(tcfg, localPort)
	case tunnel.ModeSSH:
		tunnelProc = tunnel.SSHCommand(tcfg, localPort)
	default:
		return fmt.Errorf("unknown tunnel mode %q", sshTunnelMode)
	}

	// Capture tunnel output for debugging
	tunnelProc.Stdout = os.Stderr
	tunnelProc.Stderr = os.Stderr

	if err := tunnelProc.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	defer tunnelProc.Process.Kill()

	// Wait for tunnel to establish
	time.Sleep(2 * time.Second)

	// Now SSH to localhost via the tunnel
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found on PATH: %w", err)
	}

	sshArgs := []string{
		"-i", cfg.Desktop.SSHKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-p", fmt.Sprintf("%d", localPort),
		fmt.Sprintf("ubuntu@127.0.0.1"),
	}

	sshCmd := exec.Command(sshPath, sshArgs...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	// Handle Ctrl+C gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		tunnelProc.Process.Kill()
		sshCmd.Process.Kill()
		os.Exit(0)
	}()

	err = sshCmd.Run()
	tunnelProc.Process.Kill()

	// If SSH fails and the instance doesn't have a key pair, suggest SSM shell
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nSSH connection failed. If the instance doesn't have an SSH key pair,\n")
		fmt.Fprintf(os.Stderr, "use AWS Systems Manager Session Manager instead:\n\n")
		fmt.Fprintf(os.Stderr, "  aws ssm start-session --target %s --region %s\n\n", d.InstanceID, cfg.AWS.Region)
		if cfg.AWS.Profile != "" {
			fmt.Fprintf(os.Stderr, "  Or with your AWS profile:\n")
			fmt.Fprintf(os.Stderr, "  aws ssm start-session --target %s --region %s --profile %s\n\n", d.InstanceID, cfg.AWS.Region, cfg.AWS.Profile)
		}
		return err
	}

	return nil
}
