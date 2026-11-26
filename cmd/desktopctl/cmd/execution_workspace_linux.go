package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/orchael/desktopctl/internal/executionworkspace"
	"github.com/orchael/desktopctl/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:    "execution-workspace <prepare|status|cleanup>",
		Short:  "Manage a lease-scoped desktop execution checkout",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return errors.New("execution workspace requires the authenticated host transport account")
			}
			payload, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4097))
			if err != nil || len(payload) > 4096 {
				return errors.New("invalid execution workspace request")
			}
			decoder := json.NewDecoder(bytes.NewReader(payload))
			decoder.DisallowUnknownFields()
			var input executionworkspace.Request
			if err := decoder.Decode(&input); err != nil {
				return errors.New("invalid execution workspace request")
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				return errors.New("invalid execution workspace request")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			status, err := executionworkspace.Apply(ctx, "/var/lib/desktopctl/execution", args[0], input, checkoutExecutionRevision)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		},
	})
}

func checkoutExecutionRevision(ctx context.Context, path, repository, revision string) error {
	source, err := repo.Parse(repository)
	if err != nil {
		return errors.New("invalid GitHub repository")
	}
	account, err := user.Lookup("ubuntu")
	if err != nil {
		return errors.New("desktop workspace user unavailable")
	}
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 {
		return errors.New("invalid desktop workspace user")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	if err := os.Chown(path, int(uid), int(gid)); err != nil {
		return err
	}
	git := func(args ...string) ([]byte, error) {
		config := []string{"-c", "core.hooksPath=/dev/null", "-c", "credential.helper=", "-c", "http.followRedirects=false"}
		// Remote URL and revision were validated before reaching this command.
		cmd := exec.CommandContext(ctx, "/usr/bin/git", append(config, args...)...) //nolint:gosec
		cmd.Dir = path
		cmd.Env = []string{
			"PATH=/usr/bin:/bin", "HOME=/nonexistent", "XDG_CONFIG_HOME=/nonexistent",
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "GIT_ALLOW_PROTOCOL=https",
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		return cmd.Output()
	}
	for _, args := range [][]string{
		{"init", "--quiet", "--template="},
		{"fetch", "--quiet", "--no-tags", "--depth=1", "--no-recurse-submodules", "--", source.HTTPS(), revision},
		{"checkout", "--quiet", "--detach", revision},
	} {
		if _, err := git(args...); err != nil {
			return errors.New("GitHub revision checkout failed")
		}
	}
	head, err := git("rev-parse", "--verify", "HEAD")
	if err != nil || strings.TrimSpace(string(head)) != revision {
		return errors.New("Git checkout revision mismatch")
	}
	return nil
}
