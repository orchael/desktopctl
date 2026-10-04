package version

// Version is the current CLI version. It may be overridden at build time via
// -ldflags="-X github.com/orchael/desktopctl/internal/version.Version=x.y.z".
var Version = "0.6.1"

// DesktopWebVersion is the @orchael/desktopctl npm package version baked
// into AMIs built by this CLI. It is always kept in sync with Version.
var DesktopWebVersion = "0.6.1"
