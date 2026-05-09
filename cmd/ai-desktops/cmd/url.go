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

var urlCmd = &cobra.Command{
	Use:   "url <desktop-id>",
	Short: "Print the browser access URL for a desktop",
	Args:  cobra.ExactArgs(1),
	RunE:  runURL,
}

func init() {
	rootCmd.AddCommand(urlCmd)
}

func runURL(cmd *cobra.Command, args []string) error {
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
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"url": d.NoVNCURL})
	}

	fmt.Println(d.NoVNCURL)
	return nil
}
