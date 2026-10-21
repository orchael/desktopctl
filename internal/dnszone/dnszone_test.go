package dnszone

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

type fakeRoute53 struct {
	zones   []types.HostedZone
	created int
}

func (f *fakeRoute53) ListHostedZones(_ context.Context, _ *route53.ListHostedZonesInput, _ ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return &route53.ListHostedZonesOutput{HostedZones: f.zones}, nil
}

func (f *fakeRoute53) GetHostedZone(_ context.Context, input *route53.GetHostedZoneInput, _ ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error) {
	for _, zone := range f.zones {
		if aws.ToString(zone.Id) == aws.ToString(input.Id) {
			return &route53.GetHostedZoneOutput{HostedZone: &zone, DelegationSet: &types.DelegationSet{NameServers: []string{"ns-1.example.net.", "ns-2.example.org."}}}, nil
		}
	}
	return nil, errors.New("missing zone")
}

func (f *fakeRoute53) CreateHostedZone(_ context.Context, input *route53.CreateHostedZoneInput, _ ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error) {
	f.created++
	zone := types.HostedZone{Id: aws.String("/hostedzone/ZNEW"), Name: aws.String(aws.ToString(input.Name) + ".")}
	f.zones = append(f.zones, zone)
	return &route53.CreateHostedZoneOutput{HostedZone: &zone, DelegationSet: &types.DelegationSet{NameServers: []string{"ns-1.example.net.", "ns-2.example.org."}}}, nil
}

func TestEnsureCreatesOrReusesPublicZone(t *testing.T) {
	api := &fakeRoute53{}
	zone, created, err := Ensure(context.Background(), api, "Desktops.MarkCallen.Dev.")
	if err != nil || !created || zone.Name != "desktops.markcallen.dev" || zone.ID != "/hostedzone/ZNEW" {
		t.Fatalf("Ensure create = %+v, %t, %v", zone, created, err)
	}
	zone, created, err = Ensure(context.Background(), api, "desktops.markcallen.dev")
	if err != nil || created || api.created != 1 {
		t.Fatalf("Ensure reuse = %+v, %t, %v, creates=%d", zone, created, err, api.created)
	}
}

func TestEnsureRejectsDuplicateOrPrivateZone(t *testing.T) {
	api := &fakeRoute53{zones: []types.HostedZone{
		{Id: aws.String("Z1"), Name: aws.String("desktops.example.com.")},
		{Id: aws.String("Z2"), Name: aws.String("desktops.example.com.")},
	}}
	if _, _, err := Ensure(context.Background(), api, "desktops.example.com"); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	api.zones = []types.HostedZone{{Id: aws.String("ZP"), Name: aws.String("desktops.example.com."), Config: &types.HostedZoneConfig{PrivateZone: true}}}
	if _, err := Get(context.Background(), api, "desktops.example.com", "ZP"); err == nil {
		t.Fatal("private zone was accepted")
	}
}

func TestVerifyDelegationRequiresExactNameservers(t *testing.T) {
	zone := Zone{Name: "desktops.example.com", NameServers: []string{"ns-1.example.net", "ns-2.example.org"}}
	lookup := func(_ context.Context, name string) ([]*net.NS, error) {
		if name != zone.Name {
			t.Fatalf("lookup name = %q", name)
		}
		return []*net.NS{{Host: "NS-2.EXAMPLE.ORG."}, {Host: "ns-1.example.net."}}, nil
	}
	if err := VerifyDelegation(context.Background(), zone, lookup); err != nil {
		t.Fatal(err)
	}
	wrong := func(context.Context, string) ([]*net.NS, error) {
		return []*net.NS{{Host: "ns-1.example.net."}}, nil
	}
	if err := VerifyDelegation(context.Background(), zone, wrong); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected mismatch, got %v", err)
	}
	missing := func(context.Context, string) ([]*net.NS, error) { return nil, errors.New("NXDOMAIN") }
	if err := VerifyDelegation(context.Background(), zone, missing); err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("expected missing delegation, got %v", err)
	}
}

func TestNormalizeRejectsInvalidZones(t *testing.T) {
	for _, name := range []string{"", "localhost", "-bad.example.com", "*.example.com", "https://example.com", "foo..com"} {
		if _, err := Normalize(name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if got, err := Normalize("DESKTOPS.Example.COM."); err != nil || !reflect.DeepEqual(got, "desktops.example.com") {
		t.Fatalf("Normalize = %q, %v", got, err)
	}
}
