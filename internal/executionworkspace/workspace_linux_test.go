package executionworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFenceAndCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	first := Request{"11111111-1111-4111-8111-111111111111", 1, "example/repo", strings.Repeat("a", 40)}
	next := Request{"22222222-2222-4222-8222-222222222222", 2, "example/next", strings.Repeat("b", 40)}
	calls := 0
	checkout := func(_ context.Context, path, repository, revision string) error {
		calls++
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, "revision"), []byte(repository+"@"+revision), 0600)
	}
	apply := func(operation string, req Request) (Status, error) {
		return Apply(context.Background(), root, operation, req, checkout)
	}
	fence := func(req Request) Request {
		return Request{LeaseID: req.LeaseID, Generation: req.Generation}
	}
	check := func(operation string, req Request, want string) Status {
		t.Helper()
		result, err := apply(operation, req)
		if err != nil || result.State != want {
			t.Fatalf("%s: got %+v, %v, want %s", operation, result, err, want)
		}
		return result
	}
	check("status", fence(first), "absent")
	ready := check("prepare", first, "ready")
	if replay := check("prepare", first, "ready"); replay != ready || calls != 1 {
		t.Fatalf("prepare replay: %+v, calls %d", replay, calls)
	}
	check("status", fence(first), "ready")
	if _, err := apply("prepare", next); !errors.Is(err, ErrFence) {
		t.Fatalf("competing prepare: %v", err)
	}
	if _, err := apply("cleanup", fence(next)); !errors.Is(err, ErrFence) {
		t.Fatalf("competing cleanup: %v", err)
	}
	check("cleanup", fence(first), "cleaned")
	check("cleanup", fence(first), "cleaned")
	if _, err := apply("prepare", first); !errors.Is(err, ErrFence) {
		t.Fatalf("late prepare: %v", err)
	}
	check("prepare", next, "ready")
	if _, err := apply("cleanup", fence(first)); !errors.Is(err, ErrFence) {
		t.Fatalf("stale cleanup: %v", err)
	}
	check("status", fence(next), "ready")
}

func TestWorkspaceRetryAndCleanupBeforePrepare(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	input := Request{"11111111-1111-4111-8111-111111111111", 1, "example/repo", strings.Repeat("a", 40)}
	ctx := context.Background()
	failed := errors.New("interrupted clone")
	_, err := Apply(ctx, root, "prepare", input, func(_ context.Context, path, _, _ string) error {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("prepare error: %v", err)
	}
	status, err := Apply(ctx, root, "prepare", input, func(_ context.Context, path, _, _ string) error {
		return os.Mkdir(path, 0700)
	})
	if err != nil || status.State != "ready" {
		t.Fatalf("retry: %+v, %v", status, err)
	}

	root = filepath.Join(t.TempDir(), "state")
	if status, err := Apply(ctx, root, "cleanup", Request{LeaseID: input.LeaseID, Generation: input.Generation}, nil); err != nil || status.State != "cleaned" {
		t.Fatalf("cleanup before prepare: %+v, %v", status, err)
	}
	if _, err := Apply(ctx, root, "prepare", input, nil); !errors.Is(err, ErrFence) {
		t.Fatalf("delayed prepare: %v", err)
	}
}
