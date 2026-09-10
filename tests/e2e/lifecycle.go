package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/orchael/ai-desktops/internal/store"
)

// State deliberately contains resource identifiers and paths only, never auth.
type state struct {
	Version           int    `json:"version"`
	RunID             string `json:"run_id"`
	Name              string `json:"name"`
	Environment       string `json:"environment"`
	Owner             string `json:"owner"`
	Repo              string `json:"repo"`
	StartedAt         string `json:"started_at"`
	Config            string `json:"config"`
	Profile           string `json:"profile,omitempty"`
	Region            string `json:"region,omitempty"`
	SSHKey            string `json:"ssh_key"`
	DesktopID         string `json:"desktop_id,omitempty"`
	WorkspaceID       string `json:"workspace_id,omitempty"`
	AccessPointID     string `json:"access_point_id,omitempty"`
	WorkspaceCreated  bool   `json:"workspace_created"`
	DesktopCreated    bool   `json:"desktop_created"`
	DesktopTerminated bool   `json:"desktop_terminated"`
	WorkspaceDeleted  bool   `json:"workspace_deleted"`
	Result            string `json:"result"`
	Path              string `json:"-"`
}

func (s *state) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path), ".e2e-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.Path)
}

func loadState(path string) (*state, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s state
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	s.Path = path
	if err = s.validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

func (s *state) validate() error {
	if s.Version != 1 || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(s.RunID) || s.Name != "e2e-ai-desktops-"+s.RunID || s.Environment != "dev" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/ai-desktops$`).MatchString(s.Repo) || s.Owner != strings.Split(s.Repo, "/")[0] {
		return errors.New("refusing state without this runner's exact dev resource identity")
	}
	if s.DesktopID != "" && (!s.DesktopCreated || !s.WorkspaceCreated) {
		return errors.New("desktop not recorded as created by this runner")
	}
	if s.WorkspaceCreated && (s.WorkspaceID == "" || s.AccessPointID == "") {
		return errors.New("workspace ownership identifiers missing")
	}
	return nil
}

type resources interface {
	Workspace(context.Context, string) (store.Workspace, error)
	Desktop(context.Context, string) (store.Desktop, error)
	Terminate(context.Context, string) error
	DeleteWorkspace(context.Context, string) error
}

func validateResources(ctx context.Context, c resources, s *state) error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.WorkspaceCreated && !s.WorkspaceDeleted {
		w, err := c.Workspace(ctx, s.Name)
		if err != nil {
			return err
		}
		if w.WorkspaceID != s.WorkspaceID || w.WorkspaceName != s.Name || w.Environment != s.Environment || w.GitHubOwner != s.Owner || !sameRepo(w.Repos, s.Repo) || w.EFSAccessPointID != s.AccessPointID || (w.AttachedDesktopID != "" && w.AttachedDesktopID != s.DesktopID) {
			return errors.New("live workspace identity or attachment differs from runner state; refusing mutation")
		}
	}
	if s.DesktopCreated && !s.DesktopTerminated {
		d, err := c.Desktop(ctx, s.DesktopID)
		if err != nil {
			return err
		}
		if d.DesktopID != s.DesktopID || d.DesktopName != s.Name || d.Environment != s.Environment || d.WorkspaceName != s.Name || d.WorkspaceID != s.WorkspaceID || d.GitHubOwner != s.Owner || !sameRepo(d.Repos, s.Repo) {
			return errors.New("live desktop identity differs from runner state; refusing mutation")
		}
	}
	return nil
}

func sameRepo(repos []string, expected string) bool {
	return len(repos) == 1 && strings.TrimPrefix(repos[0], "github.com/") == expected
}

func cleanup(ctx context.Context, c resources, s *state) error {
	if err := validateResources(ctx, c, s); err != nil {
		return err
	}
	if s.DesktopCreated && !s.DesktopTerminated {
		d, err := c.Desktop(ctx, s.DesktopID)
		if err != nil {
			return err
		}
		if d.State != store.StateTerminated {
			if err = c.Terminate(ctx, s.DesktopID); err != nil {
				return fmt.Errorf("desktop retained; workspace untouched: %w", err)
			}
		}
		s.DesktopTerminated = true
		if err = s.save(); err != nil {
			return err
		}
	}
	if s.WorkspaceCreated && !s.WorkspaceDeleted {
		// Re-check attachment after terminating before deleting storage metadata.
		if err := validateResources(ctx, c, s); err != nil {
			return err
		}
		w, err := c.Workspace(ctx, s.Name)
		if err != nil {
			return err
		}
		if w.AttachedDesktopID != "" {
			return errors.New("workspace still attached after termination; retained")
		}
		if err = c.DeleteWorkspace(ctx, s.Name); err != nil {
			return err
		}
		s.WorkspaceDeleted = true
		if err = s.save(); err != nil {
			return err
		}
	}
	return nil
}

func finish(ctx context.Context, c resources, s *state, keep bool, scenarioErr error) error {
	if scenarioErr != nil {
		s.Result = "failed"
	} else {
		s.Result = "passed"
	}
	if err := s.save(); err != nil {
		return errors.Join(scenarioErr, err)
	}
	if scenarioErr != nil || keep {
		return scenarioErr
	}
	return cleanup(ctx, c, s)
}
