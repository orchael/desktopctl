package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status <desktop-id>",
	Short: "Show the status of a desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
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

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(d)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}

	fmt.Printf("Desktop ID   : %s\n", d.DesktopID)
	fmt.Printf("State        : %s\n", d.State)
	fmt.Printf("Owner        : %s\n", d.GitHubOwner)
	fmt.Printf("Region       : %s\n", region)
	fmt.Printf("Hostname     : %s\n", d.Hostname)
	fmt.Printf("Desktop URL  : %s\n", d.NoVNCURL)
	if liveURL := fetchNoVNCDesktopURL(d); liveURL != "" {
		fmt.Printf("NoVNC URL    : %s\n", liveURL)
	}
	fmt.Printf("SSH target   : %s\n", d.SSHTarget)
	fmt.Printf("Instance ID  : %s\n", d.InstanceID)
	if d.AMIID != "" {
		fmt.Printf("AMI ID       : %s\n", d.AMIID)
	}
	fmt.Printf("Pulumi stack : %s\n", d.StackName)
	fmt.Printf("Readiness    : %s\n", d.Readiness)
	fmt.Printf("Created      : %s\n", d.CreatedAt)
	fmt.Printf("Updated      : %s\n", d.UpdatedAt)
	if d.FailurePhase != "" {
		fmt.Printf("Failure phase: %s\n", d.FailurePhase)
		fmt.Printf("Failure msg  : %s\n", d.FailureMsg)
	}
	return nil
}

// fetchNoVNCDesktopURL SSHes into the desktop and runs novnc-desktop-url to
// retrieve the live URL. Returns empty string on any error so callers can
// treat it as optional.
func fetchNoVNCDesktopURL(d *store.Desktop) string {
	if d.SSHTarget == "" || cfg.Desktop.SSHKeyPath == "" {
		return ""
	}
	if d.State != store.StateReady && d.State != store.StateUnhealthy {
		return ""
	}
	out, _ := exec.Command("ssh",
		"-i", cfg.Desktop.SSHKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		d.SSHTarget,
		"novnc-desktop-url",
	).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Desktop URL :"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}
