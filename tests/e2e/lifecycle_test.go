package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

type fakeCloud struct {
	events []string
	w      store.Workspace
	d      store.Desktop
	fail   string
}

func (f *fakeCloud) Workspace(context.Context, string) (store.Workspace, error) { return f.w, nil }
func (f *fakeCloud) Desktop(context.Context, string) (store.Desktop, error)     { return f.d, nil }
func (f *fakeCloud) Terminate(context.Context, string) error {
	f.events = append(f.events, "terminate")
	if f.fail == "terminate" {
		return errors.New("destroy failed")
	}
	f.d.State = store.StateTerminated
	f.w.AttachedDesktopID = ""
	return nil
}
func (f *fakeCloud) DeleteWorkspace(context.Context, string) error {
	f.events = append(f.events, "delete-workspace")
	return nil
}

func fixture(t *testing.T) (*state, *fakeCloud) {
	t.Helper()
	s := &state{Version: 1, RunID: "0123456789abcdef", Name: "e2e-ai-desktops-0123456789abcdef", Environment: "dev", Owner: "orchael", Repo: "orchael/ai-desktops", DesktopID: "d-test", WorkspaceID: "ws-test", AccessPointID: "fsap-test", WorkspaceCreated: true, DesktopCreated: true, Path: t.TempDir() + "/state.json"}
	f := &fakeCloud{w: store.Workspace{WorkspaceID: s.WorkspaceID, WorkspaceName: s.Name, WorkspaceMode: "efs", Environment: s.Environment, GitHubOwner: s.Owner, Repos: []string{s.Repo}, EFSAccessPointID: s.AccessPointID, AttachedDesktopID: s.DesktopID}, d: store.Desktop{DesktopID: s.DesktopID, DesktopName: s.Name, WorkspaceName: s.Name, WorkspaceID: s.WorkspaceID, Environment: s.Environment, GitHubOwner: s.Owner, Repos: []string{s.Repo}}}
	return s, f
}

func TestFinish(t *testing.T) {
	for _, tt := range []struct {
		name        string
		keep        bool
		scenarioErr error
		want        []string
	}{
		{"success", false, nil, []string{"terminate", "delete-workspace"}},
		{"keep", true, nil, nil},
		{"failure", false, errors.New("auth failed"), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, f := fixture(t)
			err := finish(context.Background(), f, s, tt.keep, tt.scenarioErr)
			if !reflect.DeepEqual(f.events, tt.want) {
				t.Fatalf("events %v, want %v", f.events, tt.want)
			}
			if tt.scenarioErr != nil && !errors.Is(err, tt.scenarioErr) {
				t.Fatal(err)
			}
			if tt.scenarioErr == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

type failingCleanup struct {
	*fakeCloud
	err           error
	beforeFailure func()
}

func (f *failingCleanup) Terminate(context.Context, string) error {
	if f.beforeFailure != nil {
		f.beforeFailure()
	}
	return f.err
}

func TestFinishPersistsCleanupFailure(t *testing.T) {
	s, f := fixture(t)
	cleanupErr := errors.New("cleanup unavailable")
	err := finish(context.Background(), &failingCleanup{fakeCloud: f, err: cleanupErr}, s, false, nil)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup error lost: %v", err)
	}
	loaded, err := loadState(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Result != "failed" {
		t.Fatalf("cleanup failure persisted as %q", loaded.Result)
	}
}

func TestFinishPreservesCleanupAndStateWriteErrors(t *testing.T) {
	s, f := fixture(t)
	cleanupErr := errors.New("cleanup unavailable")
	c := &failingCleanup{fakeCloud: f, err: cleanupErr, beforeFailure: func() { s.Path = filepath.Join(t.TempDir(), "missing", "state.json") }}
	err := finish(context.Background(), c, s, false, nil)
	var pathErr *os.PathError
	if !errors.Is(err, cleanupErr) || !errors.As(err, &pathErr) {
		t.Fatalf("must retain cleanup and write errors: %v", err)
	}
	if s.Result != "failed" {
		t.Fatalf("in-memory result %q", s.Result)
	}
}

func TestCleanupRefusesUnownedOrChangedResources(t *testing.T) {
	for _, mutation := range []func(*state, *fakeCloud){
		func(s *state, _ *fakeCloud) { s.Name = "eos-dev" },
		func(s *state, _ *fakeCloud) { s.DesktopCreated = false },
		func(_ *state, f *fakeCloud) { f.d.DesktopName = "eos-dev" },
		func(_ *state, f *fakeCloud) { f.w.EFSAccessPointID = "fsap-someone-else" },
		func(_ *state, f *fakeCloud) { f.w.AttachedDesktopID = "d-another" },
		func(_ *state, f *fakeCloud) { f.d.WorkspaceID = "ws-another" },
	} {
		s, f := fixture(t)
		mutation(s, f)
		if err := cleanup(context.Background(), f, s); err == nil {
			t.Fatal("unsafe cleanup accepted")
		}
		if len(f.events) > 0 {
			t.Fatalf("mutations before validation: %v", f.events)
		}
	}
}

func TestCleanupStopsBeforeWorkspaceDeleteOnTerminateFailure(t *testing.T) {
	s, f := fixture(t)
	f.fail = "terminate"
	if err := cleanup(context.Background(), f, s); err == nil {
		t.Fatal("expected error")
	}
	if !reflect.DeepEqual(f.events, []string{"terminate"}) {
		t.Fatal(f.events)
	}
}

func TestReuseValidatesOwnershipWithoutCreatingResources(t *testing.T) {
	s, f := fixture(t)
	if err := validateResources(context.Background(), f, s); err != nil {
		t.Fatal(err)
	}
	if len(f.events) != 0 {
		t.Fatal(f.events)
	}
	f.d.GitHubOwner = "someone-else"
	if err := validateResources(context.Background(), f, s); err == nil {
		t.Fatal("accepted unrelated desktop")
	}
}
