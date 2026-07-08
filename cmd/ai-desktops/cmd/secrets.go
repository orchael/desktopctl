package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func init() {
	secretsCmd.AddCommand(secretsReloadCmd)
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
  printf '%%s\n' "$SECRET_JSON" | python3 -c "import json,sys; d=json.load(sys.stdin); sq=lambda v: chr(39)+str(v).replace(chr(39), chr(39)+chr(92)+chr(39)+chr(39))+chr(39); print('\n'.join(f'{k}={sq(v)}' for k,v in d.items() if v))" >> "$DESKTOP_ENV_TMP" || echo "WARNING: failed to parse desktop secret %s" >&2
fi
unset SECRET_JSON
`, path, path, path)
	}

	b.WriteString(`
if [ ! -s "$DESKTOP_ENV_TMP" ]; then
  echo "WARNING: no secret values retrieved; files not updated" >&2
  exit 0
fi

# systemd user environment (read by user manager; available to bridgectl and other user services)
mkdir -p ~/.config/environment.d
install -m 600 "$DESKTOP_ENV_TMP" ~/.config/environment.d/desktop-secrets.conf

# shell-sourceable file for interactive sessions
install -m 600 /dev/null ~/.desktop-secrets
while IFS= read -r kv; do
  printf 'export %s\n' "$kv" >> ~/.desktop-secrets
done < "$DESKTOP_ENV_TMP"

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
