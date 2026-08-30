package provision

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var npmScopePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// NormalizeNPMGitHubScopes validates and deduplicates npm scopes that should
// be configured to resolve from GitHub Packages. Scopes may be supplied with or
// without the leading "@".
func NormalizeNPMGitHubScopes(scopes []string) ([]string, error) {
	seen := make(map[string]struct{}, len(scopes))
	normalized := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimPrefix(strings.TrimSpace(scope), "@")
		if scope == "" {
			continue
		}
		if !npmScopePattern.MatchString(scope) {
			return nil, fmt.Errorf("invalid npm GitHub Packages scope %q: use a package scope like @myorg or myorg", scope)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		normalized = append(normalized, scope)
	}
	sort.Strings(normalized)
	return normalized, nil
}
