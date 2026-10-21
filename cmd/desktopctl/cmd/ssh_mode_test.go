package cmd

import "testing"

func TestDesktopSSHMode(t *testing.T) {
	for _, tc := range []struct {
		noDNS     bool
		requested string
		want      string
		wantErr   bool
	}{
		{true, "", "ssm", false},
		{true, "ssm", "ssm", false},
		{true, "ssh", "", true},
		{false, "", "", false},
		{false, "ssh", "ssh", false},
	} {
		got, err := desktopSSHMode(tc.noDNS, tc.requested)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("desktopSSHMode(%t, %q) = %q, %v", tc.noDNS, tc.requested, got, err)
		}
	}
}
