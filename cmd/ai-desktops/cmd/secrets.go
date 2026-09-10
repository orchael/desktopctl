package cmd

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage desktop secrets",
}

var secretsReloadCmd = &cobra.Command{
	Use:   "reload <desktop-id>",
	Short: "Reload AWS Secrets Manager values on a running desktop",
	Long: `reload re-fetches every secret path that was configured at desktop creation time
and rewrites both the systemd user environment file and the shell-sourceable env
file and bridgectl agents.env on the running desktop. The bridge and active
provider processes are stopped, local Codex auth caches are cleared, and the
bridge is restarted. Start or resume sessions afterward. Retrieval failures
leave existing credentials and sessions untouched.

The desktop must be running and reachable via SSH. Use "ai-desktops ssh" to
verify connectivity before running this command.`,
	Args: cobra.ExactArgs(1),
	RunE: runSecretsReload,
}

var secretsAddCmd = &cobra.Command{
	Use:   "add <desktop-id> <secret-path> [<secret-path> ...]",
	Short: "Add one or more secrets to a running desktop",
	Long: `add appends one or more AWS Secrets Manager paths to the desktop's secret list,
injects their values into the running instance, and persists the updated list so
future reloads include the new paths.

The desktop must be running and reachable via SSH.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runSecretsAdd,
}

var secretsRemoveCmd = &cobra.Command{
	Use:   "remove <desktop-id> <secret-path> [<secret-path> ...]",
	Short: "Remove one or more secrets from a running desktop",
	Long: `remove deletes one or more AWS Secrets Manager paths from the desktop's
configured secret list, rewrites the running instance's secret environment files
without those paths, and persists the updated list so future reloads exclude
them.

The desktop must be running and reachable via SSH.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runSecretsRemove,
}

func init() {
	secretsCmd.AddCommand(secretsReloadCmd)
	secretsCmd.AddCommand(secretsAddCmd)
	secretsCmd.AddCommand(secretsRemoveCmd)
	rootCmd.AddCommand(secretsCmd)
}

func runSecretsReload(cmd *cobra.Command, args []string) error {
	if err := requireTools("ssh"); err != nil {
		return err
	}
	if cfg.Desktop.SSHKeyPath == "" {
		return fmt.Errorf("desktop.ssh_key_path is not set in config; cannot run remote commands")
	}

	ctx := context.Background()
	id := args[0]

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	if d.Hostname == "" {
		return fmt.Errorf("desktop %q has no hostname — is it running?", id)
	}
	if len(d.Secrets) == 0 {
		return fmt.Errorf("desktop %q has no secrets configured", id)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}

	fmt.Printf("Reloading %d secret(s) on %s (%s)...\n", len(d.Secrets), id, d.Hostname)

	script := buildSecretsReloadScript(trackedSecretPaths(cfg.GitHub.AgentSecret, d.Secrets), region)
	if err := runRemote(d, script); err != nil {
		return fmt.Errorf("secrets reload failed: %w", err)
	}
	return nil
}

func runSecretsAdd(cmd *cobra.Command, args []string) error {
	if err := requireTools("ssh"); err != nil {
		return err
	}
	if cfg.Desktop.SSHKeyPath == "" {
		return fmt.Errorf("desktop.ssh_key_path is not set in config; cannot run remote commands")
	}

	ctx := context.Background()
	id := args[0]
	newPaths := args[1:]

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	if d.Hostname == "" {
		return fmt.Errorf("desktop %q has no hostname — is it running?", id)
	}

	region := d.Region
	if region == "" {
		region = cfg.AWS.Region
	}

	// Verify each new path exists in Secrets Manager before touching anything.
	awsCfg, err := awsx.LoadConfig(ctx, region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("load AWS config to validate secrets: %w", err)
	}
	for _, path := range newPaths {
		fmt.Printf("Checking secret %s ...\n", path)
		ok, err := awsx.SecretExists(ctx, awsCfg, path)
		if err != nil {
			return fmt.Errorf("check secret %s: %w", path, err)
		}
		if !ok {
			return fmt.Errorf("secret %q not found in Secrets Manager (region %s)", path, region)
		}
	}

	toAdd, reloadPaths := secretPathsAfterAdd(d.Secrets, newPaths)
	reloadPaths = trackedSecretPaths(cfg.GitHub.AgentSecret, reloadPaths)
	for _, p := range newPaths {
		if !containsString(toAdd, p) {
			fmt.Printf("Secret %s already configured on %s, skipping\n", p, id)
		}
	}
	if len(toAdd) == 0 {
		fmt.Println("No new secrets to add.")
		return nil
	}

	// Inject all configured secrets because the remote script rewrites the
	// desktop env files atomically instead of appending to them.
	fmt.Printf("Reloading %d configured secret(s) on %s (%s), including %d new...\n", len(reloadPaths), id, d.Hostname, len(toAdd))
	script := buildSecretsReloadScript(reloadPaths, region)
	if err := runRemote(d, script); err != nil {
		return fmt.Errorf("secrets inject failed: %w", err)
	}

	// Persist the updated secret list in the fleet record.
	// Preserve the same base-before-overrides order used for injection. Re-read
	// the record so this update does not restore stale lifecycle fields.
	d, err = s.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read fleet record after injection: %w", err)
	}
	d.Secrets = reloadPaths
	if err := s.Update(ctx, d); err != nil {
		return fmt.Errorf("update fleet record: %w", err)
	}

	fmt.Printf("Added %d secret(s) to %s: %s\n", len(toAdd), id, strings.Join(toAdd, ", "))
	return nil
}

func runSecretsRemove(cmd *cobra.Command, args []string) error {
	if err := requireTools("ssh"); err != nil {
		return err
	}
	if cfg.Desktop.SSHKeyPath == "" {
		return fmt.Errorf("desktop.ssh_key_path is not set in config; cannot run remote commands")
	}

	ctx := context.Background()
	id := args[0]
	removePaths := args[1:]

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("desktop %q not found", id)
		}
		return err
	}

	if d.Hostname == "" {
		return fmt.Errorf("desktop %q has no hostname — is it running?", id)
	}

	toRemove, remainingPaths := secretPathsAfterRemove(d.Secrets, removePaths)
	remainingPaths = trackedSecretPaths(cfg.GitHub.AgentSecret, remainingPaths)
	for _, p := range removePaths {
		if !containsString(toRemove, p) {
			fmt.Printf("Secret %s is not configured on %s, skipping\n", p, id)
		}
	}
	if len(toRemove) == 0 {
		fmt.Println("No configured secrets to remove.")
		return nil
	}

	var script string
	if len(remainingPaths) == 0 {
		fmt.Printf("Clearing desktop secret files on %s (%s); removing %d secret(s)...\n", id, d.Hostname, len(toRemove))
		script = buildSecretsClearScript()
	} else {
		region := d.Region
		if region == "" {
			region = cfg.AWS.Region
		}
		fmt.Printf("Reloading %d remaining secret(s) on %s (%s); removing %d...\n", len(remainingPaths), id, d.Hostname, len(toRemove))
		script = buildSecretsRemoveReloadScript(remainingPaths, region)
	}
	if err := runRemote(d, script); err != nil {
		return fmt.Errorf("secrets remove failed: %w", err)
	}

	mgr := desktop.NewManager(s)
	if err := mgr.RemoveSecrets(ctx, id, toRemove); err != nil {
		return fmt.Errorf("update fleet record: %w", err)
	}

	fmt.Printf("Removed %d secret(s) from %s: %s\n", len(toRemove), id, strings.Join(toRemove, ", "))
	return nil
}

func secretPathsAfterAdd(existingPaths, newPaths []string) ([]string, []string) {
	existing := make(map[string]bool, len(existingPaths))
	reloadPaths := append([]string(nil), existingPaths...)
	for _, p := range existingPaths {
		existing[p] = true
	}

	var toAdd []string
	for _, p := range newPaths {
		if existing[p] {
			continue
		}
		toAdd = append(toAdd, p)
		reloadPaths = append(reloadPaths, p)
		existing[p] = true
	}

	return toAdd, reloadPaths
}

func desktopSecretPaths(agentPath string, additional []string) []string {
	var paths []string
	if agentPath != "" {
		paths = append(paths, agentPath)
	}
	_, paths = secretPathsAfterAdd(paths, additional)
	return paths
}

// trackedSecretPaths orders only explicitly registered paths. It never opts a
// legacy desktop into an agent secret merely because the local config has one.
func trackedSecretPaths(agentPath string, paths []string) []string {
	if agentPath != "" && containsString(paths, agentPath) {
		return desktopSecretPaths(agentPath, paths)
	}
	return append([]string(nil), paths...)
}

func secretPathsAfterRemove(existingPaths, removePaths []string) ([]string, []string) {
	removeRequested := make(map[string]bool, len(removePaths))
	for _, p := range removePaths {
		removeRequested[p] = true
	}

	removed := make(map[string]bool, len(removePaths))
	var toRemove []string
	var remainingPaths []string
	for _, p := range existingPaths {
		if removeRequested[p] {
			if !removed[p] {
				toRemove = append(toRemove, p)
				removed[p] = true
			}
			continue
		}
		remainingPaths = append(remainingPaths, p)
	}

	return toRemove, remainingPaths
}

// buildSecretsReloadScript embeds the same transactional rotation path for
// reload, add and remove. Only non-secret metadata is included in the command.
func buildSecretsReloadScript(secretPaths []string, region string) string {
	if secretPaths == nil {
		secretPaths = []string{}
	}
	request, _ := json.Marshal(struct {
		Paths  []string `json:"paths"`
		Region string   `json:"region"`
	}{secretPaths, region})
	return "set -euo pipefail\npython3 - '" + base64.StdEncoding.EncodeToString(request) + "' <<'AI_DESKTOPS_ROTATE_PY'\n" + secretsReloadPython + "\nAI_DESKTOPS_ROTATE_PY\n"
}

//go:embed scripts/reload-secrets.py
var secretsReloadPython string

func buildSecretsRemoveReloadScript(secretPaths []string, region string) string {
	return buildSecretsReloadScript(secretPaths, region)
}

func buildSecretsClearScript() string {
	return buildSecretsReloadScript(nil, "")
}
