package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/store"
)

func TestPublicAMIBuildSkipsStoreAndConfig(t *testing.T) {
	previousCfg := cfg
	cfg = &config.Config{Fleet: config.FleetConfig{AMITableName: "missing-table"}}
	t.Cleanup(func() { cfg = previousCfg })
	amiStore, err := openBuildAMIStore(context.Background(), true)
	if err != nil || amiStore != nil {
		t.Fatalf("public build opened an AMI store: store=%v err=%v", amiStore, err)
	}
	var output bytes.Buffer
	regions := []string{"us-east-1", "ca-central-1"}
	amiIDs := map[string]string{"us-east-1": "ami-primary", "ca-central-1": "ami-copy"}
	if err := finishAMIBuild(context.Background(), regions, amiIDs, "20261020-000000", true, nil, cfg, "", &output); err != nil {
		t.Fatal(err)
	}
	if cfg.Desktop.ActiveAMI != nil {
		t.Fatal("public build should not set active AMI pins")
	}
	for _, want := range []string{"us-east-1: ami-primary (public)", "ca-central-1: ami-copy (public)"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("public build output missing %q: %s", want, output.String())
		}
	}
}

func TestPublicAMIBuildWithConfigSavesPinsWithoutStore(t *testing.T) {
	buildCfg := &config.Config{}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	var output bytes.Buffer
	if err := finishAMIBuild(context.Background(), []string{"us-east-1"}, map[string]string{"us-east-1": "ami-public"}, "20261020-000000", true, nil, buildCfg, configPath, &output); err != nil {
		t.Fatal(err)
	}
	if buildCfg.Desktop.ActiveAMI["us-east-1"] != "ami-public" {
		t.Fatalf("active AMI = %q", buildCfg.Desktop.ActiveAMI["us-east-1"])
	}
	saved, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("public build did not write explicit config: %v", err)
	}
	if saved.Desktop.ActiveAMI["us-east-1"] != "ami-public" {
		t.Fatalf("saved active AMI = %q", saved.Desktop.ActiveAMI["us-east-1"])
	}
}

func TestPrivateAMIBuildPersistsResults(t *testing.T) {
	ctx := context.Background()
	amiStore := store.NewInMemoryAMIStore()
	buildCfg := &config.Config{}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	var output bytes.Buffer
	if err := finishAMIBuild(ctx, []string{"us-east-1"}, map[string]string{"us-east-1": "ami-private"}, "20261020-000000", false, amiStore, buildCfg, configPath, &output); err != nil {
		t.Fatal(err)
	}
	if buildCfg.Desktop.ActiveAMI["us-east-1"] != "ami-private" {
		t.Fatalf("active AMI = %q", buildCfg.Desktop.ActiveAMI["us-east-1"])
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("private build did not write config: %v", err)
	}
	if _, err := amiStore.GetAMI(ctx, "us-east-1", "ami-private"); err != nil {
		t.Fatalf("private build did not record AMI history: %v", err)
	}
}

func TestNovncVersionFromAMIName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"novnc-desktop-ubuntu-24.04-elementary-20260525-005909", "20260525-005909"},
		{"novnc-desktop-ubuntu-24.04-elementary-20260101-120000", "20260101-120000"},
		{"some-other-ami-name", ""},
		{"", ""},
		{novncAMINamePrefix, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := novncVersionFromAMIName(tt.name)
			if got != tt.want {
				t.Errorf("novncVersionFromAMIName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseAMIRegions(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback string
		want     []string
		wantErr  bool
	}{
		{name: "fallback", fallback: "us-east-1", want: []string{"us-east-1"}},
		{name: "multiple", raw: "us-east-1, us-west-2", want: []string{"us-east-1", "us-west-2"}},
		{name: "deduplicate", raw: "us-east-1,us-east-1", want: []string{"us-east-1"}},
		{name: "missing", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAMIRegions(tt.raw, tt.fallback)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error: got %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("regions: got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSelectedRegionAMIs(t *testing.T) {
	manifest := map[string]string{"us-east-1": "ami-primary", "us-west-2": "ami-copy", "eu-west-1": "ami-unrequested"}
	selected, err := selectedRegionAMIs(manifest, []string{"us-east-1", "us-west-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected, map[string]string{"us-east-1": "ami-primary", "us-west-2": "ami-copy"}) {
		t.Fatalf("selected AMIs = %#v", selected)
	}
	if _, err := selectedRegionAMIs(manifest, []string{"us-east-1", "ca-central-1"}); err == nil || !strings.Contains(err.Error(), "ca-central-1") {
		t.Fatalf("missing copy should fail with region context: %v", err)
	}
}

func TestMissingAMITableError(t *testing.T) {
	err := missingAMITableError("desktopctl-ami-dev", "us-east-2", "dev")
	msg := err.Error()
	for _, want := range []string{"desktopctl-ami-dev", "us-east-2", "init-foundation", "dev"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missingAMITableError() = %q, want it to contain %q", msg, want)
		}
	}
}

func TestRequirePublicAMISharing(t *testing.T) {
	if err := requirePublicAMISharing("ca-central-1", "unblocked", "account"); err != nil {
		t.Fatalf("unblocked region rejected: %v", err)
	}
	for _, state := range []string{"block-new-sharing", ""} {
		err := requirePublicAMISharing("ca-central-1", state, "account")
		if err == nil || !strings.Contains(err.Error(), "ca-central-1") {
			t.Errorf("state %q should stop the build with regional context, got %v", state, err)
		}
	}
}
