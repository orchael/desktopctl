package repo

import (
	"fmt"
	"net/url"
	"strings"
)

// Repo represents a parsed GitHub repository reference.
type Repo struct {
	Owner string
	Name  string
}

// HTTPS returns the canonical HTTPS clone URL.
func (r *Repo) HTTPS() string {
	return fmt.Sprintf("https://github.com/%s/%s.git", r.Owner, r.Name)
}

// SSH returns the canonical SSH clone URL.
func (r *Repo) SSH() string {
	return fmt.Sprintf("git@github.com:%s/%s.git", r.Owner, r.Name)
}

// String returns a human-readable representation.
func (r *Repo) String() string {
	return fmt.Sprintf("github.com/%s/%s", r.Owner, r.Name)
}

// Parse parses a GitHub repository URL in any of the supported formats:
//   - github.com/owner/repo
//   - https://github.com/owner/repo
//   - https://github.com/owner/repo.git
//   - git@github.com:owner/repo.git
//
// Non-GitHub URLs and malformed inputs return an error.
func Parse(raw string) (*Repo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty repository URL")
	}

	// Handle git@github.com:owner/repo[.git] SSH format.
	if strings.HasPrefix(raw, "git@github.com:") {
		path := strings.TrimPrefix(raw, "git@github.com:")
		return parsePath(path)
	}

	// Handle https:// or bare github.com/... forms.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", raw, err)
	}

	host := strings.ToLower(u.Hostname())
	if host != "github.com" {
		return nil, fmt.Errorf("not a GitHub URL: host is %q", host)
	}

	return parsePath(u.Path)
}

func parsePath(path string) (*Repo, error) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("cannot parse owner/repo from %q", path)
	}
	return &Repo{Owner: parts[0], Name: parts[1]}, nil
}

// ValidateOwnerBoundary checks that all repos belong to the given owner.
// It returns an error for the first repo that violates the boundary.
func ValidateOwnerBoundary(owner string, repos []*Repo) error {
	for _, r := range repos {
		if !strings.EqualFold(r.Owner, owner) {
			return fmt.Errorf("repository %s belongs to owner %q but desktop owner boundary is %q",
				r, r.Owner, owner)
		}
	}
	return nil
}

// ParseAll parses a slice of raw repository URLs and validates that they all
// share the same GitHub owner. It returns the parsed repos and the common
// owner, or an error if any URL is invalid or owners are mixed.
func ParseAll(raws []string) ([]*Repo, string, error) {
	if len(raws) == 0 {
		return nil, "", fmt.Errorf("at least one repository is required")
	}

	repos := make([]*Repo, 0, len(raws))
	for _, raw := range raws {
		r, err := Parse(raw)
		if err != nil {
			return nil, "", err
		}
		repos = append(repos, r)
	}

	owner := repos[0].Owner
	if err := ValidateOwnerBoundary(owner, repos[1:]); err != nil {
		return nil, "", err
	}

	return repos, owner, nil
}
