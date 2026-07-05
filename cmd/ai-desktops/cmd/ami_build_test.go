package cmd

import (
	"reflect"
	"testing"
)

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

func TestValidateAMIRegionSelection(t *testing.T) {
	if err := validateAMIRegionSelection([]string{"us-east-1"}, "ami-123"); err != nil {
		t.Fatalf("single region override: %v", err)
	}
	if err := validateAMIRegionSelection([]string{"us-east-1", "us-west-2"}, "ami-123"); err == nil {
		t.Fatal("expected multi-region base AMI override error")
	}
}
