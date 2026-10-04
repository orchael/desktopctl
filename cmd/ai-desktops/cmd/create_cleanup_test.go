package cmd

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/orchael/desktopctl/internal/pulumi"
)

type fakeDestroyRunner struct{ err error }

func (f fakeDestroyRunner) Destroy(context.Context, *pulumi.StackRef, io.Writer) error { return f.err }

func TestCleanupCreateAttemptReturnsDestroyFailure(t *testing.T) {
	ref := &pulumi.StackRef{StackName: "desktop-d-0123abcd"}
	if err := cleanupCreateAttempt(context.Background(), fakeDestroyRunner{}, ref, "d-0123abcd", errors.New("up failed")); err != nil {
		t.Fatalf("successful cleanup: %v", err)
	}
	if err := cleanupCreateAttempt(context.Background(), fakeDestroyRunner{err: errors.New("destroy failed")}, ref, "d-0123abcd", errors.New("up failed")); err == nil {
		t.Fatal("destroy failure was lost")
	}
}
