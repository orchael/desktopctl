package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/dnszone"
	"github.com/spf13/cobra"
)

var dnsSetupZone string

var dnsCmd = &cobra.Command{Use: "dns", Short: "Set up and check desktop DNS delegation"}

var dnsSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Create or reuse a public Route 53 zone and print its name servers",
	Args:  cobra.NoArgs,
	RunE:  runDNSSetup,
}

var dnsCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Verify public NS delegation matches the configured Route 53 zone",
	Args:  cobra.NoArgs,
	RunE:  runDNSCheck,
}

func init() {
	dnsSetupCmd.Flags().StringVar(&dnsSetupZone, "zone", "", "desktop DNS zone, for example desktops.example.com")
	dnsCmd.AddCommand(dnsSetupCmd, dnsCheckCmd)
	rootCmd.AddCommand(dnsCmd)
}

func route53Client(ctx context.Context, region string) (*route53.Client, error) {
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return nil, err
	}
	return route53.NewFromConfig(awsCfg), nil
}

func configuredDNSZone() (string, error) {
	zone, err := cfg.DNSZone()
	if err != nil {
		return "", err
	}
	return dnszone.Normalize(zone)
}

func runDNSSetup(_ *cobra.Command, _ []string) error {
	ctx := context.Background()
	name := strings.TrimSpace(dnsSetupZone)
	if name == "" {
		name = cfg.DNS.Zone
	}
	if name == "" {
		return fmt.Errorf("set --zone to the domain you control, for example desktops.example.com")
	}
	name, err := dnszone.Normalize(name)
	if err != nil {
		return err
	}
	client, err := route53Client(ctx, cfg.AWS.Region)
	if err != nil {
		return err
	}
	zone, created, err := dnszone.Ensure(ctx, client, name)
	if err != nil {
		return err
	}
	path := cfgFile
	if path == "" {
		path, err = config.DefaultPath()
		if err != nil {
			return err
		}
	}
	updated := *cfg
	updated.DNS = config.DNSConfig{Zone: zone.Name, HostedZoneID: zone.ID}
	if err := updated.Save(path); err != nil {
		return fmt.Errorf("save DNS zone to config %q: %w", path, err)
	}
	fmt.Printf("Config: %s\n", path)
	printDNSZoneInstructions(zone, created)
	return nil
}

func printDNSZoneInstructions(zone dnszone.Zone, created bool) {
	verb := "Reused"
	if created {
		verb = "Created"
	}
	fmt.Printf("%s public Route 53 zone %s (%s)\n", verb, zone.Name, zone.ID)
	if strings.Count(zone.Name, ".") == 1 {
		fmt.Printf("At the registrar for %s, set the domain name servers to:\n", zone.Name)
	} else {
		fmt.Printf("At the DNS provider for %s, add an NS record named %s (full name %s) with these values:\n", parentDomain(zone.Name), strings.SplitN(zone.Name, ".", 2)[0], zone.Name)
	}
	for _, ns := range zone.NameServers {
		fmt.Printf("  %s\n", ns)
	}
	fmt.Println("After publishing the NS record, run desktopctl dns check with this config.")
}

func parentDomain(name string) string {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return name
}

func checkDNSDelegation(ctx context.Context, name, id, region string) error {
	client, err := route53Client(ctx, region)
	if err != nil {
		return err
	}
	zone, err := dnszone.Get(ctx, client, name, id)
	if err != nil {
		return err
	}
	if err := dnszone.VerifyDelegation(ctx, zone, net.DefaultResolver.LookupNS); err != nil {
		return fmt.Errorf("desktop DNS is not ready: %w", err)
	}
	return nil
}

func requireDesktopDNS(ctx context.Context, region string) error {
	name, err := configuredDNSZone()
	if err != nil {
		return err
	}
	return checkDNSDelegation(ctx, name, cfg.DNS.HostedZoneID, region)
}

func runDNSCheck(_ *cobra.Command, _ []string) error {
	ctx := context.Background()
	name, err := configuredDNSZone()
	if err != nil {
		return err
	}
	if err := checkDNSDelegation(ctx, name, cfg.DNS.HostedZoneID, cfg.AWS.Region); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "DNS delegation ready: %s\n", name)
	return nil
}
