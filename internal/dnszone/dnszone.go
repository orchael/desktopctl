package dnszone

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/route53/types"
)

var labelRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type API interface {
	ListHostedZones(context.Context, *route53.ListHostedZonesInput, ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error)
	GetHostedZone(context.Context, *route53.GetHostedZoneInput, ...func(*route53.Options)) (*route53.GetHostedZoneOutput, error)
	CreateHostedZone(context.Context, *route53.CreateHostedZoneInput, ...func(*route53.Options)) (*route53.CreateHostedZoneOutput, error)
}

type Zone struct {
	Name        string
	ID          string
	NameServers []string
}

func Normalize(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if len(name) > 253 || len(name) < 3 || strings.ContainsAny(name, "/: ") {
		return "", fmt.Errorf("invalid DNS zone %q", name)
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", fmt.Errorf("DNS zone must be a domain or subdomain")
	}
	for _, label := range labels {
		if !labelRE.MatchString(label) {
			return "", fmt.Errorf("invalid DNS zone label %q", label)
		}
	}
	return name, nil
}

func normalizeNS(names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, strings.TrimSuffix(strings.ToLower(name), "."))
	}
	slices.Sort(result)
	return result
}

func findZones(ctx context.Context, api API, name string) ([]types.HostedZone, error) {
	var matches []types.HostedZone
	var marker *string
	for {
		out, err := api.ListHostedZones(ctx, &route53.ListHostedZonesInput{Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("list Route 53 zones: %w", err)
		}
		for _, zone := range out.HostedZones {
			if strings.TrimSuffix(strings.ToLower(aws.ToString(zone.Name)), ".") == name && (zone.Config == nil || !zone.Config.PrivateZone) {
				matches = append(matches, zone)
			}
		}
		if !out.IsTruncated {
			return matches, nil
		}
		if out.NextMarker == nil || aws.ToString(out.NextMarker) == "" {
			return nil, fmt.Errorf("route 53 zones listing ended without a next marker")
		}
		marker = out.NextMarker
	}
}

func describe(ctx context.Context, api API, id, name string) (Zone, error) {
	out, err := api.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		return Zone{}, fmt.Errorf("get Route 53 zone %q: %w", id, err)
	}
	if out.HostedZone == nil || strings.TrimSuffix(strings.ToLower(aws.ToString(out.HostedZone.Name)), ".") != name || (out.HostedZone.Config != nil && out.HostedZone.Config.PrivateZone) {
		return Zone{}, fmt.Errorf("route 53 zone %q does not match public DNS zone %q", id, name)
	}
	if out.DelegationSet == nil || len(out.DelegationSet.NameServers) == 0 {
		return Zone{}, fmt.Errorf("route 53 zone %q has no public name servers", id)
	}
	return Zone{Name: name, ID: aws.ToString(out.HostedZone.Id), NameServers: normalizeNS(out.DelegationSet.NameServers)}, nil
}

// Get finds the one public hosted zone with this name, or the pinned zone ID.
func Get(ctx context.Context, api API, name, id string) (Zone, error) {
	var err error
	name, err = Normalize(name)
	if err != nil {
		return Zone{}, err
	}
	if id != "" {
		return describe(ctx, api, id, name)
	}
	matches, err := findZones(ctx, api, name)
	if err != nil {
		return Zone{}, err
	}
	if len(matches) != 1 {
		return Zone{}, fmt.Errorf("found %d public Route 53 zones named %q; run dns setup to select one", len(matches), name)
	}
	return describe(ctx, api, aws.ToString(matches[0].Id), name)
}

// Ensure reuses an existing public hosted zone or creates one for delegation.
func Ensure(ctx context.Context, api API, name string) (Zone, bool, error) {
	var err error
	name, err = Normalize(name)
	if err != nil {
		return Zone{}, false, err
	}
	matches, err := findZones(ctx, api, name)
	if err != nil {
		return Zone{}, false, err
	}
	if len(matches) > 1 {
		return Zone{}, false, fmt.Errorf("multiple public Route 53 zones named %q; resolve the duplicate before setup", name)
	}
	if len(matches) == 1 {
		zone, err := describe(ctx, api, aws.ToString(matches[0].Id), name)
		return zone, false, err
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return Zone{}, false, err
	}
	reference := "desktopctl-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(nonce[:])
	out, err := api.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{Name: aws.String(name), CallerReference: aws.String(reference)})
	if err != nil {
		return Zone{}, false, fmt.Errorf("create Route 53 zone %q: %w", name, err)
	}
	if out.HostedZone == nil || out.DelegationSet == nil || len(out.DelegationSet.NameServers) == 0 {
		return Zone{}, false, fmt.Errorf("route 53 returned no hosted zone or name servers for %q", name)
	}
	return Zone{Name: name, ID: aws.ToString(out.HostedZone.Id), NameServers: normalizeNS(out.DelegationSet.NameServers)}, true, nil
}

type LookupNS func(context.Context, string) ([]*net.NS, error)

// VerifyDelegation checks public DNS resolution against the assigned NS set.
func VerifyDelegation(ctx context.Context, zone Zone, lookup LookupNS) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	observed, err := lookup(ctx, zone.Name)
	if err != nil {
		return fmt.Errorf("DNS delegation for %q is not visible yet: %w; expected NS %s", zone.Name, err, strings.Join(zone.NameServers, ", "))
	}
	actual := make([]string, 0, len(observed))
	for _, ns := range observed {
		actual = append(actual, ns.Host)
	}
	actual = normalizeNS(actual)
	if !slices.Equal(actual, zone.NameServers) {
		return fmt.Errorf("DNS delegation for %q does not match Route 53: got [%s], expected [%s]", zone.Name, strings.Join(actual, ", "), strings.Join(zone.NameServers, ", "))
	}
	return nil
}
