package version

import "testing"

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version must not be empty")
	}
}
