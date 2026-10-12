package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryWebInstallUsesNewScopeAndMigratesService(t *testing.T) {
	npmrc := desktopWebNPMRC("test-token")
	if !strings.Contains(npmrc, "@orchael:registry=https://npm.pkg.github.com\n") || strings.Contains(npmrc, "@markcallen:registry=") {
		t.Fatalf("wrong npm scope: %q", npmrc)
	}
	script := registryWebInstallScript("0.6.1")
	assertWebServiceMigration(t, script)
	if !strings.Contains(script, "@orchael/desktopctl@0.6.1") || !strings.Contains(script, "trap 'sudo rm -f /root/.npmrc' EXIT") || strings.Contains(script, "test-token") {
		t.Fatalf("registry install or token cleanup missing: %s", script)
	}
}

func TestLocalWebInstallMigratesService(t *testing.T) {
	script := localWebInstallScript("/tmp/orchael-desktopctl-0.6.1.tgz")
	assertWebServiceMigration(t, script)
	if !strings.Contains(script, "sudo npm install --prefix /opt/ai-desktops/web /tmp/orchael-desktopctl-0.6.1.tgz") {
		t.Fatalf("local tarball install missing: %s", script)
	}
}

func assertWebServiceMigration(t *testing.T, script string) {
	t.Helper()
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(script)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("remote shell script is invalid: %v: %s", err, output)
	}
	oldPath := "/node_modules/@markcallen/desktop-web/server-dist/index.js"
	newPath := "/node_modules/@orchael/desktopctl/server-dist/index.js"
	for _, want := range []string{oldPath, newPath, "ai-desktops-web.service.d", "sudo systemctl daemon-reload"} {
		if !strings.Contains(script, want) {
			t.Fatalf("service migration missing %q: %s", want, script)
		}
	}
	if strings.Index(script, "sudo systemctl daemon-reload") > strings.Index(script, "sudo systemctl restart ai-desktops-web") {
		t.Fatalf("service restarts before its unit is migrated: %s", script)
	}
}

func TestReadLocalPkgJSONRequiresDesktopctlPackage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package.json")
	for _, tt := range []struct {
		name    string
		wantErr bool
	}{
		{"@markcallen/desktop-web", true},
		{"@orchael/desktopctl", false},
	} {
		if err := os.WriteFile(path, []byte(`{"name":"`+tt.name+`","version":"0.6.1"}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := readLocalPkgJSON(dir)
		if (err != nil) != tt.wantErr {
			t.Fatalf("package %q: error %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}
