package controlplane

import (
	"context"
	"errors"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

// MockJobRunner exercises browser lifecycle flows without cloud resources.
type MockJobRunner struct{ Store store.Store }

func (r MockJobRunner) Run(ctx context.Context, args ...string) error {
	if len(args) == 0 {
		return errors.New("missing action")
	}
	switch args[0] {
	case "create":
		id, owner := flagValue(args, "--desktop-id"), flagValue(args, "--github-owner")
		if id == "" || owner == "" {
			return errors.New("missing create fields")
		}
		return r.Store.Create(ctx, &store.Desktop{
			DesktopID: id, GitHubOwner: owner, State: store.StateReady,
			StackName: "desktop-" + id, Region: "us-east-2", Readiness: "mock ready",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	case "start", "stop", "terminate":
		if len(args) != 2 {
			return errors.New("missing desktop ID")
		}
		d, err := r.Store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		switch args[0] {
		case "start":
			d.State = store.StateReady
		case "stop":
			d.State = store.StateStopped
		case "terminate":
			return r.Store.MarkTerminated(ctx, args[1])
		}
		return r.Store.Update(ctx, d)
	default:
		return errors.New("unknown action")
	}
}

func flagValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}
