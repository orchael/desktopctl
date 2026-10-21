package awsx

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type fakeHostedZoneLookup struct {
	zones  []types.HostedZone
	byID   map[string]types.HostedZone
	listed bool
}

func (f *fakeHostedZoneLookup) GetHostedZone(_ context.Context, in *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	zone := f.byID[aws.ToString(in.Id)]
	return &route53.GetHostedZoneOutput{HostedZone: &zone}, nil
}

func (f *fakeHostedZoneLookup) ListHostedZonesByName(_ context.Context, _ *route53.ListHostedZonesByNameInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesByNameOutput, error) {
	f.listed = true
	return &route53.ListHostedZonesByNameOutput{HostedZones: f.zones}, nil
}

func TestSelectPublicHostedZoneIDPrefersPinnedZone(t *testing.T) {
	name := "desktops.example.com."
	first := types.HostedZone{Id: aws.String("/hostedzone/ZFIRST"), Name: aws.String(name)}
	second := types.HostedZone{Id: aws.String("/hostedzone/ZSECOND"), Name: aws.String(name)}
	api := &fakeHostedZoneLookup{
		zones: []types.HostedZone{first, second},
		byID:  map[string]types.HostedZone{"/hostedzone/ZSECOND": second},
	}
	id, err := selectPublicHostedZoneID(context.Background(), api, "desktops.example.com", "/hostedzone/ZSECOND")
	if err != nil || aws.ToString(id) != "/hostedzone/ZSECOND" || api.listed {
		t.Fatalf("pinned zone selection = %q, %v; listed=%t", aws.ToString(id), err, api.listed)
	}
	api.byID["/hostedzone/ZSECOND"] = types.HostedZone{Id: aws.String("/hostedzone/ZSECOND"), Name: aws.String("other.example.com.")}
	if _, err := selectPublicHostedZoneID(context.Background(), api, "desktops.example.com", "/hostedzone/ZSECOND"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected wrong-name pinned zone to fail, got %v", err)
	}
}
