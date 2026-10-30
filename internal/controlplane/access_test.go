package controlplane

import "testing"

func TestParseDesktopAccessURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{"issued token", "Desktop URL : https://d-123.example.com:8443/access?token=one-session\nExpires : soon", true},
		{"base URL", "Desktop URL : https://d-123.example.com:8443/novnc/vnc.html", false},
		{"wrong host", "Desktop URL : https://other.example.com/access?token=one-session", false},
		{"missing token", "Desktop URL : https://d-123.example.com/access", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseDesktopAccessURL(tc.output, "d-123.example.com")
			if (err == nil) != tc.valid {
				t.Fatalf("valid = %t, error = %v", tc.valid, err)
			}
		})
	}
}
