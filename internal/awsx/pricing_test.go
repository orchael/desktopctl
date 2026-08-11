package awsx

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
)

func TestOnDemandUSDPerHour(t *testing.T) {
	raw := `{
	  "terms": {
	    "OnDemand": {
	      "sku.term": {
	        "priceDimensions": {
	          "sku.term.rate": {
	            "unit": "Hrs",
	            "pricePerUnit": {"USD": "0.0960000000"}
	          }
	        }
	      }
	    }
	  }
	}`

	got, ok := onDemandUSDPerHour(raw)
	if !ok {
		t.Fatal("expected price to parse")
	}
	if got != 0.096 {
		t.Fatalf("price = %v, want 0.096", got)
	}
}

func TestOnDemandUSDPerHourRejectsMissingPrice(t *testing.T) {
	if got, ok := onDemandUSDPerHour(`{"terms":{"OnDemand":{}}}`); ok || got != 0 {
		t.Fatalf("price = %v, ok = %v; want 0, false", got, ok)
	}
}

func TestOnDemandLinuxHourlyPricePaginates(t *testing.T) {
	client := &fakePricingClient{
		pages: []*pricing.GetProductsOutput{
			{
				PriceList: []string{`{"terms":{"OnDemand":{}}}`},
				NextToken: aws.String("page-2"),
			},
			{
				PriceList: []string{`{
				  "terms": {
				    "OnDemand": {
				      "sku.term": {
				        "priceDimensions": {
				          "sku.term.rate": {
				            "unit": "Hrs",
				            "pricePerUnit": {"USD": "0.1920000000"}
				          }
				        }
				      }
				    }
				  }
				}`},
			},
		},
	}

	got, err := onDemandLinuxHourlyPrice(context.Background(), client, "us-east-1", "m7i.xlarge")
	if err != nil {
		t.Fatalf("onDemandLinuxHourlyPrice: %v", err)
	}
	if got != 0.192 {
		t.Fatalf("price = %v, want 0.192", got)
	}
	if client.calls != 2 {
		t.Fatalf("GetProducts calls = %d, want 2", client.calls)
	}
	if client.tokens[0] != "" {
		t.Fatalf("first page token = %q, want empty", client.tokens[0])
	}
	if client.tokens[1] != "page-2" {
		t.Fatalf("second page token = %q, want page-2", client.tokens[1])
	}
}

type fakePricingClient struct {
	pages  []*pricing.GetProductsOutput
	calls  int
	tokens []string
}

func (f *fakePricingClient) GetProducts(_ context.Context, input *pricing.GetProductsInput, _ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	f.tokens = append(f.tokens, aws.ToString(input.NextToken))
	if f.calls >= len(f.pages) {
		return &pricing.GetProductsOutput{}, nil
	}
	out := f.pages[f.calls]
	f.calls++
	return out, nil
}
