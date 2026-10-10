package desktop

import "testing"

func TestValidateID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"d-0123abcd", true},
		{"d-0123ABCd", false},
		{"d-123", false},
		{"../../other", false},
		{"desktop-d-0123abcd", false},
		{"d-0123abcd\n", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if got := ValidateID(tc.id) == nil; got != tc.want {
				t.Fatalf("ValidateID(%q) success = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
