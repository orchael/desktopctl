package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

func TestDisposableAVDsRequireOptInAndSavedDefinitions(t *testing.T) {
	d, w := parkedFixture()
	if err := parkEligibleWithAVDs(d, w, true); err == nil {
		t.Fatal("AVD recreation was allowed without any configured AVDs")
	}
	d.NestedVirt = true
	d.AVDNames = []string{"pixel_7_phone", "pixel_tablet"}
	if err := parkEligible(d, w); err == nil {
		t.Fatal("AVD desktop was parkable without opt-in")
	}
	if err := parkEligibleWithAVDs(d, w, true); err != nil {
		t.Fatal(err)
	}
	boot := &provision.BootstrapConfig{DesktopID: d.DesktopID, AWSRegion: d.Region, AVDs: []config.AVDConfig{
		{Name: "pixel_7_phone", Image: "system-images;android-35;google_apis;x86_64", Device: "pixel_7"},
		{Name: "pixel_tablet", Image: "system-images;android-35;google_apis;x86_64", Device: "pixel_tablet"},
	}}
	cloud, err := provision.RenderCloudInit(boot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := gzipBase64UserData(cloud)
	if err != nil {
		t.Fatal(err)
	}
	d.RestoreSpec = &store.RestoreSpec{Version: 2, Config: map[string]string{"nestedVirtualization": "true", "userDataBase64": encoded}}
	if err := validateAVDRestore(d); err != nil {
		t.Fatal(err)
	}
	d.AVDNames[1] = "different"
	if err := validateAVDRestore(d); err == nil {
		t.Fatal("mismatched saved AVDs were accepted")
	}
}

func TestNestedUnparkImportsNewPrelaunchedInstanceAndReusesIt(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-0123abcd", State: store.StateRestoring, NestedVirt: true, AMIID: "ami-one", InstanceType: "m8i.large", Region: "us-east-2", Environment: "dev", GitHubOwner: "orchael", Hostname: "desktop.example.test"}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	config := pulumi.StackConfig{"nestedVirtualization": "true", "amiId": "ami-one", "instanceType": "m8i.large", "subnetId": "subnet-one", "securityGroupId": "sg-one", "instanceProfile": "profile-one", "userDataBase64": "encoded", "volumeSize": "200", "importInstanceId": "i-old"}
	launched := 0
	launch := func(_ context.Context, p *instanceLaunchParams, persist func(string) error) (string, error) {
		launched++
		if p.volumeSize != 200 || p.instanceType != "m8i.large" {
			t.Fatalf("wrong launch parameters: %+v", p)
		}
		if err := persist("i-replacement"); err != nil {
			return "", err
		}
		return "i-replacement", nil
	}
	inspect := func(_ context.Context, id string) (*awsx.EC2InstanceStatus, error) {
		if id != "i-replacement" {
			return nil, errors.New("wrong instance inspected")
		}
		return &awsx.EC2InstanceStatus{State: "running", NestedVirtualization: "enabled", InstanceType: "m8i.large", ImageID: "ami-one", SubnetID: "subnet-one", SecurityGroupIDs: []string{"sg-one"}}, nil
	}
	if err := prepareNestedUnparkWith(ctx, s, d, config, launch, inspect); err != nil {
		t.Fatal(err)
	}
	if config["importInstanceId"] != "i-replacement" {
		t.Fatal("stale import ID was retained")
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		t.Fatal(err)
	}
	if current.InstanceID != "i-replacement" {
		t.Fatal("new instance ID was not persisted")
	}
	if err := prepareNestedUnparkWith(ctx, s, d, config, launch, inspect); err != nil {
		t.Fatal(err)
	}
	if launched != 1 {
		t.Fatalf("retry launched %d instances", launched)
	}
}
