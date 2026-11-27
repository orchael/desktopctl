package executionworkspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var (
	leasePattern    = regexp.MustCompile("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
	repoPattern     = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$")
	revisionPattern = regexp.MustCompile("^[0-9a-f]{40}$")
	ErrFence        = errors.New("execution workspace fence rejected")
)

type Request struct {
	LeaseID      string `json:"leaseId"`
	Generation   int32  `json:"generation"`
	Repository   string `json:"repository,omitempty"`
	BaseRevision string `json:"baseRevision,omitempty"`
}

type Status struct {
	Request
	State        string `json:"state"`
	WorkspaceRef string `json:"workspaceRef,omitempty"`
}

type Checkout func(ctx context.Context, path, repository, revision string) error

// Apply serializes operations below a dedicated root owned by the transport
// account. checkout must create the checkout beneath the supplied path, run
// unprivileged, and verify the resulting revision before returning.
func Apply(ctx context.Context, root, operation string, input Request, checkout Checkout) (Status, error) {
	if !leasePattern.MatchString(input.LeaseID) || input.Generation < 1 {
		return Status{}, errors.New("invalid execution fence")
	}
	switch operation {
	case "prepare":
		if !repoPattern.MatchString(input.Repository) ||
			filepath.Base(input.Repository) == "." || filepath.Base(input.Repository) == ".." ||
			!revisionPattern.MatchString(input.BaseRevision) {
			return Status{}, errors.New("invalid repository or base revision")
		}
	case "status", "cleanup":
		if input.Repository != "" || input.BaseRevision != "" {
			return Status{}, errors.New("unexpected repository or base revision")
		}
	default:
		return Status{}, errors.New("unsupported workspace operation")
	}

	if err := os.MkdirAll(root, 0755); err != nil {
		return Status{}, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return Status{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return Status{}, errors.New("execution workspace state directory is not protected")
	}
	lock, err := os.OpenFile(filepath.Join(root, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Status{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return Status{}, errors.New("execution workspace busy; retry")
		}
		return Status{}, err
	}

	statePath := filepath.Join(root, "state.json")
	var state Status
	data, err := os.ReadFile(statePath)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &state); err != nil {
			return Status{}, err
		}
		switch state.State {
		case "preparing", "ready", "cleaning", "cleaned":
		default:
			return Status{}, errors.New("invalid persisted workspace state")
		}
		if input.Generation < state.Generation ||
			(input.Generation == state.Generation && input.LeaseID != state.LeaseID) ||
			(input.Generation > state.Generation && state.State != "cleaned") {
			return Status{}, ErrFence
		}
	case !errors.Is(err, os.ErrNotExist):
		return Status{}, err
	}
	matching := input.Generation == state.Generation
	path := filepath.Join(root, fmt.Sprintf("checkout-%d-%s", input.Generation, input.LeaseID))
	if matching && state.State == "ready" && operation != "cleanup" {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return Status{}, errors.New("prepared execution checkout is unavailable")
		}
	}
	if operation == "status" {
		if !matching {
			return Status{Request: input, State: "absent"}, nil
		}
		return state, nil
	}

	if operation == "cleanup" {
		if matching && state.State == "cleaned" {
			return state, nil
		}
		state = Status{Request: input, State: "cleaning"}
		if err := save(root, statePath, state); err != nil {
			return Status{}, err
		}
		if err := os.RemoveAll(path); err != nil {
			return Status{}, err
		}
		state.State = "cleaned"
		return state, save(root, statePath, state)
	}

	if matching {
		if state.State == "cleaned" || state.State == "cleaning" || state.Request != input {
			return Status{}, ErrFence
		}
		if state.State == "ready" {
			return state, nil
		}
	}
	state = Status{Request: input, State: "preparing"}
	if err := save(root, statePath, state); err != nil {
		return Status{}, err
	}
	if err := os.RemoveAll(path); err != nil {
		return Status{}, err
	}
	if err := checkout(ctx, path, input.Repository, input.BaseRevision); err != nil {
		return Status{}, fmt.Errorf("prepare execution checkout: %w", err)
	}
	state.State = "ready"
	state.WorkspaceRef = fmt.Sprintf("%s:%d", input.LeaseID, input.Generation)
	return state, save(root, statePath, state)
}

func save(root, destination string, state Status) error {
	file, err := os.CreateTemp(root, ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(state)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), destination); err != nil {
		return err
	}
	directory, err := os.Open(root)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
