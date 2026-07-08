package version

// Version is the current CLI version. It may be overridden at build time via
// -ldflags="-X github.com/orchael/ai-desktops/internal/version.Version=x.y.z".
var Version = "0.3.1"

// DesktopWebVersion is the @markcallen/desktop-web npm package version baked
// into AMIs built by this CLI. It is always kept in sync with Version.
var DesktopWebVersion = "0.3.1"
