package repo

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		// bare github.com/owner/repo
		{"github.com/acme/myapp", "acme", "myapp", false},
		// https with .git
		{"https://github.com/acme/myapp.git", "acme", "myapp", false},
		// https without .git
		{"https://github.com/acme/myapp", "acme", "myapp", false},
		// SSH format
		{"git@github.com:acme/myapp.git", "acme", "myapp", false},
		// SSH without .git
		{"git@github.com:acme/myapp", "acme", "myapp", false},
		// bare without scheme, no trailing slash
		{"github.com/acme/repo-with-dashes", "acme", "repo-with-dashes", false},

		// errors
		{"https://gitlab.com/acme/myapp", "", "", true},
		{"https://bitbucket.org/acme/myapp", "", "", true},
		{"", "", "", true},
		{"github.com/acme", "", "", true},
		{"not-a-url", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			r, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q): error=%v, wantErr=%v", tt.input, err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if r.Owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", r.Owner, tt.wantOwner)
			}
			if r.Name != tt.wantName {
				t.Errorf("name: got %q, want %q", r.Name, tt.wantName)
			}
		})
	}
}

func TestParseAll_sameOwner(t *testing.T) {
	raws := []string{
		"github.com/acme/app-one",
		"github.com/acme/app-two",
		"git@github.com:acme/app-three.git",
	}
	repos, owner, err := ParseAll(raws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" {
		t.Errorf("owner: got %q, want %q", owner, "acme")
	}
	if len(repos) != 3 {
		t.Errorf("len(repos): got %d, want 3", len(repos))
	}
}

func TestParseAll_mixedOwner(t *testing.T) {
	raws := []string{
		"github.com/acme/app-one",
		"github.com/other/app-two",
	}
	_, _, err := ParseAll(raws)
	if err == nil {
		t.Error("expected error for mixed owners, got nil")
	}
}

func TestParseAll_nonGitHub(t *testing.T) {
	raws := []string{
		"github.com/acme/app-one",
		"https://gitlab.com/acme/app-two",
	}
	_, _, err := ParseAll(raws)
	if err == nil {
		t.Error("expected error for non-GitHub URL, got nil")
	}
}

func TestParseAll_empty(t *testing.T) {
	_, _, err := ParseAll(nil)
	if err == nil {
		t.Error("expected error for empty slice, got nil")
	}
}

func TestValidateOwnerBoundary(t *testing.T) {
	repos := []*Repo{
		{Owner: "acme", Name: "b"},
		{Owner: "other", Name: "c"},
	}
	if err := ValidateOwnerBoundary("acme", repos); err == nil {
		t.Error("expected mixed-owner error, got nil")
	}

	repos2 := []*Repo{{Owner: "acme", Name: "b"}}
	if err := ValidateOwnerBoundary("acme", repos2); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRepoURLs(t *testing.T) {
	r := &Repo{Owner: "acme", Name: "myapp"}
	if got := r.HTTPS(); got != "https://github.com/acme/myapp.git" {
		t.Errorf("HTTPS: got %q", got)
	}
	if got := r.SSH(); got != "git@github.com:acme/myapp.git" {
		t.Errorf("SSH: got %q", got)
	}
}
