//go:build linux || darwin

package controlplane

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCLIJobRunnerCancellationStopsChildProcess(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	childFinished := filepath.Join(dir, "child-finished")
	scriptPath := filepath.Join(dir, "fake-desktopctl")
	script := "#!/bin/sh\n(sleep 2; touch '" + childFinished + "') &\ntouch '" + started + "'\nwait\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- (CLIJobRunner{Binary: scriptPath, ConfigPath: "unused", SourceDir: dir}).Run(ctx, "start", "d-test001")
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake CLI did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled CLI unexpectedly succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled CLI did not return")
	}
	time.Sleep(2200 * time.Millisecond)
	if _, err := os.Stat(childFinished); err == nil {
		t.Fatal("CLI child process survived cancellation")
	}
}
