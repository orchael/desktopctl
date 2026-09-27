package provision

import (
	"fmt"
	"regexp"
	"strings"
)

// AgentProfileReference identifies a directory in a GitHub repository.
type AgentProfileReference struct {
	Owner      string
	Repository string
	Path       string
}

var (
	profileOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	profilePartPattern  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// ParseAgentProfileReference validates a reference before it is rendered into
// cloud-init shell commands. Paths cannot escape the selected repository.
func ParseAgentProfileReference(value string) (AgentProfileReference, error) {
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return AgentProfileReference{}, fmt.Errorf("invalid agent profile reference %q: expected owner/repository[:path]", value)
	}
	repository := strings.Split(parts[0], "/")
	if len(repository) != 2 || !profileOwnerPattern.MatchString(repository[0]) ||
		!profilePartPattern.MatchString(repository[1]) || repository[1] == "." || repository[1] == ".." {
		return AgentProfileReference{}, fmt.Errorf("invalid agent profile reference %q: expected owner/repository[:path]", value)
	}
	ref := AgentProfileReference{Owner: repository[0], Repository: repository[1]}
	if len(parts) == 2 {
		if parts[1] == "" {
			return AgentProfileReference{}, fmt.Errorf("invalid agent profile path in %q", value)
		}
		for _, part := range strings.Split(parts[1], "/") {
			if !profilePartPattern.MatchString(part) || part == "." || part == ".." {
				return AgentProfileReference{}, fmt.Errorf("invalid agent profile path in %q", value)
			}
		}
		ref.Path = parts[1]
	}
	return ref, nil
}
