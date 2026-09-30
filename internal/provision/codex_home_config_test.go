package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexHomeConfigScript(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
		want    string
	}{
		{"new", "", "check_for_update_on_startup = false\n"},
		{"existing keys and table", "model = \"gpt-5\"\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n", "model = \"gpt-5\"\ncheck_for_update_on_startup = false\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n"},
		{"replace enabled option", "model = \"gpt-5\"\ncheck_for_update_on_startup = true\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n", "model = \"gpt-5\"\ncheck_for_update_on_startup = false\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n"},
		{"existing key without final newline", "model = \"gpt-5\"", "model = \"gpt-5\"\ncheck_for_update_on_startup = false\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "codex-home", "config.toml")
			if tc.initial != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.initial), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				cmd := exec.Command("python3", "codex_home_config.py", path)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("run %d: %v: %s", i, err, out)
				}
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Fatalf("run %d: got %q, want %q", i, got, tc.want)
				}
			}
		})
	}
}

func TestCodexHomeConfigScriptRejectsInvalidOrSymlinkConfig(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "codex-home", "config.toml")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(filepath.Dir(path), "original.toml")
		if err := os.WriteFile(original, []byte("broken = [\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if symlink {
			if err := os.Symlink(original, path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("broken = [\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python3", "codex_home_config.py", path)
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("expected failure for symlink=%v: %s", symlink, out)
		}
		got, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), "broken = [") {
			t.Fatalf("original modified: %q", got)
		}
	}
}

func TestRenderCloudInit_configuresCodexUpdateCheck(t *testing.T) {
	for _, preinstalled := range []bool{false, true} {
		out, err := RenderCloudInit(&BootstrapConfig{DesktopID: "d-codex-update", Hostname: "d-codex-update.example.com", GitHubOwner: "acme", PackagesPreInstalled: preinstalled})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"codex_home_config.py", "check_for_update_on_startup", "/home/ubuntu/.config/bridgectl/codex-home/config.toml"} {
			if !strings.Contains(out, want) {
				t.Fatalf("preinstalled=%v: cloud-init missing %q", preinstalled, want)
			}
		}
	}
}
