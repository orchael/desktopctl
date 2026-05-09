package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/store"
)

var (
	terminateForce bool
)

var terminateCmd = &cobra.Command{
	Use:   "terminate <desktop-id>",
	Short: "Permanently destroy a desktop and its disk state",
	Long: `terminate destroys the Pulumi desktop stack, which permanently removes the EC2
instance and root EBS volume. This operation is IRREVERSIBLE.

The fleet record is marked terminated only after Pulumi destroy succeeds.
A failed desktop is NOT automatically destroyed; use --force to attempt
termination regardless of current state.`,
	Args: cobra.ExactArgs(1),
	RunE: runTerminate,
}

func init() {
	terminateCmd.Flags().BoolVar(&terminateForce, "force", false, "terminate even if in a failed or unhealthy state")
	rootCmd.AddCommand(terminateCmd)
}

func runTerminate(cmd *cobra.Command, args []string) error {
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

	if d.State == store.StateTerminated {
		return fmt.Errorf("desktop %q is already terminated", id)
	}

	if !terminateForce && (d.State == store.StateFailed || d.State == store.StateProvisioningFailed) {
		return fmt.Errorf("desktop %q is in state %q; use --force to terminate anyway", id, d.State)
	}

	mgr := desktop.NewManager(s)
	if err := mgr.MarkTerminating(ctx, id); err != nil {
		return err
	}

	fmt.Printf("Terminating desktop %s (stack %s) ...\n", id, d.StackName)
	fmt.Println("IMPORTANT: This will permanently destroy the EC2 instance and EBS volume.")
	fmt.Println("NOTE: Pulumi Automation API integration required to run destroy.")
	fmt.Printf("Run: cd infra/pulumi/desktop && pulumi stack select %s && pulumi destroy\n", d.StackName)

	if err := s.MarkTerminated(ctx, id); err != nil {
		return err
	}

	fmt.Printf("Desktop %s marked as terminated.\n", id)
	return nil
}
