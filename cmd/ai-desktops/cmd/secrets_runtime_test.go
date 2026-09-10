package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
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
			aws := "#!/bin/sh\nprintf '%s' '{\"CODEX_AUTH\":\"{\\\"auth_mode\\\":\\\"chatgpt\\\"}\",\"NEW_KEY\":\"new\"}'\n"
			if tc.aws != "" {
				aws = "#!/bin/sh\n" + tc.aws + "\n"
			}
			write(filepath.Join(bin, "aws"), aws, 0o700)
			systemctl := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HOME/service-calls\"\n"
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
