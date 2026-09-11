package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// AUTH-4a: output safety is independent of whether the snapshot contains Codex
// keys. No real secrets, AWS calls, services, chown, or mounts are needed.
func TestSecretsOutputPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, target, kind string
	}{
		{"safe-readable-directories", "", "readable"},
		{"missing-directories", "", "missing"},
		{"writable-home", ".", "writable"},
		{"writable-config", ".config", "writable"},
		{"group-writable-config", ".config", "group-writable"},
		{"writable-bridge", ".config/bridgectl", "writable"},
		{"writable-environment", ".config/environment.d", "writable"},
		{"foreign-config", ".config", "foreign"},
		{"foreign-bridge", ".config/bridgectl", "foreign"},
		{"foreign-environment", ".config/environment.d", "foreign"},
		{"symlink-home", ".", "symlink-home"},
		{"symlink-config", ".config", "symlink"},
		{"symlink-bridge", ".config/bridgectl", "symlink"},
		{"symlink-environment", ".config/environment.d", "symlink"},
		{"symlink-agents-file", ".config/bridgectl/agents.env", "symlink"},
		{"symlink-environment-file", ".config/environment.d/desktop-secrets.conf", "symlink"},
		{"symlink-shell-file", ".desktop-secrets", "symlink"},
		{"symlink-bashrc", ".bashrc", "symlink"},
		{"public-credentials", ".config/bridgectl/agents.env", "public-file"},
		{"writable-bashrc", ".bashrc", "writable"},
		{"missing-directories-unsafe-bashrc", ".bashrc", "missing-unsafe"},
		{"non-directory-environment", ".config/environment.d", "non-directory"},
		{"nfs-home", ".", "nfs"},
		{"nfs-config", ".config", "nfs"},
		{"nfs-bridge", ".config/bridgectl", "nfs"},
		{"nfs-environment", ".config/environment.d", "nfs"},
		{"nfs-agents-file", ".config/bridgectl/agents.env", "nfs"},
		{"nfs-environment-file", ".config/environment.d/desktop-secrets.conf", "nfs"},
		{"nfs-shell-file", ".desktop-secrets", "nfs"},
		{"nfs-bashrc", ".bashrc", "nfs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, bin := t.TempDir(), t.TempDir()
			home := filepath.Join(root, "home")
			write := func(path, value string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(value), mode); err != nil {
					t.Fatal(err)
				}
			}
			outputs := []string{".config/bridgectl/agents.env", ".config/environment.d/desktop-secrets.conf", ".desktop-secrets"}
			for _, output := range outputs {
				write(filepath.Join(home, output), "APP_KEY='old'\n", 0600)
			}
			write(filepath.Join(home, ".bashrc"), "# existing shell settings\n", 0644)
			write(filepath.Join(bin, "aws"), "#!/bin/sh\nprintf '%s' '{\"APP_KEY\":\"dummy-secret-marker\"}'\n", 0700)
			write(filepath.Join(bin, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/service-calls\"\nif [ \"$2\" = show ]; then echo loaded; fi\n", 0700)
			script := buildSecretsReloadScript([]string{"/test/application"}, "us-east-2")
			target := filepath.Join(home, tc.target)
			success := tc.kind == "readable" || tc.kind == "missing"
			switch tc.kind {
			case "readable":
				for _, dir := range []string{home, filepath.Join(home, ".config"), filepath.Join(home, ".config/bridgectl"), filepath.Join(home, ".config/environment.d")} {
					if err := os.Chmod(dir, 0755); err != nil {
						t.Fatal(err)
					}
				}
			case "missing":
				if err := os.Rename(filepath.Join(home, ".config"), filepath.Join(root, "previous-config")); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(target, 0777); err != nil {
					t.Fatal(err)
				}
			case "group-writable":
				if err := os.Chmod(target, 0775); err != nil {
					t.Fatal(err)
				}
			case "missing-unsafe":
				if err := os.Rename(filepath.Join(home, ".config"), filepath.Join(root, "previous-config")); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0777); err != nil {
					t.Fatal(err)
				}
			case "non-directory":
				if err := os.Rename(target, filepath.Join(root, "previous-directory")); err != nil {
					t.Fatal(err)
				}
				write(target, "not a directory", 0600)
			case "public-file":
				if err := os.Chmod(target, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink", "symlink-home":
				moved := filepath.Join(root, "external-target")
				if err := os.Rename(target, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, target); err != nil {
					t.Fatal(err)
				}
			case "foreign":
				// Change only the selected directory's observed UID, not the home.
				inject := fmt.Sprintf("import pathlib\n_original_lstat = pathlib.Path.lstat\ndef _foreign_lstat(path, *args, **kwargs):\n    info = _original_lstat(path, *args, **kwargs)\n    if str(path) == %q:\n        fields = list(info)\n        fields[4] = info.st_uid + 1\n        return os.stat_result(fields)\n    return info\npathlib.Path.lstat = _foreign_lstat\n", target)
				script = strings.Replace(script, "import pathlib\n", inject, 1)
			case "nfs":
				mountinfo := filepath.Join(root, "mountinfo")
				write(mountinfo, "1 0 0:1 / / rw - ext4 /dev/root rw\n2 1 0:2 / "+target+" rw - nfs4 server:/shared rw\n", 0600)
				script = strings.Replace(script, `pathlib.Path("/proc/self/mountinfo")`, fmt.Sprintf("pathlib.Path(%q)", mountinfo), 1)
			}
			before := reloadRuntimeSnapshot(t, root)
			command := exec.Command("bash", "-c", script)
			command.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if strings.Contains(string(output), "dummy-secret-marker") {
				t.Fatal("credential in diagnostics")
			}
			if !success {
				if err == nil {
					t.Fatalf("unsafe output path accepted: %s", output)
				}
				if !reflect.DeepEqual(before, reloadRuntimeSnapshot(t, root)) {
					t.Fatal("unsafe output preflight changed files, directories, or external targets")
				}
				if _, err := os.Stat(filepath.Join(home, "service-calls")); !os.IsNotExist(err) {
					t.Fatal("contacted service before output preflight completed")
				}
				return
			}
			if err != nil {
				t.Fatalf("safe output paths rejected: %v: %s", err, output)
			}
			for _, path := range outputs {
				data, err := os.ReadFile(filepath.Join(home, path))
				if err != nil || !strings.Contains(string(data), "dummy-secret-marker") {
					t.Fatalf("missing replacement: %s: %v", path, err)
				}
				info, err := os.Stat(filepath.Join(home, path))
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("unsafe output mode: %s", path)
				}
			}
			for _, dir := range []string{filepath.Join(home, ".config"), filepath.Join(home, ".config/bridgectl"), filepath.Join(home, ".config/environment.d")} {
				info, err := os.Stat(dir)
				want := os.FileMode(0755)
				if tc.kind == "missing" {
					want = 0700
				}
				if err != nil || info.Mode().Perm() != want {
					t.Fatalf("unexpected directory mode for %s: %v", dir, err)
				}
			}
		})
	}
}
