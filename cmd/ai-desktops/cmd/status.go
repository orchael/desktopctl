package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/store"
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
	fmt.Printf("noVNC URL    : %s\n", d.NoVNCURL)
	fmt.Printf("SSH target   : %s\n", d.SSHTarget)
	fmt.Printf("Instance ID  : %s\n", d.InstanceID)
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
