// E2E is opt-in: go test runs only offline safety tests; go run provisions AWS.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/store"
)

//go:embed remote_codex.py
var scripts embed.FS

type executor func(context.Context, string, []string, []byte) ([]byte, error)

var runCommand executor = execute

// Do not include captured stderr/stdout in errors: providers may print auth.
func execute(ctx context.Context, program string, args []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%s failed: %s (%w); raw output withheld to protect credentials", filepath.Base(program), failureCategory(stdout.String()+"\n"+stderr.String()), err)
	}
	return stdout.Bytes(), nil
}

func failureCategory(output string) string {
	output = strings.ToLower(output)
	for _, entry := range []struct {
		category string
		patterns []string
	}{
		{"cloud_init_render", []string{"render cloud-init", "compress cloud-init user-data"}},
		{"user_data_too_large", []string{"user data is limited", "user data size", "user data exceeds", "user-data exceeds"}},
		{"foundation_unavailable", []string{"read foundation stack outputs", "foundation stack incomplete"}},
		{"workspace_conflict", []string{"repo set does not match", "is already attached", "check desktop name availability", "is already in use by desktop", "missing efs attachment metadata"}},
		{"fleet_record_write", []string{"create fleet record", "update fleet record", "mark ready"}},
		{"repository_access", []string{"repository not found", "could not read from remote repository", "repository access", "authentication failed for 'https://github.com"}},
		{"secret_lookup", []string{"resourcenotfoundexception", "secrets manager", "getsecretvalue", "secret not found"}},
		{"invalid_options", []string{"unknown flag", "required flag", "invalid argument", "invalid value"}},
		{"configuration_missing", []string{"not configured", "is not set in config", "no active ami", "no default ami"}},
		{"access_denied", []string{"accessdenied", "permission denied", "unauthorizedoperation"}},
		{"network_unreachable", []string{"no such host", "network is unreachable", "connection timed out"}},
	} {
		for _, pattern := range entry.patterns {
			if strings.Contains(output, pattern) {
				return entry.category
			}
		}
	}
	return "command_failed"
}

type cli struct {
	binary string
	state  *state
	exec   executor
}

func (c *cli) call(ctx context.Context, args ...string) ([]byte, error) {
	args = append(args, "--config", c.state.Config)
	if c.state.Profile != "" {
		args = append(args, "--profile", c.state.Profile)
	}
	if c.state.Region != "" {
		args = append(args, "--region", c.state.Region)
	}
	return c.exec(ctx, c.binary, args, nil)
}
func (c *cli) Workspace(ctx context.Context, name string) (store.Workspace, error) {
	var w store.Workspace
	b, e := c.call(ctx, "workspace", "status", name, "--env", c.state.Environment, "--json")
	if e == nil {
		e = json.Unmarshal(b, &w)
	}
	return w, e
}
func (c *cli) Desktop(ctx context.Context, id string) (store.Desktop, error) {
	var d store.Desktop
	b, e := c.call(ctx, "status", id, "--json")
	if e == nil {
		e = json.Unmarshal(b, &d)
	}
	return d, e
}
func (c *cli) Terminate(ctx context.Context, id string) error {
	logStage("terminate " + id)
	_, e := c.call(ctx, "terminate", id, "--force")
	return e
}
func (c *cli) DeleteWorkspace(ctx context.Context, name string) error {
	logStage("delete workspace " + name)
	_, e := c.call(ctx, "workspace", "delete", name, "--env", c.state.Environment)
	return e
}
func logStage(stage string) {
	fmt.Fprintf(os.Stderr, "e2e %s: %s\n", time.Now().UTC().Format(time.RFC3339), stage)
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "E2E FAIL:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("e2e", flag.ContinueOnError)
	binary := flags.String("cli", "./ai-desktops", "locally built ai-desktops binary")
	bridge := flags.String("bridgectl-binary", "", "locally built Linux amd64 bridgectl to install on the test desktop (required for a new run)")
	configPath := flags.String("config", "", "ai-desktops config (defaults to the normal operator config)")
	profile := flags.String("profile", "", "AWS profile override")
	region := flags.String("region", "", "AWS region override")
	repo := flags.String("repo", "", "GitHub owner/ai-desktops (defaults to this checkout's origin)")
	key := flags.String("ssh-key", "", "SSH private key (defaults to desktop.ssh_key_path in config)")
	statePath := flags.String("state", "", "new state file; defaults to a unique directory under /tmp")
	reuse := flags.String("reuse", "", "reuse resources from this runner's state file")
	clean := flags.String("cleanup", "", "terminate/delete only resources owned by this runner state file")
	keep := flags.Bool("keep", false, "retain desktop and workspace after success (failures always retain)")
	scenario := flags.String("scenario", "codex-auth", "scenario to run (codex-auth)")
	timeout := flags.Duration("timeout", 30*time.Minute, "maximum time for create/readiness/scenario; cleanup gets a separate 15 minutes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *reuse != "" && *clean != "" {
		return errors.New("--reuse and --cleanup are mutually exclusive")
	}
	if *scenario != "codex-auth" {
		return fmt.Errorf("unknown scenario %q", *scenario)
	}
	if *reuse == "" && *clean == "" && *bridge == "" {
		return errors.New("--bridgectl-binary is required so new runs test the auth fix")
	}
	var s *state
	var err error
	if *reuse != "" || *clean != "" {
		p := *reuse
		if *clean != "" {
			p = *clean
		}
		s, err = loadState(p)
		if err != nil {
			return err
		}
		if *configPath != "" || *profile != "" || *region != "" || *statePath != "" || *repo != "" {
			return errors.New("reuse/cleanup use the persisted config/profile/region; do not override resource identity")
		}
		if *key != "" {
			s.SSHKey = *key
		}
	} else {
		if *repo == "" {
			origin, e := runCommand(ctx, "git", []string{"remote", "get-url", "origin"}, nil)
			if e != nil {
				return fmt.Errorf("read this checkout's origin: %w", e)
			}
			*repo, e = githubRepo(string(origin))
			if e != nil {
				return e
			}
		}
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/ai-desktops$`).MatchString(*repo) {
			return errors.New("--repo must identify a GitHub owner/ai-desktops checkout")
		}
		if *configPath == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return e
			}
			*configPath = filepath.Join(home, ".ai-desktops", "config.yaml")
		}
		*configPath, err = filepath.Abs(*configPath)
		if err != nil {
			return err
		}
		cfg, e := config.LoadOrDefault(*configPath)
		if e != nil {
			return e
		}
		if *region == "" {
			*region = cfg.AWS.Region
		}
		if *profile == "" {
			*profile = cfg.AWS.Profile
		}
		if *key == "" {
			*key = cfg.Desktop.SSHKeyPath
		}
		if *key == "" {
			return errors.New("SSH key is required via config or --ssh-key")
		}
		if _, e = os.Stat(*key); e != nil {
			return e
		}
		var id [8]byte
		if _, e = rand.Read(id[:]); e != nil {
			return e
		}
		runID := hex.EncodeToString(id[:])
		if *statePath == "" {
			dir, e := os.MkdirTemp("", "ai-desktops-e2e-")
			if e != nil {
				return e
			}
			*statePath = filepath.Join(dir, "state.json")
		}
		// Exclusive reservation prevents accidentally replacing another run's manifest.
		f, e := os.OpenFile(*statePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		f.Close()
		s = &state{Version: 1, RunID: runID, Name: "e2e-ai-desktops-" + runID, Environment: "dev", Owner: strings.Split(*repo, "/")[0], Repo: *repo, StartedAt: time.Now().UTC().Format(time.RFC3339), Config: *configPath, Profile: *profile, Region: *region, SSHKey: *key, Path: *statePath, Result: "creating"}
		if err = s.save(); err != nil {
			return err
		}
	}
	logStage("state " + s.Path + "; workspace " + s.Name)
	c := &cli{binary: *binary, state: s, exec: runCommand}
	defer func() {
		fmt.Fprintf(os.Stderr, "Resources: desktop=%s workspace=%s state=%s\n", s.DesktopID, s.Name, s.Path)
	}()
	if *clean != "" {
		cleanupCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		return cleanup(cleanupCtx, c, s)
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if *reuse != "" {
		if !s.WorkspaceCreated || s.DesktopTerminated || s.WorkspaceDeleted {
			return errors.New("state has no reusable desktop/workspace")
		}
		if !s.DesktopCreated && *bridge == "" {
			return errors.New("--bridgectl-binary is required when resuming a workspace to create its desktop")
		}
		err = resume(ctx, c, s)
	} else {
		err = provision(ctx, c, s)
	}
	if err == nil {
		err = exercise(ctx, c, *bridge, *scenario)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cleanupCancel()
	if e := finish(cleanupCtx, c, s, *keep, err); e != nil {
		return e
	}
	logStage("PASS " + *scenario)
	return nil
}

func githubRepo(origin string) (string, error) {
	origin = strings.TrimSpace(origin)
	for _, prefix := range []string{"git@github.com:", "https://github.com/", "ssh://git@github.com/"} {
		if strings.HasPrefix(origin, prefix) {
			repo := strings.TrimSuffix(strings.TrimPrefix(origin, prefix), ".git")
			if regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/ai-desktops$`).MatchString(repo) {
				return repo, nil
			}
		}
	}
	return "", errors.New("origin must identify this GitHub ai-desktops repository; specify --repo owner/ai-desktops")
}

func provision(ctx context.Context, c *cli, s *state) error {
	logStage("create EFS workspace " + s.Name)
	b, err := c.call(ctx, "workspace", "create", "--name", s.Name, "--env", s.Environment, "--github-owner", s.Owner, "--repo", s.Repo, "--json")
	var w store.Workspace
	if err == nil {
		err = json.Unmarshal(b, &w)
	}
	if err != nil {
		return fmt.Errorf("workspace create failed; inspect exact workspace %s before retrying: %w", s.Name, err)
	}
	s.WorkspaceID = w.WorkspaceID
	s.AccessPointID = w.EFSAccessPointID
	s.WorkspaceCreated = true
	if err = s.save(); err != nil {
		return err
	}
	return createDesktop(ctx, c, s)
}

// resume never creates a second workspace. It can recover a desktop whose CLI
// process exited before its ID reached the manifest, but only when its exact
// name, creation time, owner, repository, and owned workspace agree.
func resume(ctx context.Context, c *cli, s *state) error {
	if !s.DesktopCreated {
		all, err := c.call(ctx, "list", "--all", "--json")
		if err != nil {
			return err
		}
		var candidates []store.Desktop
		if err = json.Unmarshal(all, &candidates); err != nil {
			return err
		}
		var found *store.Desktop
		for _, candidate := range candidates {
			if candidate.DesktopName != s.Name || candidate.Environment != s.Environment {
				continue
			}
			if found != nil || candidate.WorkspaceID != s.WorkspaceID || candidate.WorkspaceName != s.Name || candidate.GitHubOwner != s.Owner || !sameRepo(candidate.Repos, s.Repo) || s.StartedAt == "" || candidate.CreatedAt < s.StartedAt || candidate.State == store.StateTerminated {
				return errors.New("desktop name collision does not identify one live desktop created for this runner workspace")
			}
			copy := candidate
			found = &copy
		}
		if found != nil {
			// Validate the tentative identity against both live records before
			// saving ownership or touching resources.
			tentative := *s
			tentative.DesktopID = found.DesktopID
			tentative.DesktopCreated = true
			if err = validateResources(ctx, c, &tentative); err != nil {
				return err
			}
			*s = tentative
			if err = s.save(); err != nil {
				return err
			}
		}
	}
	if err := validateResources(ctx, c, s); err != nil {
		return err
	}
	if !s.DesktopCreated {
		w, err := c.Workspace(ctx, s.Name)
		if err != nil {
			return err
		}
		if w.State != store.WorkspaceStateAvailable {
			return errors.New("runner workspace is not available; inspect its state before retrying desktop creation")
		}
		return createDesktop(ctx, c, s)
	}
	return nil
}

func createDesktop(ctx context.Context, c *cli, s *state) error {
	logStage("create desktop " + s.Name)
	b, err := c.call(ctx, "create", "--env", s.Environment, "--name", s.Name, "--github-owner", s.Owner, "--workspace-mode", "efs", "--workspace-name", s.Name, "--repo", s.Repo, "--instance-type", "m7i.large", "--json")
	var d store.Desktop
	if err == nil {
		// create emits a presentation map (some fields are strings whereas
		// status uses arrays/bools). Decode only the stable identifier here.
		var created struct {
			DesktopID string `json:"desktop_id"`
		}
		err = json.Unmarshal(b, &created)
		d.DesktopID = created.DesktopID
	}
	if err != nil {
		// Creation can fail after its fleet record was saved. Recover only the
		// unique name + exact workspace created in this run; never adopt by repo.
		discoveryCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if all, e := c.call(discoveryCtx, "list", "--all", "--json"); e == nil {
			var candidates []store.Desktop
			if json.Unmarshal(all, &candidates) == nil {
				for _, candidate := range candidates {
					if candidate.DesktopName == s.Name && candidate.WorkspaceID == s.WorkspaceID && candidate.Environment == s.Environment && candidate.CreatedAt >= s.StartedAt {
						d = candidate
						break
					}
				}
			}
		}
	}
	if d.DesktopID != "" {
		s.DesktopID = d.DesktopID
		s.DesktopCreated = true
		if e := s.save(); e != nil {
			return errors.Join(err, e)
		}
	}
	if err != nil {
		return fmt.Errorf("desktop create failed; resources retained: %w", err)
	}
	if s.DesktopID == "" {
		return errors.New("create returned no desktop ID; workspace retained")
	}
	return validateResources(ctx, c, s)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (c *cli) remote(ctx context.Context, host, command string, input []byte) ([]byte, error) {
	return c.exec(ctx, "ssh", []string{"-i", c.state.SSHKey, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + filepath.Join(filepath.Dir(c.state.Path), "known_hosts"), host, command}, input)
}

func exercise(ctx context.Context, c *cli, bridge, scenario string) error {
	d, err := c.Desktop(ctx, c.state.DesktopID)
	if err != nil {
		return err
	}
	host := d.SSHTarget
	if host == "" {
		host = "ubuntu@" + d.Hostname
	}
	if !strings.HasPrefix(host, "ubuntu@") || strings.ContainsAny(host, "\n\r \t") {
		return errors.New("invalid SSH target in desktop record")
	}
	logStage("wait for cloud-init, workspace clone, and bridge")
	for {
		_, err = c.remote(ctx, host, "test -f /var/lib/cloud/instance/boot-finished && test -d /workspace/ai-desktops/.git && XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user is-active --quiet bridgectl", nil)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout: %w", ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}
	if bridge != "" {
		logStage("install branch bridgectl binary")
		data, e := os.ReadFile(bridge)
		if e != nil {
			return e
		}
		if len(data) < 5 || string(data[:4]) != "\x7fELF" {
			return errors.New("bridgectl binary must be a Linux ELF executable")
		}
		_, err = c.remote(ctx, host, "set -e; install -d -m 700 /home/ubuntu/.local/share/ai-desktops-e2e; dd of=/home/ubuntu/.local/share/ai-desktops-e2e/bridgectl status=none; chmod 700 /home/ubuntu/.local/share/ai-desktops-e2e/bridgectl; sudo install -m 755 /home/ubuntu/.local/share/ai-desktops-e2e/bridgectl /usr/local/bin/bridgectl; XDG_RUNTIME_DIR=/run/user/$(id -u) systemctl --user restart bridgectl", data)
		if err != nil {
			return err
		}
	}
	if scenario == "codex-auth" {
		return codexScenario(ctx, c, host)
	}
	return fmt.Errorf("unimplemented scenario %s", scenario)
}

func codexScenario(ctx context.Context, c *cli, host string) (result error) {
	script, err := scripts.ReadFile("remote_codex.py")
	if err != nil {
		return err
	}
	remotePath := "/home/ubuntu/.local/share/ai-desktops-e2e/codex-test.py"
	_, err = c.remote(ctx, host, "install -d -m 700 /home/ubuntu/.local/share/ai-desktops-e2e && dd of="+shellQuote(remotePath)+" status=none", script)
	if err != nil {
		return err
	}
	action := func(action string) error {
		logStage("codex-auth " + action)
		b, e := c.remote(ctx, host, "XDG_RUNTIME_DIR=/run/user/$(id -u) python3 "+shellQuote(remotePath)+" "+shellQuote(action), nil)
		if e != nil {
			category := strings.TrimSpace(string(b))
			for _, safe := range []string{"refresh_token_reused", "token_expired", "authentication_failed", "provider_unavailable", "response_timeout", "assertion_failed", "configuration_failed"} {
				if category == "FAIL "+safe {
					return fmt.Errorf("codex-auth %s: %s; resources retained", action, safe)
				}
			}
			return fmt.Errorf("codex-auth %s: %w; inspect desktop locally (no auth output collected)", action, e)
		}
		if strings.TrimSpace(string(b)) != "PASS "+action {
			return fmt.Errorf("codex-auth %s did not confirm success", action)
		}
		return nil
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, e := c.remote(restoreCtx, host, "XDG_RUNTIME_DIR=/run/user/$(id -u) python3 "+shellQuote(remotePath)+" restore", nil)
		if e != nil {
			result = errors.Join(result, fmt.Errorf("restore provider config: %w", e))
		}
	}()
	if err = action("prepare"); err != nil {
		return err
	}
	if err = action("first"); err != nil {
		return err
	}
	if err = action("second"); err != nil {
		return err
	}
	logStage("explicit secrets reload while old provider sessions remain alive")
	if _, err = c.call(ctx, "secrets", "reload", c.state.DesktopID); err != nil {
		return err
	}
	if err = action("reloaded"); err != nil {
		return err
	}
	return action("third")
}
