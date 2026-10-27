package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestAppEnvironmentRequiresLoopback(t *testing.T) {
	previousBind, previousPort, previousConfig := appBind, appPort, cfgFile
	t.Cleanup(func() { appBind, appPort, cfgFile = previousBind, previousPort, previousConfig })
	appBind, appPort = "0.0.0.0", 3000
	cfgFile = filepath.Join(t.TempDir(), "config.yaml")
	if _, err := appEnvironment(false); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("expected loopback error, got %v", err)
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
