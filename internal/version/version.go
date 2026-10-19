package version

import (
	"fmt"
	"regexp"
)

var semverPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[-+][0-9A-Za-z.-]+)?$`)

// Version is the current CLI version. It may be overridden at build time via
// -ldflags="-X github.com/orchael/desktopctl/internal/version.Version=x.y.z".
var Version = "0.7.0"

// DesktopWebVersion is the @orchael/desktopctl npm package version baked
// into AMIs built by this CLI. It is always kept in sync with Version.
var DesktopWebVersion = "0.7.0"

// Major returns the decimal major component of a release version.
func Major(release string) (string, error) {
	parts := semverPattern.FindStringSubmatch(release)
	if parts == nil {
		return "", fmt.Errorf("desktopctl version %q is not a semantic version", release)
	}
	return parts[1], nil
}
