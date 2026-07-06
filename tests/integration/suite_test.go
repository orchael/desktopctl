//go:build integration

// Package integration contains end-to-end tests that require a real AWS
// account.  The suite is entirely self-contained: it generates its own SSH
// key pair, writes its own config file, bootstraps the S3 Pulumi backend,
// deploys the foundation stack, creates a desktop, runs all FR checks, and
// terminates the desktop on exit.
//
// Run with:
//
//	make test-integration
//
// Required environment variables (when not using AI_DESKTOPS_TEST_CONFIG):
//
//	AI_DESKTOPS_TEST_BUCKET   — globally-unique S3 bucket name for Pulumi state
//	                            (created if it doesn't exist; never touches prod/dev buckets)
//	AI_DESKTOPS_GITHUB_OWNER  — GitHub org or user whose repos will be used
//
// AWS credentials are taken from the environment in the standard order:
// AWS_PROFILE, AWS_ACCESS_KEY_ID/SECRET/SESSION, or the default credential chain.
//
// Optional:
//
//	AI_DESKTOPS_TEST_CONFIG — path to a pre-existing config file (e.g. tests/integration/config.yaml).
//	                          When set, AI_DESKTOPS_TEST_BUCKET and AI_DESKTOPS_GITHUB_OWNER are read
//	                          from the config file and SSH key generation is skipped when ssh_key_path
//	                          in the config already points to an existing file.
//	AI_DESKTOPS_TEST_REPO   — a valid repo URL for FR-6/7 workspace tests
//	AI_DESKTOPS_EXISTING_ID — adopt an already-running desktop (skip create/terminate)
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// DesktopFixture holds state for the desktop created (or adopted) by TestMain
// and shared across all integration subtests.
type DesktopFixture struct {
	ID        string
	Hostname  string
	NoVNCURL  string
	SSHTarget string
	SSHKey    string
	Owner     string
	Repos     []string

	// ownedByTest is true when TestMain created the desktop and is responsible
	// for terminating it on exit.
	ownedByTest bool
}

// shared fixture, populated by TestMain before tests run.
var fx *DesktopFixture

// cliPath is the compiled binary path used by all exec helpers.
var cliPath string

// configPath is the generated test config written by TestMain.
var configPath string

// moduleRootPath is the repo root, used to resolve packer-dir and other paths.
var moduleRootPath string

// testKeyPairName is the EC2 key pair name imported during setup; deleted in teardown.
var testKeyPairName string

const (
	testRegion = "us-west-2"
	testEnv    = "test"
)

func TestMain(m *testing.M) {
	testRepo := os.Getenv("AI_DESKTOPS_TEST_REPO")

	// All generated files live in a temp dir that is cleaned up on exit.
	tmpDir, err := os.MkdirTemp("", "ai-desktops-integration-*")
	if err != nil {
		fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	moduleRoot, err := findModuleRoot()
	if err != nil {
		fatalf("find module root: %v", err)
	}
	moduleRootPath = moduleRoot

	// AI_DESKTOPS_TEST_CONFIG: use a pre-existing config file (e.g. generated
	// by `make setup-integration`) rather than deriving one from env vars.
	// When set, ssh key generation is skipped if ssh_key_path in the config
	// already points to an existing file.
	var sshKey string
	if preExistingConfig := os.Getenv("AI_DESKTOPS_TEST_CONFIG"); preExistingConfig != "" {
		configPath = preExistingConfig
		fmt.Fprintf(os.Stderr, "integration: using pre-existing config %s\n", configPath)

		// Load config to discover owner, bucket, and ssh_key_path.
		parsedCfg, parseErr := loadConfigYAML(configPath)
		if parseErr != nil {
			fatalf("parse test config %s: %v", configPath, parseErr)
		}
		if parsedCfg.Desktop.SSHKeyPath != "" {
			if _, statErr := os.Stat(parsedCfg.Desktop.SSHKeyPath); statErr == nil {
				sshKey = parsedCfg.Desktop.SSHKeyPath
				fmt.Fprintf(os.Stderr, "integration: using SSH key from config: %s\n", sshKey)
			}
		}
		// Track key pair name for teardown so it can be deleted after the run.
		testKeyPairName = parsedCfg.Desktop.SSHKeyName
		if sshKey == "" {
			// Config doesn't have a usable key; generate one into the temp dir.
			sshKey, err = generateSSHKey(tmpDir)
			if err != nil {
				fatalf("generate SSH key: %v", err)
			}
		}

		// Print startup banner.
		fmt.Fprintf(os.Stderr, "\n=== ai-desktops integration test suite ===\n")
		fmt.Fprintf(os.Stderr, "  region      : %s\n", testRegion)
		fmt.Fprintf(os.Stderr, "  environment : %s\n", testEnv)
		fmt.Fprintf(os.Stderr, "  config      : %s\n", configPath)
		fmt.Fprintf(os.Stderr, "==========================================\n\n")
	} else {
		// Derive config from environment variables (original behaviour).
		owner := requireEnv("AI_DESKTOPS_GITHUB_OWNER")
		bucket := requireEnv("AI_DESKTOPS_TEST_BUCKET")

		// Print startup banner.
		fmt.Fprintf(os.Stderr, "\n=== ai-desktops integration test suite ===\n")
		fmt.Fprintf(os.Stderr, "  region      : %s\n", testRegion)
		fmt.Fprintf(os.Stderr, "  environment : %s\n", testEnv)
		fmt.Fprintf(os.Stderr, "  bucket      : %s\n", bucket)
		fmt.Fprintf(os.Stderr, "  owner       : %s\n", owner)
		fmt.Fprintf(os.Stderr, "==========================================\n\n")

		// Generate a fresh SSH key pair for this test run.
		sshKey, err = generateSSHKey(tmpDir)
		if err != nil {
			fatalf("generate SSH key: %v", err)
		}

		// Write a self-contained config file for this test run.
		configPath, err = writeTestConfig(tmpDir, moduleRoot, bucket, sshKey, owner)
		if err != nil {
			fatalf("write test config: %v", err)
		}
	}

	// Build the CLI binary into the temp dir so tests use a fresh compile.
	bin, err := buildCLI(moduleRoot, tmpDir)
	if err != nil {
		fatalf("build CLI: %v", err)
	}
	cliPath = bin

	// Bootstrap S3 backend and deploy the foundation stack.
	if err := setupFoundation(); err != nil {
		fatalf("setup foundation: %v", err)
	}

	// Build an AMI before creating the desktop.
	if err := buildAMI(); err != nil {
		fatalf("ami build: %v", err)
	}

	// Adopt or create the test desktop.
	if existingID := os.Getenv("AI_DESKTOPS_EXISTING_ID"); existingID != "" {
		fx, err = adoptDesktop(existingID, sshKey, owner)
	} else {
		repos := []string{}
		if testRepo != "" {
			repos = []string{testRepo}
		}
		fx, err = createDesktop(owner, sshKey, repos)
	}
	if err != nil {
		fatalf("fixture setup: %v", err)
	}
	if testRepo != "" && len(fx.Repos) == 0 {
		fx.Repos = []string{testRepo}
	}

	// Run all tests and capture the exit code.
	code := m.Run()

	// Always attempt cleanup when we own the desktop.
	if fx.ownedByTest {
		if err := terminateDesktop(fx.ID); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: terminate %s failed: %v\n", fx.ID, err)
		}
	}

	// Delete AMIs built during this test run.
	teardownAMIs()

	// Delete the EC2 key pair imported by setup-integration, if any.
	if testKeyPairName != "" {
		teardownKeyPair(testKeyPairName)
	}

	// Tear down the foundation stack so test resources don't persist.
	teardownFoundation()

	os.Exit(code)
}

// requireEnv returns the value of an environment variable or exits with a
// helpful message if it is not set.
func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "integration tests require %s to be set\n", key)
		os.Exit(1)
	}
	return v
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL "+format+"\n", args...)
	os.Exit(1)
}

// generateSSHKey creates an ed25519 key pair in dir and returns the private
// key path.  The key is registered in the suite config so desktops are
// provisioned with it.
func generateSSHKey(dir string) (string, error) {
	privKey := filepath.Join(dir, "id_ed25519")
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", privKey, "-N", "", "-C", "ai-desktops-integration-test")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ssh-keygen: %w", err)
	}
	fmt.Fprintf(os.Stderr, "integration: generated SSH key %s\n", privKey)
	return privKey, nil
}

// writeTestConfig writes a minimal config.yaml to dir and returns its path.
// It uses test-specific resource names so it never touches prod/dev state.
func writeTestConfig(dir, moduleRoot, bucket, sshKeyPath, owner string) (string, error) {
	awsProfile := os.Getenv("AWS_PROFILE")

	cfg := fmt.Sprintf(`aws:
  region: %s
  profile: %s
pulumi:
  backend_bucket: %s
  infra_dir: %s
fleet:
  table_name: ai-desktops-fleet-test
  environment: %s
github:
  owner: %s
  github_secret: /ai-desktops/%s/github
  agent_secret: /ai-desktops/%s/agents
desktop:
  instance_type: t3.large
  operator_cidr: 0.0.0.0/0
  ssh_key_path: %s
agent:
  bridge_port: 9445
`, testRegion, awsProfile, bucket, moduleRoot, testEnv, owner, owner, owner, sshKeyPath)

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		return "", err
	}
	fmt.Fprintf(os.Stderr, "integration: wrote test config %s\n", path)
	return path, nil
}

// setupFoundation runs `bootstrap` (creates the S3 bucket) and
// `init-foundation` (deploys VPC, IAM, DynamoDB, security groups).
func setupFoundation() error {
	fmt.Fprintln(os.Stderr, "integration: bootstrapping S3 backend...")
	if _, err := runCLI(context.Background(), 5*time.Minute, "bootstrap", "--config", configPath); err != nil {
		return fmt.Errorf("bootstrap: %w", err)
	}

	fmt.Fprintln(os.Stderr, "integration: deploying foundation stack (env=test)...")
	if _, err := runCLI(context.Background(), 30*time.Minute,
		"init-foundation", "--config", configPath, "--env", testEnv); err != nil {
		return fmt.Errorf("init-foundation: %w", err)
	}
	return nil
}

// buildAMI runs `ami build` to produce a pre-baked AMI before desktop creation.
// This ensures the full integration run exercises the Packer pipeline.
func buildAMI() error {
	fmt.Fprintln(os.Stderr, "integration: building AMI with Packer (this takes 15–20 minutes)...")
	packerDir := filepath.Join(moduleRootPath, "packer")
	_, err := runCLI(context.Background(), 45*time.Minute,
		"ami", "build", "--config", configPath, "--packer-dir", packerDir)
	if err != nil {
		return fmt.Errorf("ami build: %w", err)
	}
	fmt.Fprintln(os.Stderr, "integration: AMI build complete")
	return nil
}

// teardownAMIs deletes any AMIs that were built during this test run so they
// do not accumulate as orphaned resources in AWS.
func teardownAMIs() {
	fmt.Fprintln(os.Stderr, "integration: listing AMIs to clean up...")
	out, err := runCLI(context.Background(), 2*time.Minute,
		"ami", "list", "--config", configPath, "--json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: ami list failed: %v\n", err)
		return
	}

	var entries []struct {
		AMIID  string `json:"ami_id"`
		Region string `json:"region"`
	}
	if err := json.Unmarshal(out, &entries); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: parse ami list output: %v\n", err)
		return
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr, "integration: no AMIs to clean up")
		return
	}

	for _, e := range entries {
		fmt.Fprintf(os.Stderr, "integration: deleting AMI %s in %s...\n", e.AMIID, e.Region)
		_, delErr := runCLI(context.Background(), 2*time.Minute,
			"ami", "delete", e.AMIID, "--config", configPath, "--force")
		if delErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: delete AMI %s failed: %v\n", e.AMIID, delErr)
		}
	}
}

// teardownKeyPair deletes the named EC2 key pair so it does not accumulate
// across test runs. Only called when setup-integration imported a key pair.
func teardownKeyPair(keyName string) {
	fmt.Fprintf(os.Stderr, "integration: deleting EC2 key pair %q...\n", keyName)
	args := []string{"ec2", "delete-key-pair", "--key-name", keyName, "--region", testRegion}
	if profile := os.Getenv("AWS_PROFILE"); profile != "" {
		args = append(args, "--profile", profile)
	}
	cmd := exec.Command("aws", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: delete EC2 key pair %q failed: %v\n", keyName, err)
	} else {
		fmt.Fprintf(os.Stderr, "integration: deleted EC2 key pair %q\n", keyName)
	}
}

// teardownFoundation destroys the foundation Pulumi stack to avoid leaving
// test resources running in AWS after the integration suite finishes.
func teardownFoundation() {
	fmt.Fprintln(os.Stderr, "integration: destroying foundation stack (env=test)...")
	_, err := runCLI(context.Background(), 30*time.Minute,
		"destroy-foundation", "--config", configPath, "--env", testEnv, "--yes")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: foundation teardown failed: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "integration: foundation stack destroyed")
	}
}

// buildCLI compiles the CLI binary into dir and returns its path.
func buildCLI(moduleRoot, dir string) (string, error) {
	bin := filepath.Join(dir, "ai-desktops")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ai-desktops")
	cmd.Dir = moduleRoot
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}
	return bin, nil
}

// findModuleRoot walks up from the current directory to find go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

// createDesktop runs `ai-desktops create` and waits for the desktop to reach
// state=ready.  It returns an owned fixture (will be terminated on cleanup).
func createDesktop(owner, sshKey string, repos []string) (*DesktopFixture, error) {
	args := []string{
		"create",
		"--config", configPath,
		"--json",
		"--github-owner", owner,
		"--env", testEnv,
	}
	for _, r := range repos {
		args = append(args, "--repo", r)
	}

	fmt.Fprintln(os.Stderr, "integration: creating desktop...")
	out, err := runCLI(context.Background(), 30*time.Minute, args...)
	if err != nil {
		return nil, fmt.Errorf("create: %w\noutput: %s", err, out)
	}

	var result struct {
		DesktopID string `json:"desktop_id"`
		Hostname  string `json:"hostname"`
		NoVNCURL  string `json:"novnc_url"`
		SSHTarget string `json:"ssh_target"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse create output: %w\nraw: %s", err, out)
	}
	if result.DesktopID == "" {
		return nil, fmt.Errorf("create returned empty desktop_id; raw output: %s", out)
	}

	fmt.Fprintf(os.Stderr, "integration: desktop %s created, waiting for ready...\n", result.DesktopID)
	if err := waitForState(result.DesktopID, "ready", 20*time.Minute); err != nil {
		return nil, fmt.Errorf("wait for ready: %w", err)
	}

	return &DesktopFixture{
		ID:          result.DesktopID,
		Hostname:    result.Hostname,
		NoVNCURL:    result.NoVNCURL,
		SSHTarget:   result.SSHTarget,
		SSHKey:      sshKey,
		Owner:       owner,
		Repos:       repos,
		ownedByTest: true,
	}, nil
}

// adoptDesktop reads an existing desktop's state and returns an unowned
// fixture (will not be terminated on cleanup).
func adoptDesktop(id, sshKey, owner string) (*DesktopFixture, error) {
	out, err := runCLI(context.Background(), 30*time.Second,
		"status", id, "--config", configPath, "--json")
	if err != nil {
		return nil, fmt.Errorf("status %s: %w", id, err)
	}

	var d struct {
		DesktopID   string   `json:"desktop_id"`
		Hostname    string   `json:"hostname"`
		NoVNCURL    string   `json:"novnc_url"`
		SSHTarget   string   `json:"ssh_target"`
		State       string   `json:"lifecycle_state"`
		GitHubOwner string   `json:"github_owner"`
		Repos       []string `json:"repos"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("parse status: %w\nraw: %s", err, out)
	}
	if d.State != "ready" {
		return nil, fmt.Errorf("existing desktop %s is in state %q (want ready)", id, d.State)
	}

	adoptOwner := d.GitHubOwner
	if adoptOwner == "" {
		adoptOwner = owner
	}

	return &DesktopFixture{
		ID:          d.DesktopID,
		Hostname:    d.Hostname,
		NoVNCURL:    d.NoVNCURL,
		SSHTarget:   d.SSHTarget,
		SSHKey:      sshKey,
		Owner:       adoptOwner,
		Repos:       d.Repos,
		ownedByTest: false,
	}, nil
}

// terminateDesktop runs `ai-desktops terminate <id>`.
func terminateDesktop(id string) error {
	fmt.Fprintf(os.Stderr, "integration: terminating desktop %s...\n", id)
	_, err := runCLI(context.Background(), 30*time.Minute,
		"terminate", id, "--config", configPath, "--force")
	return err
}

// waitForState polls `ai-desktops status <id>` until lifecycle_state matches
// want or the deadline elapses. It prints progress every 30 seconds so CI logs
// do not appear to hang during long-running operations like TLS provisioning.
//
// When want is "ready" and the fixture has an SSH key, it also probes TCP port
// 22 after DynamoDB reports the state, so callers do not race against sshd
// startup after an EC2 start.
func waitForState(id, want string, timeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	lastLog := start
	for time.Now().Before(deadline) {
		out, err := runCLI(context.Background(), 30*time.Second,
			"status", id, "--config", configPath, "--json")
		if err == nil {
			var d struct {
				State    string `json:"lifecycle_state"`
				Hostname string `json:"hostname"`
			}
			if json.Unmarshal(out, &d) == nil {
				if d.State == want {
					// For "ready", also verify SSH port is reachable so callers
					// don't race sshd startup after an EC2 start.
					if want == "ready" {
						host := d.Hostname
						if host == "" {
							host = fx.Hostname
						}
						if host != "" && !tcpReachable(host, 22, 5*time.Second) {
							if time.Since(lastLog) >= 30*time.Second {
								fmt.Fprintf(os.Stderr,
									"integration: desktop %s state=%q but SSH not yet reachable (elapsed %s)\n",
									id, want, time.Since(start).Round(time.Second))
								lastLog = time.Now()
							}
							time.Sleep(15 * time.Second)
							continue
						}
					}
					fmt.Fprintf(os.Stderr, "integration: desktop %s reached state %q (elapsed %s)\n",
						id, want, time.Since(start).Round(time.Second))
					return nil
				}
				if time.Since(lastLog) >= 30*time.Second {
					fmt.Fprintf(os.Stderr, "integration: desktop %s state=%q, waiting for %q (elapsed %s)\n",
						id, d.State, want, time.Since(start).Round(time.Second))
					lastLog = time.Now()
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
	return fmt.Errorf("timed out waiting for desktop %s to reach state %q after %s", id, want, timeout)
}

// tcpReachable returns true if host:port accepts a TCP connection within timeout.
func tcpReachable(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// runCLI executes the compiled CLI binary with the given arguments and returns
// stdout.  stderr is forwarded to os.Stderr for visibility during test runs.
func runCLI(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath, args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("cli %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// minimalConfig holds the fields from config.yaml that TestMain needs when
// reading a pre-existing config via AI_DESKTOPS_TEST_CONFIG.
type minimalConfig struct {
	AWS struct {
		Region string `yaml:"region"`
	} `yaml:"aws"`
	Desktop struct {
		SSHKeyPath string `yaml:"ssh_key_path"`
		SSHKeyName string `yaml:"ssh_key_name"`
	} `yaml:"desktop"`
	GitHub struct {
		Owner string `yaml:"owner"`
	} `yaml:"github"`
	Pulumi struct {
		BackendBucket string `yaml:"backend_bucket"`
	} `yaml:"pulumi"`
}

// loadConfigYAML parses enough of path to extract fields used by TestMain.
func loadConfigYAML(path string) (*minimalConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c minimalConfig
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
