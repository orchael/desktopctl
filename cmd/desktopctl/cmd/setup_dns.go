package cmd

import (
	"context"
	"fmt"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/dnszone"
)

// ensureSetupDNS runs after credential setup and before writing the config, so
// the saved zone ID always identifies the hosted zone selected by Route 53.
func ensureSetupDNS(ctx context.Context, a *setupAnswers, updated *config.Config) error {
	if a.DNSZone == "" {
		updated.DNS = config.DNSConfig{}
		return nil
	}
	awsCfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion(a.AWSRegion),
		awscfg.WithSharedConfigProfile(a.AWSProfile),
	)
	if err != nil {
		return fmt.Errorf("load AWS config for desktop DNS: %w", err)
	}
	zone, created, err := dnszone.Ensure(ctx, route53.NewFromConfig(awsCfg), a.DNSZone)
	if err != nil {
		return fmt.Errorf("set up desktop DNS zone: %w", err)
	}
	updated.DNS = config.DNSConfig{Zone: zone.Name, HostedZoneID: zone.ID}
	printDNSZoneInstructions(zone, created)
	return nil
}
