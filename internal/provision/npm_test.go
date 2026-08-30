package provision

import "testing"

func TestNormalizeNPMGitHubScopes(t *testing.T) {
	got, err := NormalizeNPMGitHubScopes([]string{" @acme ", "private-tools", "acme", ""})
	if err != nil {
		t.Fatalf("NormalizeNPMGitHubScopes: %v", err)
	}
	want := []string{"acme", "private-tools"}
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeNPMGitHubScopes_invalid(t *testing.T) {
	if _, err := NormalizeNPMGitHubScopes([]string{"bad scope"}); err == nil {
		t.Fatal("expected error for invalid scope")
	}
}
