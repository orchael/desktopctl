package provision

import "testing"

func TestParseAgentProfileReference(t *testing.T) {
	for _, tc := range []struct {
		input, owner, repo, path string
	}{
		{input: "markcallen/ai-desktop-profile", owner: "markcallen", repo: "ai-desktop-profile"},
		{input: "orchael/crew:profiles/reviewer", owner: "orchael", repo: "crew", path: "profiles/reviewer"},
	} {
		got, err := ParseAgentProfileReference(tc.input)
		if err != nil {
			t.Fatalf("ParseAgentProfileReference(%q): %v", tc.input, err)
		}
		if got.Owner != tc.owner || got.Repository != tc.repo || got.Path != tc.path {
			t.Fatalf("ParseAgentProfileReference(%q) = %#v", tc.input, got)
		}
	}
}

func TestParseAgentProfileReferenceRejectsUnsafeInput(t *testing.T) {
	for _, input := range []string{
		"", "owner", "/repo", "owner/", "owner/repo/extra", "owner/repo:",
		"owner/repo:../escape", "owner/repo:/absolute", "owner/repo:profiles//reviewer",
		"owner/repo:profiles/./reviewer", "owner/repo;echo bad", "owner/repo\nnext",
		"owner/repo:profile space", "owner/repo:-option", "-owner/repo",
	} {
		if _, err := ParseAgentProfileReference(input); err == nil {
			t.Errorf("accepted unsafe reference %q", input)
		}
	}
}
