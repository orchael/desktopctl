package main

import (
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"testing"
)

type desktopMocks struct {
	supported, lookupError bool
	memorySize             int
	lookups                int
	instance               resource.PropertyMap
}

func (m *desktopMocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if args.Token != "aws:ec2/getInstanceType:getInstanceType" {
		return nil, fmt.Errorf("unexpected lookup %s", args.Token)
	}
	m.lookups++
	if m.lookupError {
		return nil, fmt.Errorf("lookup unavailable")
	}
	return resource.NewPropertyMapFromMap(map[string]interface{}{"hibernationSupported": m.supported, "memorySize": m.memorySize}), nil
}
func (m *desktopMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	if args.TypeToken == "aws:ec2/instance:Instance" {
		m.instance = args.Inputs
		outputs := args.Inputs.Copy()
		outputs["publicIp"] = resource.NewStringProperty("192.0.2.1")
		return "i-test", outputs, nil
	}
	return args.Name, args.Inputs, nil
}
func TestDesktopSpotHibernation(t *testing.T) {
	tests := []struct {
		memorySize                                    int
		name, market                                  string
		nested, supported, lookupError, wantHibernate bool
		wantBehavior                                  string
		wantLookups                                   int
	}{
		{name: "supported spot", market: "spot", supported: true, memorySize: 16 * 1024, wantHibernate: true, wantBehavior: "hibernate", wantLookups: 1},
		{name: "Linux RAM boundary", market: "spot", supported: true, memorySize: 150 * 1024, wantHibernate: true, wantBehavior: "hibernate", wantLookups: 1},
		{name: "too much Linux RAM", market: "spot", supported: true, memorySize: 192 * 1024, wantBehavior: "stop", wantLookups: 1},
		{name: "unsupported spot", market: "spot", wantBehavior: "stop", wantLookups: 1},
		{name: "nested spot", market: "spot", nested: true, supported: true, wantBehavior: "stop"},
		{name: "on demand", market: "on-demand", wantHibernate: true},
		{name: "nested on demand", market: "on-demand", nested: true},
		{name: "failed capability lookup", market: "spot", lookupError: true, wantLookups: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mocks := &desktopMocks{supported: tt.supported, lookupError: tt.lookupError, memorySize: tt.memorySize}
			config := map[string]string{
				"desktop:desktopId": "d-test", "desktop:githubOwner": "test", "desktop:zone": "example.test",
				"desktop:subnetId": "subnet-test", "desktop:securityGroupId": "sg-test", "desktop:instanceProfile": "profile-test",
				"aws:region": "us-east-1", "desktop:amiId": "ami-test", "desktop:userData": "#cloud-config",
				"desktop:dnsEnabled": "false", "desktop:instanceType": "m6i.xlarge", "desktop:marketType": tt.market,
				"desktop:nestedVirtualization": fmt.Sprint(tt.nested), "desktop:spotMaxPrice": "0.12",
			}
			err := pulumi.RunErr(run, pulumi.WithMocks("desktop", "test", mocks), func(info *pulumi.RunInfo) { info.Config = config })
			if tt.lookupError {
				if err == nil {
					t.Fatal("expected capability lookup error")
				}
				if mocks.instance != nil {
					t.Fatal("instance registered after failed lookup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mocks.lookups != tt.wantLookups {
				t.Fatalf("lookups = %d, want %d", mocks.lookups, tt.wantLookups)
			}
			if mocks.instance["hibernation"].BoolValue() != tt.wantHibernate {
				t.Fatalf("unexpected hibernation: %v", mocks.instance["hibernation"])
			}
			if tt.market == "spot" {
				spot := mocks.instance["instanceMarketOptions"].ObjectValue()["spotOptions"].ObjectValue()
				if spot["instanceInterruptionBehavior"].StringValue() != tt.wantBehavior {
					t.Fatalf("unexpected interruption behavior: %v", spot)
				}
				if spot["spotInstanceType"].StringValue() != "persistent" {
					t.Fatal("Spot request must remain persistent")
				}
				if spot["maxPrice"].StringValue() != "0.12" {
					t.Fatal("Spot max price not preserved")
				}
			} else if _, ok := mocks.instance["instanceMarketOptions"]; ok {
				t.Fatal("On-Demand received Spot options")
			}
			if !mocks.instance["rootBlockDevice"].ObjectValue()["encrypted"].BoolValue() {
				t.Fatal("root volume must be encrypted")
			}
		})
	}
}
