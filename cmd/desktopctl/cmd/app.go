package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/orchael/desktopctl/internal/config"
	"github.com/spf13/cobra"
)

var (
	appSource string
	appBind   string
	appPort   int
	appOrigin string
)

var appCmd = &cobra.Command{Use: "app", Short: "Run the standalone browser companion on this server"}

func init() {
	appCmd.PersistentFlags().StringVar(&appSource, "source", ".", "desktopctl source checkout containing Dockerfile.control-plane")
	appCmd.PersistentFlags().StringVar(&appBind, "bind", "", "IPv4 bind address for the browser app (overrides app.bind)")
	appCmd.PersistentFlags().IntVar(&appPort, "port", 3000, "browser app port")
	appCmd.PersistentFlags().StringVar(&appOrigin, "origin", "", "HTTPS origin served by an authenticated private network proxy")
	appCmd.AddCommand(&cobra.Command{Use: "start", Short: "Build and start the companion app", Args: cobra.NoArgs, RunE: runAppStart})
	appCmd.AddCommand(&cobra.Command{Use: "stop", Short: "Stop the companion app", Args: cobra.NoArgs, RunE: runAppStop})
	appCmd.AddCommand(&cobra.Command{Use: "status", Short: "Show companion app containers", Args: cobra.NoArgs, RunE: runAppStatus})
	appCmd.AddCommand(&cobra.Command{Use: "url", Short: "Print the browser URL", Args: cobra.NoArgs, RunE: runAppURL})
	rootCmd.AddCommand(appCmd)
}

func appComposePath() (string, error) {
	root, err := filepath.Abs(appSource)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "deploy", "control-plane", "standalone-compose.yml")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("standalone Compose file at %s: %w", path, err)
	}
	return path, nil
}

func appConfigPath() (string, error) {
	path := cfgFile
	if path == "" {
		var err error
		path, err = config.DefaultPath()
		if err != nil {
			return "", err
		}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("desktopctl config at %s: %w", path, err)
	}
	return path, nil
}

func appTokenPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "desktopctl", "app-api-token"), nil
}

func appToken(create bool) (string, error) {
	path, err := appTokenPath()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("app token at %s must be a private regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(string(data))) < 32 {
			return "", errors.New("app token file is invalid")
		}
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, os.ErrNotExist) || !create {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(token + "\n"); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return token, nil
}

func appEnvironment(createToken bool) ([]string, error) {
	if err := validateAppAddress(); err != nil {
		return nil, err
	}
	bind := effectiveAppBind()
	configPath, err := appConfigPath()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	awsDir, err := filepath.Abs(filepath.Join(home, ".aws"))
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(awsDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("AWS config directory at %s is required", awsDir)
	}
	sourceDir, err := filepath.Abs(appSource)
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(cfg.Pulumi.InfraDir) {
		rel, err := filepath.Rel(sourceDir, cfg.Pulumi.InfraDir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, errors.New("pulumi.infra_dir must be inside --source for companion lifecycle operations")
		}
	}
	pulumiDir := filepath.Join(home, ".pulumi")
	if err := os.MkdirAll(pulumiDir, 0o700); err != nil {
		return nil, fmt.Errorf("prepare Pulumi directory: %w", err)
	}
	sshPublicKey := ""
	if cfg.Desktop.SSHKeyPath != "" {
		value, err := os.ReadFile(cfg.Desktop.SSHKeyPath + ".pub")
		if err != nil {
			return nil, fmt.Errorf("read configured desktop SSH public key: %w", err)
		}
		sshPublicKey = strings.TrimSpace(string(value))
	}
	token, err := appToken(createToken)
	if err != nil {
		return nil, err
	}
	return append(os.Environ(),
		"CONTROL_PLANE_API_TOKEN="+token,
		"DESKTOPCTL_APP_CONFIG="+configPath,
		"DESKTOPCTL_APP_AWS_DIR="+awsDir,
		"DESKTOPCTL_APP_PULUMI_DIR="+pulumiDir,
		"DESKTOPCTL_APP_SOURCE="+sourceDir,
		"DESKTOPCTL_APP_BIND="+bind,
		"CONTROL_PLANE_BIND_HOST="+bind,
		"DESKTOPCTL_APP_PORT="+strconv.Itoa(appPort),
		"DESKTOPCTL_APP_UID="+strconv.Itoa(os.Getuid()),
		"DESKTOPCTL_APP_GID="+strconv.Itoa(os.Getgid()),
		"CONTROL_PLANE_PUBLIC_ORIGIN="+appOrigin,
		"DESKTOPCTL_APP_AWS_PROFILE="+profile,
		"DESKTOPCTL_APP_AWS_REGION="+region,
		"DESKTOPCTL_APP_SSH_PUBLIC_KEY="+sshPublicKey), nil
}

func validateAppAddress() error {
	ip := net.ParseIP(effectiveAppBind())
	if ip == nil || ip.To4() == nil {
		return errors.New("app.bind or --bind must be an IPv4 address")
	}
	if appPort < 1 || appPort > 65535 {
		return errors.New("--port must be between 1 and 65535")
	}
	if appOrigin != "" {
		parsed, err := url.Parse(appOrigin)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("--origin must be an HTTPS origin without a path")
		}
	}
	return nil
}

func effectiveAppBind() string {
	if appBind != "" {
		return appBind
	}
	if cfg != nil && cfg.App.Bind != "" {
		return cfg.App.Bind
	}
	return "127.0.0.1"
}

func appBindWarning() string {
	if effectiveAppBind() == "0.0.0.0" {
		return "WARNING: 0.0.0.0 exposes the companion on every IPv4 interface; restrict access to your private network."
	}
	return ""
}

func runAppCompose(cmd *cobra.Command, start bool, args ...string) error {
	path, err := appComposePath()
	if err != nil {
		return err
	}
	var environment []string
	if start {
		environment, err = appEnvironment(true)
		if err != nil {
			return err
		}
	} else {
		// Compose still interpolates required service variables for ps/down.
		// Their values are unused, so stopping works after config or key removal.
		environment = append(os.Environ(),
			"CONTROL_PLANE_API_TOKEN=unused",
			"DESKTOPCTL_APP_CONFIG=/dev/null",
			"DESKTOPCTL_APP_AWS_DIR=/dev/null",
			"DESKTOPCTL_APP_PULUMI_DIR=/dev/null",
			"DESKTOPCTL_APP_SOURCE=/tmp",
			"DESKTOPCTL_APP_UID="+strconv.Itoa(os.Getuid()),
			"DESKTOPCTL_APP_GID="+strconv.Itoa(os.Getgid()))
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Minute)
	defer cancel()
	arguments := append([]string{"compose", "-f", path}, args...)
	process := exec.CommandContext(ctx, "docker", arguments...)
	process.Env = environment
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	if err := process.Run(); err != nil {
		return fmt.Errorf("docker compose %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func runAppStart(cmd *cobra.Command, _ []string) error {
	if warning := appBindWarning(); warning != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), warning)
	}
	if err := runAppCompose(cmd, true, "up", "--build", "--detach", "--wait"); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "App running at %s\n", appURL())
	return err
}

func runAppStop(cmd *cobra.Command, _ []string) error {
	return runAppCompose(cmd, false, "down")
}

func runAppStatus(cmd *cobra.Command, _ []string) error {
	return runAppCompose(cmd, false, "ps")
}

func runAppURL(cmd *cobra.Command, _ []string) error {
	if err := validateAppAddress(); err != nil {
		return err
	}
	if warning := appBindWarning(); warning != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), warning)
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), appURL())
	return err
}

func appURL() string {
	if appOrigin != "" {
		return appOrigin + "/"
	}
	return fmt.Sprintf("http://%s:%d/", effectiveAppBind(), appPort)
}
