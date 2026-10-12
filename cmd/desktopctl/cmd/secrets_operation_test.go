package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/store"
)

func localSecretOperation(t *testing.T, home string) (*secretOperation, error) {
	t.Helper()
	c := exec.CommandContext(context.Background(), "python3", "-c", secretOperationPython)
	c.Env = append(os.Environ(), "HOME="+home)
	return startSecretOperation(c)
}

func TestSecretOperationOverlapAndRecovery(t *testing.T) {
	home := t.TempDir()
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Close)
	if _, err := localSecretOperation(t, home); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second operator should fail busy: %v", err)
	}
	if err := op.Run("true"); err != nil {
		t.Fatal(err)
	}
	// Still locked after remote work, while the client commits fleet metadata.
	if _, err := localSecretOperation(t, home); err == nil {
		t.Fatal("released before commit")
	}
	op.Close() // interrupted between rotation and fleet commit
	recovery, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	if !recovery.pending {
		t.Fatal("interrupted rotation was forgotten")
	}
	if err := recovery.Run("true"); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Commit(); err != nil {
		t.Fatal(err)
	}
	recovery.Close()
	next, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.pending {
		t.Fatal("successful commit retained pending state")
	}
}

func TestSecretOperationFailureRetainsRecoveryWithoutOutput(t *testing.T) {
	home := t.TempDir()
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	err = op.Run("echo dummy-secret >&2; exit 1")
	if err == nil || strings.Contains(err.Error(), "dummy-secret") {
		t.Fatalf("unsafe result: %v", err)
	}
	op.Close()
	op, err = localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if !op.pending {
		t.Fatal("failed operation needs explicit recovery")
	}
}

func TestSecretOperationReturnsAllowlistedFailureReason(t *testing.T) {
	home := t.TempDir()
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()

	err = op.Run("exit 21")
	if err == nil || !strings.Contains(err.Error(), "Codex auth paths must be private") {
		t.Fatalf("missing safe actionable reason: %v", err)
	}
}

func TestSecretOperationDoesNotReturnUnknownChildOutput(t *testing.T) {
	home := t.TempDir()
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()

	err = op.Run("echo dummy-secret >&2; exit 73")
	if err == nil || strings.Contains(err.Error(), "dummy-secret") || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unsafe or unclassified result: %v", err)
	}
}

func TestSecretOperationRejectsSymlinkLock(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".ai-desktops-secret-operation")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "unrelated")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := localSecretOperation(t, home); err == nil {
		t.Fatal("accepted symlink lock")
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "preserve" {
		t.Fatalf("changed target: %v", err)
	}
}

func TestSecretOperationChildRetainsLockAfterDisconnect(t *testing.T) {
	home := t.TempDir()
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	started := filepath.Join(home, "started")
	done := filepath.Join(home, "done")
	finished := make(chan error, 1)
	go func() { finished <- op.Run(fmt.Sprintf("touch %q; sleep 1; touch %q", started, done)) }()
	waitSecretFile(t, started)
	if err := op.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err == nil {
		t.Fatal("lost coordinator reported success")
	}
	op.Close()
	if other, err := localSecretOperation(t, home); err == nil {
		other.Close()
		t.Fatal("child mutation no longer holds lock after coordinator death")
	}
	waitSecretFile(t, done)
	// The child can finish just after touching done; retry only this test's lock probe.
	deadline := time.Now().Add(5 * time.Second)
	for {
		other, err := localSecretOperation(t, home)
		if err == nil {
			defer other.Close()
			if !other.pending {
				t.Fatal("lost coordinator did not preserve pending rotation")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitSecretFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not reach synchronization point")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDesktopSecretsFreshSnapshotAndRecovery(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-test", Hostname: "desktop", Secrets: []string{"/old"}}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	stale := *d
	d.Secrets = []string{"/new"}
	if err := s.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	open := func(context.Context, *store.Desktop) (*secretOperation, error) { return localSecretOperation(t, home) }
	fresh, op, token, err := lockDesktopSecrets(ctx, s, &stale, false, open)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fresh.Secrets, ",") != "/new" {
		t.Fatal("used pre-lock snapshot")
	}
	if err := op.Run("true"); err != nil {
		t.Fatal(err)
	}
	op.Close() // lost client; add/remove must not silently choose a new snapshot
	if _, _, _, err := lockDesktopSecrets(ctx, s, d, false, open); err == nil {
		t.Fatal("add/remove accepted incomplete rotation")
	}
	_, recovery, newToken, err := lockDesktopSecrets(ctx, s, d, true, open)
	if err != nil {
		t.Fatal(err)
	}
	defer recovery.Close()
	if err := s.CommitSecretOperation(ctx, d.DesktopID, token, []string{"/stale"}); err == nil {
		t.Fatal("late disconnected client committed")
	}
	if err := recovery.Run("true"); err != nil {
		t.Fatal(err)
	}
	if err := commitDesktopSecrets(ctx, s, d.DesktopID, newToken, []string{"/new", "/added"}, recovery); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, d.DesktopID)
	if err != nil || strings.Join(got.Secrets, ",") != "/new,/added" {
		t.Fatalf("snapshot = %+v, %v", got, err)
	}
}

type failingSecretCommitStore struct{ *store.InMemoryStore }

func (s failingSecretCommitStore) CommitSecretOperation(context.Context, string, string, []string) error {
	return errors.New("injected metadata failure")
}

func TestDesktopSecretsFailedCommitRequiresReload(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	s := failingSecretCommitStore{store.NewInMemoryStore()}
	d := &store.Desktop{DesktopID: "d-fail", Hostname: "desktop"}
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	open := func(context.Context, *store.Desktop) (*secretOperation, error) { return localSecretOperation(t, home) }
	_, op, token, err := lockDesktopSecrets(ctx, s, d, false, open)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Run("true"); err != nil {
		t.Fatal(err)
	}
	if err := commitDesktopSecrets(ctx, s, d.DesktopID, token, []string{"/new"}, op); err == nil {
		t.Fatal("ignored failed commit")
	}
	op.Close()
	if _, _, _, err := lockDesktopSecrets(ctx, s, d, false, open); err == nil {
		t.Fatal("failed metadata commit lost recovery marker")
	}
	got, err := s.Get(ctx, d.DesktopID)
	if err != nil || len(got.Secrets) != 0 {
		t.Fatalf("changed authoritative snapshot: %+v %v", got, err)
	}
}

func TestDesktopSecretsSSHProtocol(t *testing.T) {
	oldConfig := cfg
	cfg = &config.Config{}
	t.Cleanup(func() { cfg = oldConfig })
	home := t.TempDir()
	bin := t.TempDir()
	// Execute the actual generated SSH remote command locally; no cloud/socket.
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nfor arg; do remote=$arg; done\nexec bash -c \"$remote\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-ssh", Hostname: "desktop"}
	ctx := context.Background()
	if err := s.Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	_, op, token, err := beginDesktopSecrets(ctx, s, d, false)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if err := op.Run("true"); err != nil {
		t.Fatal(err)
	}
	if err := commitDesktopSecrets(ctx, s, d.DesktopID, token, nil, op); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopSecretsRejectsChangedTarget(t *testing.T) {
	s := store.NewInMemoryStore()
	d := &store.Desktop{DesktopID: "d-target", Hostname: "old"}
	if err := s.Create(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	old := *d
	d.Hostname = "new"
	if err := s.Update(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	open := func(context.Context, *store.Desktop) (*secretOperation, error) { return localSecretOperation(t, home) }
	if _, _, _, err := lockDesktopSecrets(context.Background(), s, &old, false, open); err == nil {
		t.Fatal("accepted changed target")
	}
	// Early failure must release its lock without recording a pending mutation.
	op, err := localSecretOperation(t, home)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	if op.pending {
		t.Fatal("marked untouched credentials pending")
	}
}
