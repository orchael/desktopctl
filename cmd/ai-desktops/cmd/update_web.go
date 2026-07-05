package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var updateWebVersion string
var updateWebLocal bool
var updateWebLocalDir string

var updateWebCmd = &cobra.Command{
	Use:   "update-web <desktop-id>",
	Short: "Update the desktop-web app on a running desktop",
	Long: `update-web installs a new version of @markcallen/desktop-web on a running desktop
and restarts the ai-desktops-web systemd service.

Two modes are available:

  --version X.Y.Z
      Install a specific published version from GitHub Packages (requires
      GITHUB_NPM_TOKEN with read:packages scope to be set in the environment).

  --local [path]
      Build the local apps/desktop-web source (or the path you specify), pack
      it with "pnpm pack", copy the tarball to the desktop over SCP, install it
      directly from the tarball, and restart the service. This does not require
      a published package.

Examples:
  ai-desktops update-web d-8925023f --version 0.3.0
  ai-desktops update-web d-8925023f --local
  ai-desktops update-web d-8925023f --local ./path/to/desktop-web`,
	Args: cobra.ExactArgs(1),
	RunE: runUpdateWeb,
}

func init() {
	updateWebCmd.Flags().StringVar(&updateWebVersion, "version", "", "published npm version to install (e.g. 0.3.0)")
	updateWebCmd.Flags().BoolVar(&updateWebLocal, "local", false, "build and install from local source")
	updateWebCmd.Flags().StringVar(&updateWebLocalDir, "local-dir", "", "path to local desktop-web source (defaults to apps/desktop-web relative to the repo root)")
	rootCmd.AddCommand(updateWebCmd)
}

func runUpdateWeb(cmd *cobra.Command, args []string) error {
	if updateWebVersion == "" && !updateWebLocal {
		return fmt.Errorf("one of --version or --local is required")
	}
	if updateWebVersion != "" && updateWebLocal {
		return fmt.Errorf("--version and --local are mutually exclusive")
	}
	if err := requireTools("ssh", "scp"); err != nil {
		return err
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
	if cfg.Desktop.SSHKeyPath == "" {
		return fmt.Errorf("desktop.ssh_key_path is not set in config; cannot run remote commands")
	}

	if updateWebLocal {
		return updateWebFromLocal(d)
	}
	return updateWebFromRegistry(d, updateWebVersion)
}

// sshArgs returns the common ssh flags for the desktop.
func sshFlags(d *store.Desktop) []string {
	return []string{
		"-i", cfg.Desktop.SSHKeyPath,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=30",
		"-o", "BatchMode=yes",
		"-o", "PasswordAuthentication=no",
	}
}

// runRemote executes cmd on the desktop over SSH, streaming stdout/stderr.
func runRemote(d *store.Desktop, remoteCmd string) error {
	args := append(sshFlags(d), fmt.Sprintf("ubuntu@%s", d.Hostname), remoteCmd)
	c := exec.Command("ssh", args...) //nolint:gosec
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// updateWebFromRegistry installs a specific published version via npm.
func updateWebFromRegistry(d *store.Desktop, version string) error {
	npmToken := os.Getenv("GITHUB_NPM_TOKEN")
	if npmToken == "" {
		return fmt.Errorf("GITHUB_NPM_TOKEN is not set; a GitHub token with read:packages scope is required")
	}

	fmt.Printf("Installing @markcallen/desktop-web@%s on %s...\n", version, d.DesktopID)

	// Write ~/.npmrc, install the package, remove the token, restart.
	remoteCmd := fmt.Sprintf(`set -e
printf '@markcallen:registry=https://npm.pkg.github.com\n//npm.pkg.github.com/:_authToken=%s\n' | sudo tee /root/.npmrc > /dev/null
sudo npm install --prefix /opt/ai-desktops/web @markcallen/desktop-web@%s
sudo rm -f /root/.npmrc
sudo systemctl restart ai-desktops-web
sudo systemctl is-active --quiet ai-desktops-web && echo "ai-desktops-web restarted successfully"`,
		npmToken, version)

	if err := runRemote(d, remoteCmd); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}
	fmt.Printf("desktop-web updated to %s on %s\n", version, d.DesktopID)
	return nil
}

// updateWebFromLocal builds the local source, SCPs the tarball, and installs it.
func updateWebFromLocal(d *store.Desktop) error {
	if err := requireTools("pnpm"); err != nil {
		return err
	}

	localDir := updateWebLocalDir
	if localDir == "" {
		// Default: apps/desktop-web relative to the repo root (two levels above the binary).
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}
		// Walk up from the binary toward the repo root.
		localDir = filepath.Join(filepath.Dir(exe), "..", "..", "apps", "desktop-web")
		// If that doesn't exist, fall back to cwd-relative.
		if _, serr := os.Stat(localDir); serr != nil {
			localDir = filepath.Join("apps", "desktop-web")
		}
	}

	abs, err := filepath.Abs(localDir)
	if err != nil {
		return fmt.Errorf("resolve local dir: %w", err)
	}
	if _, serr := os.Stat(abs); serr != nil {
		return fmt.Errorf("local desktop-web directory not found at %s; use --local-dir to specify the path", abs)
	}

	fmt.Printf("Building local desktop-web from %s...\n", abs)

	// Run pnpm build then pnpm pack inside the local directory.
	buildCmd := exec.Command("pnpm", "run", "build")
	buildCmd.Dir = abs
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("pnpm build failed: %w", err)
	}

	// Read name and version from package.json so we can compute the tarball
	// filename without parsing pnpm's rich stdout output.
	pkg, err := readLocalPkgJSON(abs)
	if err != nil {
		return fmt.Errorf("read package.json: %w", err)
	}
	tarball := filepath.Join(os.TempDir(), npmTarballName(pkg.Name, pkg.Version))
	defer os.Remove(tarball)

	// pnpm pack writes its rich UI (📦 summary, file list) to stdout.
	// Forward both streams to stderr so the user sees the output but we do
	// not attempt to parse it.
	packCmd := exec.Command("pnpm", "pack", "--pack-destination", os.TempDir())
	packCmd.Dir = abs
	packCmd.Stdout = os.Stderr
	packCmd.Stderr = os.Stderr
	if err := packCmd.Run(); err != nil {
		return fmt.Errorf("pnpm pack failed: %w", err)
	}

	if _, serr := os.Stat(tarball); serr != nil {
		return fmt.Errorf("expected tarball not found at %s after pnpm pack", tarball)
	}

	version := pkg.Version

	fmt.Printf("Packed %s — copying to %s...\n", filepath.Base(tarball), d.DesktopID)

	// SCP the tarball to a temp location on the desktop.
	remoteTar := "/tmp/" + filepath.Base(tarball)
	scpArgs := append(sshFlags(d), tarball, fmt.Sprintf("ubuntu@%s:%s", d.Hostname, remoteTar))
	scpCmd := exec.Command("scp", scpArgs...) //nolint:gosec
	scpCmd.Stdout = os.Stdout
	scpCmd.Stderr = os.Stderr
	if err := scpCmd.Run(); err != nil {
		return fmt.Errorf("scp failed: %w", err)
	}

	fmt.Printf("Installing from tarball on %s...\n", d.DesktopID)

	remoteCmd := fmt.Sprintf(`set -e
sudo npm install --prefix /opt/ai-desktops/web %s
rm -f %s
sudo systemctl restart ai-desktops-web
sudo systemctl is-active --quiet ai-desktops-web && echo "ai-desktops-web restarted successfully"`,
		remoteTar, remoteTar)

	if err := runRemote(d, remoteCmd); err != nil {
		return fmt.Errorf("remote install failed: %w", err)
	}
	fmt.Printf("desktop-web updated to %s on %s\n", version, d.DesktopID)
	return nil
}

type localPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func readLocalPkgJSON(dir string) (*localPkg, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	var p localPkg
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	if p.Name == "" || p.Version == "" {
		return nil, fmt.Errorf("package.json missing name or version")
	}
	return &p, nil
}

// npmTarballName returns the filename npm/pnpm gives a packed tarball.
// e.g. "@markcallen/desktop-web", "0.2.4" → "markcallen-desktop-web-0.2.4.tgz"
func npmTarballName(name, version string) string {
	n := strings.TrimPrefix(name, "@")
	n = strings.ReplaceAll(n, "/", "-")
	return n + "-" + version + ".tgz"
}
