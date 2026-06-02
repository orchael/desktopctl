package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var purgeDryRun bool

var purgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Delete terminated desktop records",
	Long:  "purge deletes fleet metadata records for desktops that have already been terminated.",
	Args:  cobra.NoArgs,
	RunE:  runPurge,
}

func init() {
	purgeCmd.Flags().BoolVar(&purgeDryRun, "dry-run", false, "preview terminated desktop records without deleting them")
	rootCmd.AddCommand(purgeCmd)
}

func runPurge(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	desktops, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("list desktops: %w", err)
	}
	purged, err := purgeTerminated(ctx, s, desktops, purgeDryRun)
	if err != nil {
		return err
	}
	if purgeDryRun {
		fmt.Printf("Would delete %d terminated desktop record(s).\n", purged)
		return nil
	}
	fmt.Printf("Deleted %d terminated desktop record(s).\n", purged)
	return nil
}

func purgeTerminated(ctx context.Context, s store.Store, desktops []*store.Desktop, dryRun bool) (int, error) {
	purged := 0
	for _, desktop := range desktops {
		if desktop.State != store.StateTerminated {
			continue
		}
		fmt.Println(desktop.DesktopID)
		if !dryRun {
			if err := s.Delete(ctx, desktop.DesktopID); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return purged, fmt.Errorf("delete desktop %q: %w", desktop.DesktopID, err)
			}
		}
		purged++
	}
	return purged, nil
}
