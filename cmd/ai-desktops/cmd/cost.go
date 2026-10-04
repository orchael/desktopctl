package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
)

func estimateHourlyCostLabel(ctx context.Context, awsCfg aws.Config, region, instanceType, marketType string) string {
	estimate, err := awsx.EstimateInstanceHourlyCost(ctx, awsCfg, region, instanceType, marketType)
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	return formatHourlyCost(estimate)
}

func formatHourlyCost(estimate *awsx.InstanceCostEstimate) string {
	if estimate == nil {
		return ""
	}
	if estimate.MarketType == store.MarketSpot {
		return fmt.Sprintf("$%.4f/hr spot (%s)", estimate.USDPerHour, estimate.Source)
	}
	return fmt.Sprintf("$%.4f/hr on-demand (%s)", estimate.USDPerHour, estimate.Source)
}
