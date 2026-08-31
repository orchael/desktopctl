package cmd

import (
	"context"
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
file on the running desktop. Affected user services (e.g. bridgectl) are
restarted automatically.

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

	script := buildSecretsReloadScript(d.Secrets, region)
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
	mgr := desktop.NewManager(s)
	if err := mgr.AddSecrets(ctx, id, toAdd); err != nil {
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
		script = buildSecretsReloadScript(remainingPaths, region)
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

// buildSecretsReloadScript returns a shell script that re-fetches each secret
// path from AWS Secrets Manager and rewrites the desktop secret files.
func buildSecretsReloadScript(secretPaths []string, region string) string {
	var b strings.Builder

	b.WriteString("set -euo pipefail\n")
	b.WriteString("DESKTOP_ENV_TMP=$(mktemp)\n")
	b.WriteString("trap 'rm -f \"$DESKTOP_ENV_TMP\"' EXIT\n")
	fmt.Fprintf(&b, "REGION=%q\n", region)

	for _, path := range secretPaths {
		fmt.Fprintf(&b, `
SECRET_JSON=$(aws secretsmanager get-secret-value \
  --region "$REGION" \
  --secret-id %q \
  --query SecretString \
  --output text 2>/dev/null) || true
if [ -z "$SECRET_JSON" ] || [ "$SECRET_JSON" = "None" ]; then
  echo "WARNING: could not retrieve desktop secret %s" >&2
else
  printf '%%s\n' "$SECRET_JSON" | python3 -c "
import json, re, sys
d = json.load(sys.stdin)
valid_key = re.compile(r'^[A-Za-z_][A-Za-z0-9_]*$')
sq = lambda v: chr(39) + str(v).replace(chr(39), chr(39)+chr(92)+chr(39)+chr(39)) + chr(39)
def normalize(v):
    if isinstance(v, (dict, list)):
        return json.dumps(v, separators=(',', ':'))
    sv = str(v)
    if '\0' in sv:
        return None
    stripped = sv.strip()
    if '\n' in sv and stripped[:1] in ('{', '['):
        try:
            return json.dumps(json.loads(sv), separators=(',', ':'))
        except json.JSONDecodeError:
            return None
    if '\n' in sv:
        return None
    return sv
for k, v in d.items():
    if not valid_key.match(k):
        print(f'WARNING: skipping secret key {k!r} (not a valid env var name)', file=sys.stderr)
        continue
    sv = normalize(v)
    if sv is None:
        print(f'WARNING: skipping secret key {k!r} (value contains newline/NUL or invalid JSON)', file=sys.stderr)
        continue
    if sv:
        print(f'{k}={sq(sv)}')
" >> "$DESKTOP_ENV_TMP" || echo "WARNING: failed to parse desktop secret %s" >&2
fi
unset SECRET_JSON
`, path, path, path)
	}

	b.WriteString(`
if [ ! -s "$DESKTOP_ENV_TMP" ]; then
  echo "WARNING: no secret values retrieved; files not updated" >&2
  exit 0
fi

# Write the shell-sourceable file to a temp location first, then move it into
# place atomically so a partial write is never observed by a concurrent shell.
SHELL_TMP=$(mktemp)
trap 'rm -f "$DESKTOP_ENV_TMP" "$SHELL_TMP"' EXIT
while IFS= read -r kv; do
  printf 'export %s\n' "$kv" >> "$SHELL_TMP"
done < "$DESKTOP_ENV_TMP"

# systemd user environment (read by user manager; available to bridgectl and other user services)
install -d -m 700 ~/.config/environment.d
install -m 600 "$DESKTOP_ENV_TMP" ~/.config/environment.d/desktop-secrets.conf

# shell-sourceable file for interactive sessions (atomic replace)
install -m 600 "$SHELL_TMP" ~/.desktop-secrets

# reload systemd user daemon so it picks up the new environment
systemctl --user daemon-reload

# restart affected user services; non-fatal if they are not installed
for svc in bridgectl; do
  if systemctl --user is-active --quiet "$svc" 2>/dev/null; then
    systemctl --user restart "$svc" && echo "restarted $svc" || echo "WARNING: failed to restart $svc" >&2
  fi
done

echo "secrets reloaded successfully"
`)

	return b.String()
}

func buildSecretsClearScript() string {
	return `set -euo pipefail

# Clear both user environment surfaces when the configured secret list becomes empty.
install -d -m 700 ~/.config/environment.d
install -m 600 /dev/null ~/.config/environment.d/desktop-secrets.conf
install -m 600 /dev/null ~/.desktop-secrets

systemctl --user daemon-reload

for svc in bridgectl; do
  if systemctl --user is-active --quiet "$svc" 2>/dev/null; then
    systemctl --user restart "$svc" && echo "restarted $svc" || echo "WARNING: failed to restart $svc" >&2
  fi
done

echo "secrets cleared successfully"
`
}
