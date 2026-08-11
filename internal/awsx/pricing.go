package awsx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"
)

// InstanceCostEstimate is an hourly EC2 instance price estimate.
type InstanceCostEstimate struct {
	MarketType   string
	InstanceType string
	Region       string
	USDPerHour   float64
	Source       string
}

// EstimateInstanceHourlyCost returns a best-effort hourly Linux/UNIX EC2 price.
func EstimateInstanceHourlyCost(ctx context.Context, cfg aws.Config, region, instanceType, marketType string) (*InstanceCostEstimate, error) {
	if instanceType == "" {
		return nil, fmt.Errorf("instance type is empty")
	}
	if marketType == "spot" {
		price, err := SpotLinuxHourlyPrice(ctx, cfg, instanceType)
		if err != nil {
			return nil, err
		}
		return &InstanceCostEstimate{MarketType: "spot", InstanceType: instanceType, Region: region, USDPerHour: price, Source: "EC2 Spot price history"}, nil
	}
	price, err := OnDemandLinuxHourlyPrice(ctx, cfg, region, instanceType)
	if err != nil {
		return nil, err
	}
	return &InstanceCostEstimate{MarketType: "on-demand", InstanceType: instanceType, Region: region, USDPerHour: price, Source: "AWS Price List"}, nil
}

// SpotLinuxHourlyPrice returns the lowest recent Linux/UNIX Spot price for an
// instance type in the configured EC2 region.
func SpotLinuxHourlyPrice(ctx context.Context, cfg aws.Config, instanceType string) (float64, error) {
	client := ec2.NewFromConfig(cfg)
	out, err := client.DescribeSpotPriceHistory(ctx, &ec2.DescribeSpotPriceHistoryInput{
		InstanceTypes:       []ec2types.InstanceType{ec2types.InstanceType(instanceType)},
		ProductDescriptions: []string{"Linux/UNIX"},
		StartTime:           aws.Time(time.Now().Add(-1 * time.Hour)),
		MaxResults:          aws.Int32(1000),
	})
	if err != nil {
		return 0, fmt.Errorf("describe spot price history: %w", err)
	}
	best := math.MaxFloat64
	for _, price := range out.SpotPriceHistory {
		value, err := strconv.ParseFloat(aws.ToString(price.SpotPrice), 64)
		if err == nil && value > 0 && value < best {
			best = value
		}
	}
	if best == math.MaxFloat64 {
		return 0, fmt.Errorf("no recent Linux/UNIX Spot price found for %s", instanceType)
	}
	return best, nil
}

// OnDemandLinuxHourlyPrice returns the Linux shared-tenancy on-demand hourly
// price for an EC2 instance type in region.
func OnDemandLinuxHourlyPrice(ctx context.Context, cfg aws.Config, region, instanceType string) (float64, error) {
	pricingCfg := cfg.Copy()
	pricingCfg.Region = "us-east-1"
	client := pricing.NewFromConfig(pricingCfg)
	out, err := client.GetProducts(ctx, &pricing.GetProductsInput{
		ServiceCode: aws.String("AmazonEC2"),
		Filters: []pricingtypes.Filter{
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("instanceType"), Value: aws.String(instanceType)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("regionCode"), Value: aws.String(region)},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("operatingSystem"), Value: aws.String("Linux")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("tenancy"), Value: aws.String("Shared")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("preInstalledSw"), Value: aws.String("NA")},
			{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String("capacitystatus"), Value: aws.String("Used")},
		},
		MaxResults: aws.Int32(100),
	})
	if err != nil {
		return 0, fmt.Errorf("get on-demand price: %w", err)
	}
	for _, raw := range out.PriceList {
		price, ok := onDemandUSDPerHour(raw)
		if ok {
			return price, nil
		}
	}
	return 0, fmt.Errorf("no Linux on-demand price found for %s in %s", instanceType, region)
}

func onDemandUSDPerHour(raw string) (float64, bool) {
	var product struct {
		Terms struct {
			OnDemand map[string]struct {
				PriceDimensions map[string]struct {
					Unit         string            `json:"unit"`
					PricePerUnit map[string]string `json:"pricePerUnit"`
				} `json:"priceDimensions"`
			} `json:"OnDemand"`
		} `json:"terms"`
	}
	if err := json.Unmarshal([]byte(raw), &product); err != nil {
		return 0, false
	}
	for _, term := range product.Terms.OnDemand {
		for _, dimension := range term.PriceDimensions {
			if dimension.Unit != "Hrs" {
				continue
			}
			price, err := strconv.ParseFloat(dimension.PricePerUnit["USD"], 64)
			if err == nil && price > 0 {
				return price, true
			}
		}
	}
	return 0, false
}
