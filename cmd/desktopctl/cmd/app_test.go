package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/orchael/desktopctl/internal/config"
)

func TestAppTokenIsPrivateAndStable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	first, err := appToken(true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := appToken(false)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("app token was not reused")
	}
	path, err := appTokenPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %o", info.Mode().Perm())
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := appToken(false); err == nil || !strings.Contains(err.Error(), "private regular file") {
		t.Fatalf("expected private file error, got %v", err)
	}
}

func TestAppBindUsesConfigAndFlagOverride(t *testing.T) {
	previousBind, previousConfig := appBind, cfg
	t.Cleanup(func() { appBind, cfg = previousBind, previousConfig })
	cfg = &config.Config{App: config.AppConfig{Bind: "192.168.1.20"}}
	appBind = ""
	if got := effectiveAppBind(); got != "192.168.1.20" {
		t.Fatalf("configured bind = %q", got)
	}
	if err := validateAppAddress(); err != nil {
		t.Fatalf("configured bind: %v", err)
	}
	appBind = "0.0.0.0"
	if got := effectiveAppBind(); got != "0.0.0.0" || appBindWarning() == "" {
		t.Fatalf("wildcard override = %q, warning = %q", got, appBindWarning())
	}
	appBind = "not-an-ip"
	if err := validateAppAddress(); err == nil || !strings.Contains(err.Error(), "IPv4") {
		t.Fatalf("expected IPv4 error, got %v", err)
	}
}

func TestAppOriginMustBeHTTPSWithoutCredentials(t *testing.T) {
	oldBind, oldPort, oldOrigin := appBind, appPort, appOrigin
	t.Cleanup(func() { appBind, appPort, appOrigin = oldBind, oldPort, oldOrigin })
	appBind, appPort = "127.0.0.1", 3000
	for _, origin := range []string{"http://private.example", "https://user:pass@private.example", "https://private.example/path"} {
		appOrigin = origin
		if err := validateAppAddress(); err == nil {
			t.Errorf("accepted invalid origin %q", origin)
		}
	}
	appOrigin = "https://app.example.ts.net"
	if err := validateAppAddress(); err != nil {
		t.Fatalf("private HTTPS origin: %v", err)
	}
}
