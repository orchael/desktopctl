package awsx

import "testing"

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
