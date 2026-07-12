package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	printDesktopStatus(os.Stdout, d, region, fetchNoVNCDesktopURL(d))
	return nil
}

// printDesktopStatus writes the human-readable status block to w.
func printDesktopStatus(w io.Writer, d *store.Desktop, region, liveURL string) {
	fmt.Fprintf(w, "Desktop ID   : %s\n", d.DesktopID)
	fmt.Fprintf(w, "State        : %s\n", d.State)
	fmt.Fprintf(w, "Owner        : %s\n", d.GitHubOwner)
	fmt.Fprintf(w, "Region       : %s\n", region)
	fmt.Fprintf(w, "Hostname     : %s\n", d.Hostname)
	fmt.Fprintf(w, "Desktop URL  : %s\n", d.NoVNCURL)
	if liveURL != "" {
		fmt.Fprintf(w, "NoVNC URL    : %s\n", liveURL)
	}
	fmt.Fprintf(w, "SSH target   : %s\n", d.SSHTarget)
	fmt.Fprintf(w, "Instance ID  : %s\n", d.InstanceID)
	if d.AMIID != "" {
		fmt.Fprintf(w, "AMI ID       : %s\n", d.AMIID)
	}
	fmt.Fprintf(w, "Pulumi stack : %s\n", d.StackName)
	fmt.Fprintf(w, "Readiness    : %s\n", d.Readiness)
	if len(d.Repos) > 0 {
		fmt.Fprintf(w, "Repos        : %s\n", strings.Join(d.Repos, ", "))
	}
	if len(d.Secrets) > 0 {
		fmt.Fprintf(w, "Secrets      : %s\n", strings.Join(d.Secrets, ", "))
	}
	fmt.Fprintf(w, "Created      : %s\n", d.CreatedAt)
	fmt.Fprintf(w, "Updated      : %s\n", d.UpdatedAt)
	if d.FailurePhase != "" {
		fmt.Fprintf(w, "Failure phase: %s\n", d.FailurePhase)
		fmt.Fprintf(w, "Failure msg  : %s\n", d.FailureMsg)
	}
}

// parseNoVNCOutput extracts the URL from the output of novnc-desktop-url.
// The command emits a multi-line block; this picks the "Desktop URL :" line.
func parseNoVNCOutput(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "Desktop URL :"); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
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
	return parseNoVNCOutput(string(out))
}
