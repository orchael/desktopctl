package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/store"
)

var sshCmd = &cobra.Command{
	Use:   "ssh <desktop-id>",
	Short: "Open an SSH session to the desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runSSH,
}

func init() {
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

	sshArgs := []string{"-i", cfg.Desktop.SSHKeyPath, d.SSHTarget}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found on PATH: %w", err)
	}

	// Replace the current process with ssh.
	return syscall.Exec(sshPath, append([]string{"ssh"}, sshArgs...), os.Environ())
}
