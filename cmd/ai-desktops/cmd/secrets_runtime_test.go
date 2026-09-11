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

// Exercise the generated remote script with dummy credentials and commands;
// no AWS requests or real user services are involved.
func TestSecretsReloadRuntime(t *testing.T) {
	for _, tc := range []struct {
		name       string
		aws        string
		fail       bool
		manualAuth bool
		splitHomes bool
		inactive   bool
		shellValue string
	}{
		{name: "rotates-all-surfaces"},
		{name: "accepts-native-api-seed", aws: `printf '%s' '{"CODEX_AUTH":{"auth_mode":"apikey","OPENAI_API_KEY":"new-key"},"NEW_KEY":"new"}'`},
		{name: "accepts-legacy-account-seed", aws: `printf '%s' '{"CODEX_AUTH":{"tokens":{"access_token":"new-access","refresh_token":"new-refresh"}},"NEW_KEY":"new"}'`},
		{name: "reports-preserved-inactive-service", inactive: true},
		{name: "interactive-shell-preserves-literal-secret", aws: "printf '%s' '{\"NEW_KEY\":\"wow!missing_event$literal\"}'", shellValue: "wow!missing_event$literal"},
		{name: "rotates-overridden-homes-from-every-surface", splitHomes: true},
		{name: "fetch-failure-preserves-credentials", aws: "exit 1", fail: true},
		{name: "malformed-json-preserves-credentials", aws: "printf '%s' '{broken'", fail: true},
		{name: "empty-json-preserves-credentials", aws: "printf '%s' '{}'", fail: true},
		{name: "partial-fetch-preserves-credentials", aws: "if [ \"$6\" = /test/second ]; then exit 1; fi\nprintf '%s' '{\"NEW_KEY\":\"new\"}'", fail: true},
		{name: "tilde-home-rejected-before-replacement", aws: "printf '%s' '{\"CODEX_HOME\":\"~/codex\",\"NEW_KEY\":\"new\"}'", fail: true},
		{name: "relative-home-rejected-before-replacement", aws: "printf '%s' '{\"CODEX_HOME\":\"codex\",\"NEW_KEY\":\"new\"}'", fail: true},
		{name: "unrelated-secrets-preserve-manual-login", aws: "printf '%s' '{\"NEW_KEY\":\"new\"}'", manualAuth: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			bin := t.TempDir()
			write := func(path, contents string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), mode); err != nil {
					t.Fatal(err)
				}
			}
			agents := filepath.Join(home, ".config/bridgectl/agents.env")
			shell := filepath.Join(home, ".desktop-secrets")
			auth := filepath.Join(home, ".config/bridgectl/codex-home/auth.json")
			write(agents, "CODEX_AUTH='old'\nREMOVED_KEY='old'\n", 0o600)
			write(shell, "export CODEX_AUTH='old'\n", 0o600)
			write(auth, `{"tokens":{"access_token":"refreshed"}}`, 0o600)
			authFiles := []string{auth}
			if tc.splitHomes {
				for _, surface := range []string{agents, filepath.Join(home, ".config/environment.d/desktop-secrets.conf"), shell} {
					dir := filepath.Join(home, "private-"+filepath.Base(surface))
					write(surface, "CODEX_AUTH='old'\nCODEX_HOME='"+dir+"'\n", 0o600)
					cache := filepath.Join(dir, "auth.json")
					write(cache, `{"tokens":{"access_token":"refreshed"}}`, 0o600)
					authFiles = append(authFiles, cache)
				}
			}
			if tc.manualAuth {
				write(agents, "REMOVED_KEY='old'\n", 0o600)
				write(shell, "export REMOVED_KEY='old'\n", 0o600)
			}
			aws := "#!/bin/sh\nprintf '%s' '{\"CODEX_AUTH\":{\"auth_mode\":\"chatgpt\",\"tokens\":{\"access_token\":\"new-access\",\"refresh_token\":\"new-refresh\"}},\"NEW_KEY\":\"new\"}'\n"
			if tc.aws != "" {
				aws = "#!/bin/sh\n" + tc.aws + "\n"
			}
			write(filepath.Join(bin, "aws"), aws, 0o700)
			systemctl := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/service-calls\"\n"
			systemctl += "if [ \"$2\" = show ]; then printf 'loaded\\n'; fi\n"
			if tc.inactive {
				systemctl += "if [ \"$2\" = is-active ]; then exit 3; fi\n"
			}
			write(filepath.Join(bin, "systemctl"), systemctl, 0o700)
			c := exec.Command("bash", "-c", buildSecretsReloadScript([]string{"/test/agents", "/test/second"}, "us-east-2"))
			c.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
			out, err := c.CombinedOutput()
			if tc.fail {
				if err == nil {
					t.Fatalf("fetch failure reported success: %s", out)
				}
				for _, path := range []string{agents, shell, auth} {
					b, e := os.ReadFile(path)
					want := "old"
					if path == auth {
						want = "refreshed"
					}
					if e != nil || !strings.Contains(string(b), want) {
						t.Fatalf("changed %s on fetch failure", path)
					}
				}
				if _, e := os.Stat(filepath.Join(home, "service-calls")); !os.IsNotExist(e) {
					t.Fatal("touched service before all credentials fetched")
				}
				return
			}
			if err != nil {
				t.Fatalf("reload: %v: %s", err, out)
			}
			if tc.shellValue != "" {
				check := exec.Command("bash", "--noprofile", "--norc", "-ic", `set -H; source "$HOME/.desktop-secrets"; printf '%s' "$NEW_KEY"`)
				check.Env = c.Env
				value, e := check.Output()
				if e != nil || string(value) != tc.shellValue {
					t.Fatalf("interactive shell changed dummy secret: %q; error: %v", value, e)
				}
			}
			for _, path := range []string{agents, shell, filepath.Join(home, ".config/environment.d/desktop-secrets.conf")} {
				b, e := os.ReadFile(path)
				if e != nil || !strings.Contains(string(b), "NEW_KEY") || strings.Contains(string(b), "REMOVED_KEY") {
					t.Fatalf("credential surface not rotated: %s", path)
				}
				info, _ := os.Stat(path)
				if info.Mode().Perm() != 0o600 {
					t.Fatalf("unsafe permissions: %s", path)
				}
			}
			if _, e := os.Stat(auth); tc.manualAuth {
				if e != nil {
					t.Fatal("unrelated reload deleted manual login")
				}
			} else if !os.IsNotExist(e) {
				t.Fatal("managed auth cache was not invalidated")
			}
			if !tc.manualAuth {
				for _, cache := range authFiles {
					if _, e := os.Stat(cache); !os.IsNotExist(e) {
						t.Fatalf("prior auth cache was not invalidated: %s", cache)
					}
				}
			}
			calls, _ := os.ReadFile(filepath.Join(home, "service-calls"))
			stop, start := strings.Index(string(calls), "stop bridgectl"), strings.Index(string(calls), "start bridgectl")
			if tc.inactive {
				if stop >= 0 || start >= 0 || !strings.Contains(string(out), "Bridge was inactive and remains stopped") {
					t.Fatalf("inactive state must be preserved and reported: %s; calls: %s", out, calls)
				}
				return
			}
			if stop < 0 || start <= stop {
				t.Fatalf("must stop old providers then start with new creds: %s", calls)
			}
		})
	}
}

// Capture all credential contents, directory/file modes and symlink targets,
// excluding the dummy systemctl log used to distinguish reads from mutations.
func reloadRuntimeSnapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(home, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Base(path) == "service-calls" {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		content := ""
		if info.Mode()&os.ModeSymlink != 0 {
			content, err = os.Readlink(path)
		} else if info.Mode().IsRegular() {
			var data []byte
			data, err = os.ReadFile(path)
			content = string(data)
		}
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%v:%s", info.Mode(), content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSecretsReloadPreflightPreservesRuntime(t *testing.T) {
	for _, scenario := range []string{
		"public-managed-directory", "public-native-directory", "public-explicit-directory",
		"public-auth-file", "auth-file-symlink", "auth-file-hardlink", "auth-file-directory", "auth-home-file",
		"nfs-auth-directory", "nfs-bind-mounted-auth-file", "unknown-auth-filesystem",
		"foreign-owner", "unknown-unit", "unit-query-failed", "loaded-unit-unknown-active-state",
		"malformed-auth-seed", "incomplete-auth-seed", "empty-auth-object", "invalid-token-type",
	} {
		t.Run(scenario, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			write := func(path, contents string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), mode); err != nil {
					t.Fatal(err)
				}
			}
			authDir := filepath.Join(home, ".config/bridgectl/codex-home")
			authFile := filepath.Join(authDir, "auth.json")
			write(authFile, `{"tokens":{"access_token":"preserve-refreshed"}}`, 0o600)
			write(filepath.Join(home, ".config/bridgectl/agents.env"), "CODEX_AUTH='old'\n", 0o600)
			write(filepath.Join(home, ".config/environment.d/desktop-secrets.conf"), "CODEX_AUTH='old'\n", 0o600)
			write(filepath.Join(home, ".desktop-secrets"), "export CODEX_AUTH='old'\n", 0o600)
			write(filepath.Join(home, ".bashrc"), "# preserve shell configuration\n", 0o600)
			secret := `{"CODEX_AUTH":{"auth_mode":"chatgpt","tokens":{"access_token":"new-access","refresh_token":"new-refresh"}}}`
			loadState, serviceExit := "loaded", "0"
			activeExit := "0"
			mountInfoPath := ""
			switch scenario {
			case "public-managed-directory":
				if err := os.Chmod(authDir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "public-native-directory", "public-explicit-directory":
				dir := filepath.Join(home, ".codex")
				if scenario == "public-explicit-directory" {
					dir = filepath.Join(home, "explicit-codex")
					write(filepath.Join(home, ".desktop-secrets"), "export CODEX_AUTH='old'\nexport CODEX_HOME='"+dir+"'\n", 0o600)
				}
				write(filepath.Join(dir, "auth.json"), "preserve-native", 0o600)
				if err := os.Chmod(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "public-auth-file":
				if err := os.Chmod(authFile, 0o644); err != nil {
					t.Fatal(err)
				}
			case "auth-file-symlink":
				target := filepath.Join(home, "preserve-auth-target")
				if err := os.Rename(authFile, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, authFile); err != nil {
					t.Fatal(err)
				}
			case "auth-file-hardlink":
				if err := os.Link(authFile, filepath.Join(home, "preserve-linked-auth")); err != nil {
					t.Fatal(err)
				}
			case "nfs-auth-directory", "nfs-bind-mounted-auth-file", "unknown-auth-filesystem":
				mountInfoPath = filepath.Join(home, "synthetic-mountinfo")
				mountPoint := authDir
				if scenario == "nfs-bind-mounted-auth-file" {
					mountPoint = authFile
				}
				mountInfo := "1 0 0:1 / / rw - ext4 /dev/root rw\n2 1 0:2 / " + mountPoint + " rw - nfs4 server:/export rw\n"
				if scenario == "unknown-auth-filesystem" {
					mountInfo = ""
				}
				write(mountInfoPath, mountInfo, 0o600)
			case "auth-file-directory":
				if err := os.Rename(authFile, filepath.Join(home, "preserve-auth")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(authFile, 0o700); err != nil {
					t.Fatal(err)
				}
			case "auth-home-file":
				if err := os.Rename(authDir, filepath.Join(home, "preserve-auth-dir")); err != nil {
					t.Fatal(err)
				}
				write(authDir, "preserve-ordinary-file", 0o600)
			case "unknown-unit":
				loadState = "not-found"
			case "unit-query-failed":
				serviceExit = "1"
			case "loaded-unit-unknown-active-state":
				activeExit = "4"
			case "malformed-auth-seed":
				secret = `{"CODEX_AUTH":"invalid-secret-marker","OPENAI_API_KEY":"fallback-does-not-authorize-cache-deletion"}`
			case "incomplete-auth-seed":
				secret = `{"CODEX_AUTH":{"auth_mode":"chatgpt","tokens":{"access_token":"incomplete-secret-marker"}}}`
			case "empty-auth-object":
				secret = `{"CODEX_AUTH":{}}`
			case "invalid-token-type":
				secret = `{"CODEX_AUTH":{"tokens":{"access_token":123,"refresh_token":"refresh"}}}`
			}
			write(filepath.Join(bin, "aws"), "#!/bin/sh\nprintf '%s' '"+secret+"'\n", 0o700)
			write(filepath.Join(bin, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/service-calls\"\nif [ \"$2\" = show ]; then printf '"+loadState+"\\n'; exit "+serviceExit+"; fi\nif [ \"$2\" = is-active ]; then exit "+activeExit+"; fi\n", 0o700)
			before := reloadRuntimeSnapshot(t, home)
			script := buildSecretsReloadScript([]string{"/test/agents"}, "us-east-2")
			if scenario == "foreign-owner" {
				// Model another desktop UID without chown/root requirements.
				script = strings.Replace(script, "import os\n", "import os\nos.getuid = lambda: -1\n", 1)
			}
			if mountInfoPath != "" {
				script = strings.Replace(script, `pathlib.Path("/proc/self/mountinfo")`, `pathlib.Path("`+mountInfoPath+`")`, 1)
			}
			command := exec.Command("bash", "-c", script)
			command.Env = append(os.Environ(), "HOME="+home, "PATH="+bin+":"+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("unsafe preflight succeeded: %s", output)
			}
			if strings.Contains(string(output), "secret-marker") {
				t.Fatal("credential appeared in diagnostics")
			}
			if after := reloadRuntimeSnapshot(t, home); !reflect.DeepEqual(before, after) {
				t.Fatal("preflight failure modified credential files, caches, permissions, or shell config")
			}
			calls, _ := os.ReadFile(filepath.Join(home, "service-calls"))
			for _, mutation := range []string{"stop bridgectl", "start bridgectl", "unset-environment", "daemon-reload"} {
				if strings.Contains(string(calls), mutation) {
					t.Fatalf("preflight failure mutated service: %s", calls)
				}
			}
		})
	}
}
