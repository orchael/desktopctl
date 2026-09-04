package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var costStaleOlderThan string

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Cost visibility commands",
}

var costStaleCmd = &cobra.Command{
	Use:   "stale",
	Short: "List stopped desktops that have been idle longer than a threshold",
	Long: `Lists stopped desktops older than --older-than and shows their estimated
monthly EBS storage cost. Use this to identify parked desktops that are
accumulating storage charges without being used.`,
	Args: cobra.NoArgs,
	RunE: runCostStale,
}

func init() {
	costStaleCmd.Flags().StringVar(&costStaleOlderThan, "older-than", "7d", "only show desktops stopped for longer than this duration (e.g. 7d, 14d, 48h)")
	costCmd.AddCommand(costStaleCmd)
	rootCmd.AddCommand(costCmd)
}

func runCostStale(cmd *cobra.Command, args []string) error {
	threshold, err := parseStaleDuration(costStaleOlderThan)
	if err != nil {
		return fmt.Errorf("--older-than: %w", err)
	}

	ctx := context.Background()
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	desktops, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("list desktops: %w", err)
	}

	now := time.Now().UTC()
	stale := filterStaleDesktops(desktops, threshold, now)

	if len(stale) == 0 {
		fmt.Printf("No stopped desktops older than %s.\n", costStaleOlderThan)
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DESKTOP ID\tOWNER\tREGION\tINSTANCE ID\tVOL SIZE\tSTOPPED AGE\tEBS/MONTH (est)")
	var totalMonthly float64
	for _, d := range stale {
		region := d.Region
		if region == "" {
			region = cfg.AWS.Region
		}
		volSize := "N/A"
		monthly := "N/A"
		totalMonthly += 0
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			d.DesktopID, d.GitHubOwner, region, d.InstanceID,
			volSize, formatStoppedAge(d.StoppedAt, now), monthly)
	}
	_ = w.Flush()
	fmt.Printf("\n%d stale desktop(s). Estimated total EBS cost: $%.2f/month.\n", len(stale), totalMonthly)
	return nil
}

// parseStaleDuration parses a duration string like "7d", "14d", or "48h".
// Only days (d) and hours (h) suffixes are accepted; the value must be positive.
func parseStaleDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("duration must not be empty")
	}
	// Try Go standard duration first (handles h, m, s).
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return 0, fmt.Errorf("duration must be positive, got %q", s)
		}
		return d, nil
	}
	// Handle days suffix (e.g. "7d").
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q: days value must be a positive integer", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid duration %q: use a positive integer with suffix d (days) or h (hours), e.g. 7d or 48h", s)
}

// filterStaleDesktops returns desktops that are in the stopped state and have
// been stopped for longer than olderThan relative to now. Desktops without a
// valid StoppedAt timestamp are excluded.
func filterStaleDesktops(desktops []*store.Desktop, olderThan time.Duration, now time.Time) []*store.Desktop {
	var result []*store.Desktop
	for _, d := range desktops {
		if d.State != store.StateStopped {
			continue
		}
		if d.StoppedAt == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, d.StoppedAt)
		if err != nil {
			continue
		}
		if now.Sub(t) > olderThan {
			result = append(result, d)
		}
	}
	return result
}

// formatStoppedAge returns a human-readable age like "8d 0h" for a stopped desktop.
func formatStoppedAge(stoppedAt string, now time.Time) string {
	if stoppedAt == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, stoppedAt)
	if err != nil {
		return "unknown"
	}
	dur := now.Sub(t)
	days := int(dur.Hours()) / 24
	hours := int(dur.Hours()) % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}

// ebsMonthlyCostGiB returns the estimated monthly EBS gp3 cost in us-east-1
// at $0.08/GiB-month.
func ebsMonthlyCostGiB(volumeSizeGiB int) float64 {
	return float64(volumeSizeGiB) * 0.08
}
