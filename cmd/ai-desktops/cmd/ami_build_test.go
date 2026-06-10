package cmd

import (
	"reflect"
	"testing"
)

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

