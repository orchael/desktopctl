package cmd

import (
	"context"
	"testing"

	"github.com/orchael/desktopctl/internal/store"
)

func TestChooseCreateID(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	id, err := chooseCreateID(ctx, s, "d-0123abcd")
	if err != nil || id != "d-0123abcd" {
		t.Fatalf("explicit id = %q, %v", id, err)
	}
	if err := s.Create(ctx, &store.Desktop{DesktopID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := chooseCreateID(ctx, s, id); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if _, err := chooseCreateID(ctx, s, "../../bad"); err == nil {
		t.Fatal("invalid id accepted")
	}
	generated, err := chooseCreateID(ctx, s, "")
	if err != nil || generated == id || generated == "" {
		t.Fatalf("generated id = %q, %v", generated, err)
	}
}
