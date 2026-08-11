package cmd

import (
	"testing"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/store"
)

func TestFormatHourlyCost(t *testing.T) {
	tests := []struct {
		name     string
		estimate *awsx.InstanceCostEstimate
		want     string
	}{
		{
			name: "on demand",
			estimate: &awsx.InstanceCostEstimate{
				MarketType: store.MarketOnDemand,
				USDPerHour: 0.096,
				Source:     "AWS Price List",
			},
			want: "$0.0960/hr on-demand (AWS Price List)",
		},
		{
			name: "spot",
			estimate: &awsx.InstanceCostEstimate{
				MarketType: store.MarketSpot,
				USDPerHour: 0.0312,
				Source:     "EC2 Spot price history",
			},
			want: "$0.0312/hr spot (EC2 Spot price history)",
		},
		{
			name: "nil",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatHourlyCost(tt.estimate); got != tt.want {
				t.Fatalf("formatHourlyCost() = %q, want %q", got, tt.want)
			}
		})
	}
}
