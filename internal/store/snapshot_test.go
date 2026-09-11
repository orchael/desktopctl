package store

import (
	"context"
	"slices"
	"testing"
)

func TestInMemoryDesktopSliceIsolation(t *testing.T) {
	for _, operation := range []string{"Create", "Get", "List", "Update", "BeginSecretOperation"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			s := NewInMemoryStore()
			d := &Desktop{DesktopID: "d-snapshot", Repos: []string{"repo"}, Secrets: []string{"secret-path"}, AVDNames: []string{"avd"}}
			if err := s.Create(ctx, d); err != nil {
				t.Fatal(err)
			}
			var err error
			switch operation {
			case "Get":
				d, err = s.Get(ctx, d.DesktopID)
			case "List":
				var records []*Desktop
				records, err = s.List(ctx)
				if err == nil {
					d = records[0]
				}
			case "Update":
				err = s.Update(ctx, d)
			case "BeginSecretOperation":
				d, err = s.BeginSecretOperation(ctx, d.DesktopID, "snapshot-token")
			}
			if err != nil {
				t.Fatal(err)
			}
			d.Repos[0], d.Secrets[0], d.AVDNames[0] = "mutated", "mutated", "mutated"
			stored, err := s.Get(ctx, d.DesktopID)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(stored.Repos, []string{"repo"}) || !slices.Equal(stored.Secrets, []string{"secret-path"}) || !slices.Equal(stored.AVDNames, []string{"avd"}) {
				t.Fatalf("%s exposed aliased slices: repos=%v secrets=%v avds=%v", operation, stored.Repos, stored.Secrets, stored.AVDNames)
			}
		})
	}
}
