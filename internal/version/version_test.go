package version

import "testing"

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version must not be empty")
	}
}

func TestMajor(t *testing.T) {
	for input, want := range map[string]string{"0.7.0": "0", "v1.2.3": "1", "12.0.0-rc.1": "12"} {
		got, err := Major(input)
		if err != nil || got != want {
			t.Errorf("Major(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"dev", "1", "01.2.3", "1.2", ""} {
		if _, err := Major(input); err == nil {
			t.Errorf("Major(%q) unexpectedly succeeded", input)
		}
	}
}
