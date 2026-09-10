package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestGitHubRepo(t *testing.T) {
	for _, origin := range []string{"git@github.com:markcallen/ai-desktops.git", "https://github.com/markcallen/ai-desktops.git", "ssh://git@github.com/markcallen/ai-desktops"} {
		got, err := githubRepo(origin)
		if err != nil || got != "markcallen/ai-desktops" {
			t.Fatalf("%q: %q %v", origin, got, err)
		}
	}
	for _, origin := range []string{"https://evil.example/owner/ai-desktops", "git@github.com:owner/pilot.git", ""} {
		if _, err := githubRepo(origin); err == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
}

func TestCanonicalRepos(t *testing.T) {
	s, f := fixture(t)
	f.w.Repos = []string{"github.com/" + s.Repo}
	f.d.Repos = []string{"github.com/" + s.Repo}
	if err := validateResources(context.Background(), f, s); err != nil {
		t.Fatal(err)
	}
}

func TestResumeWorkspaceOnly(t *testing.T) {
	s, owned := fixture(t)
	s.DesktopID = ""
	s.DesktopCreated = false
	s.StartedAt = "2026-01-01T00:00:00Z"
	owned.w.AttachedDesktopID = ""
	owned.w.State = store.WorkspaceStateAvailable
	f := &commandFixture{t: t, w: owned.w}
	c := &cli{binary: "test-cli", state: s, exec: f.execute}
	if err := resume(context.Background(), c, s); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.events, []string{"desktop-create"}) {
		t.Fatalf("events %v", f.events)
	}
	if !s.DesktopCreated || s.DesktopID != "d-created" {
		t.Fatalf("desktop identity not persisted: %+v", s)
	}
	loaded, err := loadState(s.Path)
	if err != nil || loaded.DesktopID != s.DesktopID {
		t.Fatalf("saved state: %+v %v", loaded, err)
	}
}

func TestResumeDiscoversOnlyOwnedDesktop(t *testing.T) {
	for _, collision := range []bool{false, true} {
		s, owned := fixture(t)
		s.StartedAt = "2026-01-01T00:00:00Z"
		owned.d.CreatedAt = "2026-02-01T00:00:00Z"
		s.DesktopID = ""
		s.DesktopCreated = false
		if collision {
			owned.d.WorkspaceID = "ws-unrelated"
		}
		f := &commandFixture{t: t, w: owned.w, d: owned.d}
		c := &cli{binary: "test-cli", state: s, exec: f.execute}
		err := resume(context.Background(), c, s)
		if (err != nil) != collision {
			t.Fatalf("collision %v: %v", collision, err)
		}
		if len(f.events) > 0 {
			t.Fatalf("unexpected mutations: %v", f.events)
		}
	}
}

type commandFixture struct {
	t      *testing.T
	w      store.Workspace
	d      store.Desktop
	fail   string
	events []string
}

func argValue(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *commandFixture) execute(_ context.Context, program string, args []string, _ []byte) ([]byte, error) {
	f.t.Helper()
	encode := func(v any) ([]byte, error) { return json.Marshal(v) }
	if program == "git" {
		return []byte("git@github.com:markcallen/ai-desktops.git\n"), nil
	}
	if program == "ssh" {
		command := args[len(args)-1]
		if strings.Contains(command, "codex-test.py'") && !strings.Contains(command, "dd of=") {
			for _, action := range []string{"prepare", "first", "second", "reloaded", "third"} {
				if strings.HasSuffix(command, shellQuote(action)) {
					f.events = append(f.events, action)
					if f.fail == action {
						return nil, errors.New("auth failed")
					}
					return []byte("PASS " + action + "\n"), nil
				}
			}
		}
		return nil, nil
	}
	if program != "test-cli" {
		f.t.Fatalf("unexpected command %s %v", program, args)
	}
	switch args[0] {
	case "workspace":
		switch args[1] {
		case "create":
			f.events = append(f.events, "workspace-create")
			f.w = store.Workspace{WorkspaceID: "ws-created", WorkspaceName: argValue(args, "--name"), Environment: "dev", GitHubOwner: argValue(args, "--github-owner"), Repos: []string{"github.com/" + argValue(args, "--repo")}, EFSAccessPointID: "fsap-created"}
			return encode(f.w)
		case "status":
			return encode(f.w)
		case "delete":
			f.events = append(f.events, "workspace-delete")
			return nil, nil
		}
	case "create":
		f.events = append(f.events, "desktop-create")
		f.d = store.Desktop{DesktopID: "d-created", DesktopName: argValue(args, "--name"), WorkspaceID: f.w.WorkspaceID, WorkspaceName: f.w.WorkspaceName, Environment: "dev", GitHubOwner: f.w.GitHubOwner, Repos: f.w.Repos, State: store.StateReady, SSHTarget: "ubuntu@test.example", CreatedAt: "9999-12-31"}
		f.w.AttachedDesktopID = f.d.DesktopID
		if f.fail == "create" {
			return nil, errors.New("partial create failure")
		}
		return encode(map[string]string{"desktop_id": f.d.DesktopID, "avd_names": "", "nested_virtualization": "false"})
	case "status":
		return encode(f.d)
	case "list":
		return encode([]store.Desktop{f.d})
	case "secrets":
		f.events = append(f.events, "reload")
		return nil, nil
	case "terminate":
		f.events = append(f.events, "terminate")
		f.d.State = store.StateTerminated
		f.w.AttachedDesktopID = ""
		return nil, nil
	}
	f.t.Fatalf("unexpected CLI %v", args)
	return nil, nil
}

func TestCommandFlow(t *testing.T) {
	for _, tt := range []struct {
		name, fail string
		keep       bool
	}{
		{"success", "", false}, {"keep", "", true}, {"auth_failure", "first", false}, {"partial_create", "create", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := filepath.Join(dir, "config.yaml")
			key := filepath.Join(dir, "key")
			binary := filepath.Join(dir, "bridgectl")
			stateFile := filepath.Join(dir, "state.json")
			for path, data := range map[string]string{cfg: "aws:\n  region: us-west-2\n  profile: e2e-profile\n", key: "unused fixture key", binary: "\x7fELFfake"} {
				if err := os.WriteFile(path, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f := &commandFixture{t: t, fail: tt.fail}
			previous := runCommand
			runCommand = f.execute
			t.Cleanup(func() { runCommand = previous })
			args := []string{"--cli", "test-cli", "--config", cfg, "--ssh-key", key, "--state", stateFile, "--bridgectl-binary", binary}
			if tt.keep {
				args = append(args, "--keep")
			}
			err := run(context.Background(), args)
			if (err != nil) != (tt.fail != "") {
				t.Fatalf("error: %v", err)
			}
			s, err := loadState(stateFile)
			if err != nil {
				t.Fatal(err)
			}
			if s.Region != "us-west-2" || s.Profile != "e2e-profile" || s.Repo != "markcallen/ai-desktops" {
				t.Fatalf("identity not frozen: %+v", s)
			}
			if s.DesktopID != "d-created" {
				t.Fatal("partial resource ID lost")
			}
			wantCleanup := tt.fail == "" && !tt.keep
			if s.DesktopTerminated != wantCleanup || s.WorkspaceDeleted != wantCleanup {
				t.Fatalf("cleanup: %+v", s)
			}
			if wantCleanup {
				want := []string{"workspace-create", "desktop-create", "prepare", "first", "second", "reload", "reloaded", "third", "terminate", "workspace-delete"}
				if !reflect.DeepEqual(f.events, want) {
					t.Fatalf("events %v", f.events)
				}
			}
			if tt.keep {
				f.events = nil
				if err = run(context.Background(), []string{"--cli", "test-cli", "--reuse", stateFile, "--keep"}); err != nil {
					t.Fatal(err)
				}
				for _, event := range f.events {
					if strings.HasSuffix(event, "create") || event == "terminate" {
						t.Fatal(f.events)
					}
				}
				if err = run(context.Background(), []string{"--cli", "test-cli", "--cleanup", stateFile}); err != nil {
					t.Fatal(err)
				}
				if err = run(context.Background(), []string{"--cli", "test-cli", "--cleanup", stateFile}); err != nil {
					t.Fatal("idempotent cleanup:", err)
				}
			}
		})
	}
}

func TestInvalidOptionsHaveNoSideEffects(t *testing.T) {
	previous := runCommand
	runCommand = func(context.Context, string, []string, []byte) ([]byte, error) {
		t.Fatal("unexpected command")
		return nil, nil
	}
	t.Cleanup(func() { runCommand = previous })
	for _, args := range [][]string{{}, {"--scenario", "unknown"}, {"--reuse", "a", "--cleanup", "b"}, {"unexpected"}, {"--unknown"}, {"--repo", "evil/repo", "--bridgectl-binary", "x"}} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestStateValidationAndSave(t *testing.T) {
	s, _ := fixture(t)
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, loaded) {
		t.Fatalf("roundtrip differs: %+v", loaded)
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if _, err = loadState(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing state accepted")
	}
	if err = os.WriteFile(s.Path, []byte("bad json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadState(s.Path); err == nil {
		t.Fatal("bad state accepted")
	}
	s.WorkspaceID = ""
	if err = s.validate(); err == nil {
		t.Fatal("missing ownership IDs accepted")
	}
}

func TestShellQuote(t *testing.T) {
	for _, input := range []string{"simple", "space value", "$(not-executed)", "a'b"} {
		b, err := execute(context.Background(), "sh", []string{"-c", "printf '%s' " + shellQuote(input)}, nil)
		if err != nil || string(b) != input {
			t.Fatalf("quote %q: %q %v", input, b, err)
		}
	}
	if _, err := execute(context.Background(), "sh", []string{"-c", "echo TOPSECRET >&2; exit 1"}, nil); err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Fatal(err)
	}
}

func TestFailureCategoriesDoNotExposeDiagnosticValues(t *testing.T) {
	for _, tt := range []struct{ diagnostic, category string }{
		{"Repository not found SECRET", "repository_access"},
		{"GetSecretValue ResourceNotFoundException SECRET", "secret_lookup"},
		{"unknown flag --SECRET", "invalid_options"},
		{"render cloud-init: SECRET", "cloud_init_render"},
		{"read foundation stack outputs SECRET", "foundation_unavailable"},
		{"repo set does not match SECRET", "workspace_conflict"},
		{"no active AMI SECRET", "configuration_missing"},
		{"AccessDenied SECRET", "access_denied"},
		{"no such host SECRET", "network_unreachable"},
		{"create fleet record SECRET", "fleet_record_write"},
		{"user data is limited to 16384 bytes SECRET", "user_data_too_large"},
		{"unexpected SECRET", "command_failed"},
	} {
		if got := failureCategory(tt.diagnostic); got != tt.category || strings.Contains(got, "SECRET") {
			t.Fatalf("category %q, want %q", got, tt.category)
		}
	}
}
