package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var listAll bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List active desktops",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "include terminated desktops")
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	desktops, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("list desktops: %w", err)
	}
	desktops = filterListedDesktops(desktops, listAll)

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(desktops)
	}

	if len(desktops) == 0 {
		fmt.Println("No desktops found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DESKTOP ID\tSTATE\tOWNER\tREGION\tHOSTNAME\tCREATED")
	for _, d := range desktops {
		region := d.Region
		if region == "" {
			region = cfg.AWS.Region
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			d.DesktopID, d.State, d.GitHubOwner, region, d.Hostname, d.CreatedAt)
	}
	return w.Flush()
}

func filterListedDesktops(desktops []*store.Desktop, includeTerminated bool) []*store.Desktop {
	if includeTerminated {
		return desktops
	}
	filtered := make([]*store.Desktop, 0, len(desktops))
	for _, desktop := range desktops {
		if desktop.State != store.StateTerminated {
			filtered = append(filtered, desktop)
		}
	}
	return filtered
}
