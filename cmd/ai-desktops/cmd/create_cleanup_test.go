package cmd

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
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

type failingDeleteStore struct {
	store.Store
	err error
}

func (s failingDeleteStore) Delete(context.Context, string) error { return s.err }

func TestFinalizeCreateFailureRetainsRecordOnCleanupOrDeleteFailure(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		cleanupErr error
		deleteErr  error
		wantRecord bool
	}{
		{name: "clean", wantRecord: false},
		{name: "cleanup failed", cleanupErr: errors.New("instance still running"), wantRecord: true},
		{name: "delete failed", deleteErr: errors.New("DynamoDB unavailable"), wantRecord: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := store.NewInMemoryStore()
			id := "d-0123abcd"
			if err := base.Create(ctx, &store.Desktop{DesktopID: id}); err != nil {
				t.Fatal(err)
			}
			var s store.Store = base
			if tc.deleteErr != nil {
				s = failingDeleteStore{Store: base, err: tc.deleteErr}
			}
			err := finalizeCreateFailure(ctx, s, nil, false, "dev", "", id, errors.New("create failed"), tc.cleanupErr)
			if err == nil {
				t.Fatal("expected create error")
			}
			_, getErr := base.Get(ctx, id)
			if got := getErr == nil; got != tc.wantRecord {
				t.Fatalf("record retained = %v, want %v", got, tc.wantRecord)
			}
		})
	}
}
