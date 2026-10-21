package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	smithy "github.com/aws/smithy-go"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/dnszone"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// githubAPIBase is the GitHub REST API base URL. Overridden in tests.
var githubAPIBase = "https://api.github.com"
var publicIPURL = "https://checkip.amazonaws.com"

const defaultPublicAMIOwner = "819363892004"

var (
	setupConfigOut          string
	setupAllowExisting      bool
	setupNonInteractive     bool
	setupRotateGitHubSSHKey bool
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure desktopctl and its AWS and GitHub credentials",
	Long: `setup walks through configuring ~/.config/desktopctl/config.yaml and provisioning
GitHub credentials (token + SSH key) in AWS Secrets Manager.

When run on an existing configuration all current values are used as defaults.
Secrets scheduled for deletion are restored when the configured AWS region,
environment, and GitHub owner match. New values replace matching secret fields;
unspecified values are kept. Unrelated existing names require --allow-existing.

For scripts, pass --non-interactive and use flags or environment variables.
Flags take precedence over environment, which takes precedence over config.
Use environment variables for tokens and API keys to keep them out of process
arguments and shell history.

The wizard will:
  1. Collect AWS and GitHub configuration interactively
  2. Optionally update the GitHub token and rotate its SSH key
  3. Store updated credentials in AWS Secrets Manager
  4. Create or reuse the desktop DNS zone, when supplied
  5. Write ~/.config/desktopctl/config.yaml (or --config-out path)`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupConfigOut, "config-out", "", "write config to this path instead of ~/.config/desktopctl/config.yaml")
	setupCmd.Flags().BoolVar(&setupAllowExisting, "allow-existing", false, "explicitly reuse existing AWS secrets and EC2 key pair for a different configured identity")
	setupCmd.Flags().BoolVar(&setupNonInteractive, "non-interactive", false, "use flags, environment, and existing config without prompts")
	setupCmd.Flags().BoolVar(&setupRotateGitHubSSHKey, "rotate-github-ssh-key", false, "generate and register a new GitHub SSH key (non-interactive only)")
	for _, f := range []struct{ name, help string }{
		{"environment", "fleet environment (dev|prod|test)"},
		{"dns-zone", "desktop DNS zone to create or reuse in Route 53 (DESKTOPCTL_DNS_ZONE)"},
		{"public-ami-owner", "trusted public desktop AMI publisher AWS account (DESKTOPCTL_PUBLIC_AMI_OWNER)"},
		{"backend-bucket", "dedicated S3 bucket for Pulumi state"},
		{"github-owner", "GitHub organization or user"},
		{"github-token", "GitHub token (prefer DESKTOPCTL_GITHUB_TOKEN to keep it out of process arguments)"},
		{"git-user-name", "Git commit author name"},
		{"git-user-email", "Git commit author email"},
		{"operator-cidr", "public IPv4 /32 allowed to SSH"},
		{"ssh-key-path", "local SSH private key path"},
		{"ssh-key-name", "EC2 key pair name"},
		{"claude-oauth-token", "Claude Code OAuth token (prefer CLAUDE_CODE_OAUTH_TOKEN)"},
		{"openai-api-key", "OpenAI API key (prefer OPENAI_API_KEY)"},
		{"gemini-api-key", "Gemini API key (prefer GEMINI_API_KEY)"},
	} {
		setupCmd.Flags().String(f.name, "", f.help)
	}
	setupCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if setupNonInteractive {
			return runNonInteractiveSetup(cmd)
		}
		if setupRotateGitHubSSHKey || anySetupValueFlagChanged(cmd) {
			return fmt.Errorf("setup input flags require --non-interactive")
		}
		return runSetup(cmd, args)
	}
	rootCmd.AddCommand(setupCmd)
}

type setupAnswers struct {
	AWSRegion      string
	AWSProfile     string
	BackendBucket  string
	Environment    string
	DNSZone        string
	PublicAMIOwner string
	GitHubOwner    string
	GitHubToken    string
	GitUserName    string
	GitUserEmail   string
	AnthropicKey   string
	OpenAIKey      string
	GeminiKey      string
	SSHKeyPath     string
	SSHKeyName     string
	OperatorCIDR   string
}

func setupSameIdentity(current *config.Config, region, environment, owner string) bool {
	return current != nil && current.AWS.Region == region && current.Fleet.Environment == environment && current.GitHub.Owner == owner
}

func detectPublicIPv4(ctx context.Context) (netip.Addr, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("public IP service returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return netip.Addr{}, err
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return netip.Addr{}, fmt.Errorf("public IP service did not return a public IPv4 address")
	}
	return ip, nil
}

func validateOperatorCIDR(value string) error {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() || prefix.Addr().IsPrivate() || prefix.Bits() != 32 {
		return fmt.Errorf("SSH source must be a public IPv4 address with /32, for example 203.0.113.42/32")
	}
	return nil
}

func validatePublicAMIOwner(value string) error {
	if value == "" {
		return nil // An explicit empty value disables public AMI discovery.
	}
	if len(value) != 12 {
		return fmt.Errorf("public AMI owner must be a 12-digit AWS account ID")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("public AMI owner must be a 12-digit AWS account ID")
		}
	}
	return nil
}

func setupPublicAMIOwnerDefault(desktop config.DesktopConfig) string {
	if desktop.PublicAMIDiscoveryDisabled {
		return ""
	}
	if desktop.PublicAMIOwner != "" {
		return desktop.PublicAMIOwner
	}
	return defaultPublicAMIOwner
}

func applySetupPublicAMIOwner(desktop *config.DesktopConfig, owner string) {
	desktop.PublicAMIOwner = owner
	desktop.PublicAMIDiscoveryDisabled = owner == ""
}

func runSetup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	reader := bufio.NewReader(os.Stdin)
	saved, err := setupSavedConfig()
	if err != nil {
		return fmt.Errorf("load saved setup config: %w", err)
	}

	cfgPath := setupConfigOut
	if cfgPath == "" {
		cfgPath = cfgFile
	}
	if cfgPath == "" {
		var err error
		cfgPath, err = config.DefaultPath()
		if err != nil {
			return err
		}
	}

	_, statErr := os.Stat(cfgPath)
	isExisting := statErr == nil

	if isExisting {
		fmt.Println("Welcome to desktopctl setup — updating existing configuration.")
		fmt.Printf("Config: %s\n", cfgPath)
		fmt.Println("Press Enter to keep the current value shown in brackets.")
	} else {
		fmt.Println("Welcome to desktopctl setup.")
		fmt.Printf("This wizard will create %s and provision your GitHub credentials.\n", cfgPath)
	}
	fmt.Println()

	a := &setupAnswers{}

	// Seed defaults from the loaded config (cfg is the package-level loaded config).
	existingRegion := "us-east-1"
	existingProfile := "default"
	existingEnv := "dev"
	existingBucket := ""
	existingOwner := ""
	existingGitUserName := ""
	existingGitUserEmail := ""
	if cfg != nil {
		if cfg.AWS.Region != "" {
			existingRegion = cfg.AWS.Region
		}
		if cfg.AWS.Profile != "" {
			existingProfile = cfg.AWS.Profile
		}
		if cfg.Fleet.Environment != "" {
			existingEnv = cfg.Fleet.Environment
		}
		if cfg.Pulumi.BackendBucket != "" {
			existingBucket = cfg.Pulumi.BackendBucket
		}
		if cfg.GitHub.Owner != "" {
			existingOwner = cfg.GitHub.Owner
		}
		if cfg.GitHub.GitUserName != "" {
			existingGitUserName = cfg.GitHub.GitUserName
		}
		if cfg.GitHub.GitUserEmail != "" {
			existingGitUserEmail = cfg.GitHub.GitUserEmail
		}
	}

	fmt.Println("── AWS ──────────────────────────────────────────────")
	a.AWSRegion = prompt(reader, fmt.Sprintf("AWS region [%s]", existingRegion), existingRegion)
	a.AWSProfile = prompt(reader, fmt.Sprintf("AWS profile [%s]", existingProfile), existingProfile)
	bucketLabel := "Pulumi state S3 bucket"
	if existingBucket != "" {
		bucketLabel = fmt.Sprintf("Pulumi state S3 bucket [%s]", existingBucket)
	}
	a.BackendBucket = prompt(reader, bucketLabel, existingBucket)
	a.Environment = prompt(reader, fmt.Sprintf("Fleet environment (dev/prod/test) [%s]", existingEnv), existingEnv)
	zoneLabel := "Desktop DNS zone (for example desktops.example.com; blank for SSH-only)"
	if cfg != nil && cfg.DNS.Zone != "" {
		zoneLabel = fmt.Sprintf("Desktop DNS zone [%s]", cfg.DNS.Zone)
	}
	a.DNSZone = prompt(reader, zoneLabel, cfg.DNS.Zone)
	if a.DNSZone != "" {
		a.DNSZone, err = dnszone.Normalize(a.DNSZone)
		if err != nil {
			return fmt.Errorf("desktop DNS zone: %w", err)
		}
	}
	publicAMIOwner := defaultPublicAMIOwner
	if cfg != nil {
		publicAMIOwner = setupPublicAMIOwnerDefault(cfg.Desktop)
	}
	publicAMILabel := "Trusted public desktop AMI publisher account (disabled; enter an account to enable)"
	if publicAMIOwner != "" {
		publicAMILabel = fmt.Sprintf("Trusted public desktop AMI publisher account [%s]", publicAMIOwner)
	}
	a.PublicAMIOwner = prompt(reader, publicAMILabel, publicAMIOwner)
	if err := validatePublicAMIOwner(a.PublicAMIOwner); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("── SSH (Desktop Access) ─────────────────────────────")
	fmt.Println("The security group needs the public IPv4 address of the machine that will run desktopctl.")
	fmt.Println("SSH access will be limited to that address; update it if the machine's public IP changes.")
	currentCIDR := ""
	if ip, err := detectPublicIPv4(ctx); err == nil {
		currentCIDR = ip.String() + "/32"
		fmt.Printf("Detected this machine's public IPv4: %s\n", ip)
	} else {
		fmt.Printf("Could not detect public IPv4 automatically: %v\n", err)
	}
	defaultCIDR := currentCIDR
	if defaultCIDR == "" && cfg != nil {
		defaultCIDR = cfg.Desktop.OperatorCIDR
	}
	a.OperatorCIDR = prompt(reader, fmt.Sprintf("SSH source IPv4 CIDR [%s]", defaultCIDR), defaultCIDR)
	if err := validateOperatorCIDR(a.OperatorCIDR); err != nil {
		return err
	}
	fmt.Printf("Only %s will be allowed to connect to desktop SSH.\n", a.OperatorCIDR)

	// Detect existing SSH key path from config or auto-detect from ~/.ssh.
	existingSSHKeyPath := ""
	existingSSHKeyName := ""
	if cfg != nil {
		existingSSHKeyPath = cfg.Desktop.SSHKeyPath
		existingSSHKeyName = cfg.Desktop.SSHKeyName
	}
	if existingSSHKeyPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			for _, f := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
				p := filepath.Join(home, ".ssh", f)
				if _, err := os.Stat(p); err == nil {
					existingSSHKeyPath = p
					break
				}
			}
		}
	}

	sshKeyPathLabel := "Local SSH private key path (used to SSH into desktops)"
	if existingSSHKeyPath != "" {
		sshKeyPathLabel = fmt.Sprintf("Local SSH private key path [%s]", existingSSHKeyPath)
	}
	a.SSHKeyPath = prompt(reader, sshKeyPathLabel, existingSSHKeyPath)

	if a.SSHKeyPath != "" {
		// Verify the key file exists.
		if _, err := os.Stat(a.SSHKeyPath); err != nil {
			fmt.Printf("  Warning: key file %s not found — verify the path before creating desktops.\n", a.SSHKeyPath)
		} else {
			// Offer to register the public key as an EC2 key pair.
			// a.GitHubOwner hasn't been prompted yet (GitHub section follows
			// SSH), so use existingOwner from the loaded config as the suffix.
			keyOwner := existingOwner
			if keyOwner == "" {
				keyOwner = "operator"
			}
			defaultKeyName := fmt.Sprintf("desktopctl-%s-%s-%s", a.Environment, a.AWSRegion, keyOwner)
			if existingSSHKeyName != "" {
				defaultKeyName = existingSSHKeyName
			}
			fmt.Printf("Register %s.pub as EC2 key pair [y/N]: ", a.SSHKeyPath)
			registerAnswer, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(registerAnswer)) == "y" {
				keyNameLabel := fmt.Sprintf("EC2 key pair name [%s]", defaultKeyName)
				a.SSHKeyName = prompt(reader, keyNameLabel, defaultKeyName)

				pubPath := a.SSHKeyPath + ".pub"
				pubBytes, err := os.ReadFile(pubPath)
				if err != nil {
					fmt.Printf("  Warning: could not read %s: %v\n  EC2 key pair not registered.\n", pubPath, err)
					a.SSHKeyName = existingSSHKeyName
				} else {
					ec2Cfg, err := awscfg.LoadDefaultConfig(ctx,
						awscfg.WithRegion(a.AWSRegion),
						awscfg.WithSharedConfigProfile(a.AWSProfile),
					)
					if err != nil {
						fmt.Printf("  Warning: could not load AWS config: %v\n  EC2 key pair not registered.\n", err)
						a.SSHKeyName = existingSSHKeyName
					} else {
						fmt.Printf("Registering EC2 key pair %q in %s... ", a.SSHKeyName, a.AWSRegion)
						canReuse := setupAllowExisting || setupSameIdentity(saved, a.AWSRegion, a.Environment, existingOwner)
						if err := importEC2KeyPair(ctx, ec2Cfg, a.SSHKeyName, strings.TrimSpace(string(pubBytes)), !canReuse); err != nil {
							fmt.Println("✗")
							if !canReuse {
								return fmt.Errorf("register fresh EC2 key pair: %w", err)
							}
							fmt.Printf("  Warning: %v\n  EC2 key pair not registered — set ssh_key_name manually if needed.\n", err)
							a.SSHKeyName = existingSSHKeyName
						} else {
							fmt.Println("✓")
						}
					}
				}
			} else {
				a.SSHKeyName = existingSSHKeyName
			}
		}
	}

	fmt.Println()
	fmt.Println("── GitHub ───────────────────────────────────────────")
	ownerLabel := "GitHub owner (org or user)"
	if existingOwner != "" {
		ownerLabel = fmt.Sprintf("GitHub owner (org or user) [%s]", existingOwner)
	}
	a.GitHubOwner = prompt(reader, ownerLabel, existingOwner)
	if a.GitHubOwner == "" {
		return fmt.Errorf("github owner is required")
	}

	gitUserNameLabel := "Git commit author name"
	if existingGitUserName != "" {
		gitUserNameLabel = fmt.Sprintf("Git commit author name [%s]", existingGitUserName)
	}
	a.GitUserName = prompt(reader, gitUserNameLabel, existingGitUserName)

	gitUserEmailLabel := "Git commit author email"
	if existingGitUserEmail != "" {
		gitUserEmailLabel = fmt.Sprintf("Git commit author email [%s]", existingGitUserEmail)
	}
	a.GitUserEmail = prompt(reader, gitUserEmailLabel, existingGitUserEmail)
	sameIdentity := setupSameIdentity(saved, a.AWSRegion, a.Environment, a.GitHubOwner)

	secretPath := fmt.Sprintf("/desktopctl/%s/%s/%s/github", a.Environment, a.AWSRegion, a.GitHubOwner)
	githubSecretExists := false
	{
		awsCfg, err := awscfg.LoadDefaultConfig(ctx,
			awscfg.WithRegion(a.AWSRegion),
			awscfg.WithSharedConfigProfile(a.AWSProfile),
		)
		if err != nil {
			return fmt.Errorf("load AWS config for fresh setup: %w", err)
		}
		for _, path := range []string{
			secretPath,
			fmt.Sprintf("/desktopctl/%s/%s/%s/agents", a.Environment, a.AWSRegion, a.GitHubOwner),
			fmt.Sprintf("/desktopctl/%s/%s/%s", a.Environment, a.AWSRegion, a.GitHubOwner),
		} {
			exists, err := setupSecretExists(ctx, awsCfg, path)
			if err != nil {
				return err
			}
			if path == secretPath {
				githubSecretExists = exists
			}
			if exists && !setupAllowExisting && !sameIdentity {
				return fmt.Errorf("AWS secret %q already exists; fresh setup refuses to reuse it", path)
			}
			if exists && (path == secretPath || strings.HasSuffix(path, "/agents")) {
				if err := restoreSetupSecretIfPending(ctx, awsCfg, path); err != nil {
					return err
				}
			}
		}
	}

	githubAwsCfg, err := awscfg.LoadDefaultConfig(ctx,
		awscfg.WithRegion(a.AWSRegion),
		awscfg.WithSharedConfigProfile(a.AWSProfile),
	)
	if err != nil {
		return fmt.Errorf("load AWS config for GitHub secret: %w", err)
	}
	githubValues := map[string]string{}
	if githubSecretExists {
		githubValues, err = fetchSecretMap(ctx, githubAwsCfg, secretPath)
		if err != nil {
			return fmt.Errorf("fetch existing GitHub secret: %w", err)
		}
	}
	updateToken := !githubSecretExists || githubValues["github_token"] == ""
	rotateGitHubKey := !githubSecretExists || githubValues["ssh_private_key"] == "" || githubValues["ssh_public_key"] == ""
	if githubSecretExists {
		fmt.Print("\nUpdate GitHub token? [y/N]: ")
		answer, _ := reader.ReadString('\n')
		updateToken = updateToken || strings.EqualFold(strings.TrimSpace(answer), "y")
		fmt.Print("Rotate GitHub SSH key? [y/N]: ")
		answer, _ = reader.ReadString('\n')
		rotateGitHubKey = rotateGitHubKey || strings.EqualFold(strings.TrimSpace(answer), "y")
	}
	if updateToken {
		fmt.Println()
		fmt.Println("Before continuing, create a GitHub personal access token with these scopes:")
		fmt.Println("  • admin:public_key  (register SSH keys)")
		fmt.Println("  • repo              (clone, push, PRs)")
		fmt.Println("  • workflow          (GitHub Actions)")
		fmt.Println("  • read:user         (identity)")
		fmt.Println("  • read:org          (optional; gh CLI org features)")
		fmt.Println("  • read:packages     (required if you will build the public desktopctl AMI)")
		fmt.Println()
		fmt.Println("Create the token at: https://github.com/settings/tokens/new")
		fmt.Println("Use a classic token with those scopes. Setup stores it in AWS Secrets Manager;")
		fmt.Println("ami build can use this stored token; GITHUB_NPM_TOKEN can override it.")
		fmt.Print("Paste the token here (input hidden): ")

		tokenBytes, err := term.ReadPassword(int(syscall.Stdin)) //nolint:gosec
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read token: %w", err)
		}
		a.GitHubToken = strings.TrimSpace(string(tokenBytes))
		if a.GitHubToken == "" {
			return fmt.Errorf("token is required")
		}

		// Validate token
		fmt.Print("Validating GitHub token... ")
		if err := validateGitHubToken(ctx, a.GitHubToken); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("token validation failed: %w", err)
		}
		fmt.Println("✓")

		githubValues["github_token"] = a.GitHubToken
	}
	if rotateGitHubKey {
		fmt.Println()
		fmt.Printf("── Generating SSH key ───────────────────────────────\n")
		if githubSecretExists {
			fmt.Println("The previous GitHub SSH key remains registered; remove it after verifying the new key.")
		}
		fmt.Printf("Generating Ed25519 SSH key pair for owner: %s\n", a.GitHubOwner)

		pub, priv, err := generateSSHKeyPair(a.GitHubOwner)
		if err != nil {
			return fmt.Errorf("generate SSH key pair: %w", err)
		}
		fmt.Printf("  Public key:  %s\n", strings.TrimSpace(pub))

		// Register SSH key with GitHub
		fmt.Printf("Registering SSH public key with GitHub account '%s'... ", a.GitHubOwner)
		if err := registerGitHubSSHKey(ctx, githubValues["github_token"], "desktopctl/"+a.GitHubOwner, pub); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("register SSH key: %w", err)
		}
		fmt.Println("✓")
		githubValues["ssh_private_key"] = priv
		githubValues["ssh_public_key"] = strings.TrimSpace(pub)
	}

	if updateToken || rotateGitHubKey {
		// Store/update secret in AWS Secrets Manager
		fmt.Println()
		fmt.Printf("── Storing secret ───────────────────────────────────\n")
		fmt.Printf("Storing secret at %s in %s... ", secretPath, a.AWSRegion)
		secretValue, err := json.Marshal(githubValues)
		if err != nil {
			fmt.Println("✗")
			return fmt.Errorf("build secret JSON: %w", err)
		}
		if err := storeSecret(ctx, githubAwsCfg, secretPath, string(secretValue), a.GitHubOwner, a.Environment); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("store secret: %w", err)
		}
		fmt.Println("✓")
	} else {
		fmt.Println("  GitHub token and SSH key unchanged.")
	}

	// Collect AI provider API keys.
	// Preserve any existing custom secret path; only derive the default on first run.
	agentSecretPath := fmt.Sprintf("/desktopctl/%s/%s/%s/agents", a.Environment, a.AWSRegion, a.GitHubOwner)
	if sameIdentity && cfg.GitHub.AgentSecret != "" {
		agentSecretPath = cfg.GitHub.AgentSecret
	}
	updateAgentKeys := true
	if sameIdentity && cfg.GitHub.AgentSecret != "" {
		fmt.Println()
		fmt.Print("Update AI provider API keys? [y/N]: ")
		answer, _ := reader.ReadString('\n')
		updateAgentKeys = strings.TrimSpace(strings.ToLower(answer)) == "y"
	}

	if updateAgentKeys {
		fmt.Println()
		fmt.Println("── AI Provider Keys ─────────────────────────────────")
		fmt.Println("Enter API keys for AI providers (press Enter to keep existing value).")
		fmt.Println("Keys are stored in AWS Secrets Manager at:", agentSecretPath)
		fmt.Println()

		agentAwsCfg, err := awscfg.LoadDefaultConfig(ctx,
			awscfg.WithRegion(a.AWSRegion),
			awscfg.WithSharedConfigProfile(a.AWSProfile),
		)
		if err != nil {
			return fmt.Errorf("load AWS config: %w", err)
		}

		existing, err := fetchAgentSecret(ctx, agentAwsCfg, agentSecretPath)
		if err != nil {
			return fmt.Errorf("fetch existing agent secret: %w", err)
		}

		hint := func(key string) string {
			if existing[key] != "" {
				return " [set, Enter to keep]"
			}
			return " [not set, Enter to skip]"
		}

		fmt.Printf("Claude Code OAuth token (CLAUDE_CODE_OAUTH_TOKEN)%s: ", hint("CLAUDE_CODE_OAUTH_TOKEN"))
		anthropicBytes, err := term.ReadPassword(int(syscall.Stdin)) //nolint:gosec
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read claude code oauth token: %w", err)
		}
		a.AnthropicKey = strings.TrimSpace(string(anthropicBytes))

		fmt.Printf("OpenAI API key (OPENAI_API_KEY)%s: ", hint("OPENAI_API_KEY"))
		openaiBytes, err := term.ReadPassword(int(syscall.Stdin)) //nolint:gosec
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read openai key: %w", err)
		}
		a.OpenAIKey = strings.TrimSpace(string(openaiBytes))

		fmt.Printf("Gemini API key (GEMINI_API_KEY)%s: ", hint("GEMINI_API_KEY"))
		geminiBytes, err := term.ReadPassword(int(syscall.Stdin)) //nolint:gosec
		fmt.Println()
		if err != nil {
			return fmt.Errorf("read gemini key: %w", err)
		}
		a.GeminiKey = strings.TrimSpace(string(geminiBytes))

		merged := mergeAgentKeys(existing, a.AnthropicKey, a.OpenAIKey, a.GeminiKey)
		if len(merged) == 0 {
			fmt.Println("  No AI provider keys set — skipping agent secret.")
			updateAgentKeys = false
		} else {
			fmt.Println()
			fmt.Printf("── Storing agent secret ─────────────────────────────\n")
			fmt.Printf("Storing agent secret at %s in %s... ", agentSecretPath, a.AWSRegion)

			b, err := json.Marshal(merged)
			if err != nil {
				fmt.Println("✗")
				return fmt.Errorf("build agent secret JSON: %w", err)
			}

			if err := storeAgentSecret(ctx, agentAwsCfg, agentSecretPath, string(b), a.GitHubOwner, a.Environment); err != nil {
				fmt.Println("✗")
				return fmt.Errorf("store agent secret: %w", err)
			}
			fmt.Println("✓")
		}
	} else {
		fmt.Println("  AI provider keys unchanged.")
	}

	// Write config (always — idempotent).
	// Start from the existing loaded config so fields not touched by the wizard
	// (active_ami, operator_cidr, ssh_key_name, infra_dir, agent settings, etc.)
	// are preserved.
	fmt.Println()
	fmt.Printf("── Writing config ───────────────────────────────────\n")
	newCfg := cfg
	if newCfg == nil {
		newCfg = &config.Config{}
	}
	if newCfg.GitHub.Owner != a.GitHubOwner {
		newCfg.GitHub.GitHubSecret = ""
		newCfg.GitHub.AgentSecret = ""
		newCfg.Operator.Secret = ""
	}
	newCfg.GitHub.Owner = a.GitHubOwner
	if newCfg.Fleet.Environment != a.Environment {
		newCfg.Fleet.TableName = ""
		newCfg.Fleet.AMITableName = ""
		newCfg.Pool.TableName = ""
		newCfg.GitHub.GitHubSecret = ""
		newCfg.GitHub.AgentSecret = ""
		newCfg.Operator.Secret = ""
	}
	newCfg.Fleet.Environment = a.Environment
	newCfg.SetRegion(a.AWSRegion)
	newCfg.AWS.Profile = a.AWSProfile
	newCfg.Pulumi.BackendBucket = a.BackendBucket
	newCfg.DNS.Zone = a.DNSZone
	newCfg.GitHub.GitUserName = a.GitUserName
	newCfg.GitHub.GitUserEmail = a.GitUserEmail
	newCfg.Desktop.OperatorCIDR = a.OperatorCIDR
	applySetupPublicAMIOwner(&newCfg.Desktop, a.PublicAMIOwner)
	if a.SSHKeyPath != "" {
		newCfg.Desktop.SSHKeyPath = a.SSHKeyPath
	}
	if a.SSHKeyName != "" {
		newCfg.Desktop.SSHKeyName = a.SSHKeyName
	}
	// Preserve the existing secret path when the operator declined rotation;
	// overwrite only when a new token was stored (or on first run).
	if updateToken || cfg == nil || cfg.GitHub.GitHubSecret == "" {
		newCfg.GitHub.GitHubSecret = secretPath
	}
	if updateAgentKeys {
		newCfg.GitHub.AgentSecret = agentSecretPath
	}
	newCfg.Defaults()
	if err := ensureSetupDNS(ctx, a, newCfg); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	fmt.Printf("Writing %s... ", cfgPath)
	if err := newCfg.Save(cfgPath); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println("✓")

	fmt.Println()
	fmt.Println("Setup complete.")
	fmt.Printf("Config: %s\n", cfgPath)
	fmt.Printf("AWS: profile %s, %s, environment %s\n", a.AWSProfile, a.AWSRegion, a.Environment)
	fmt.Printf("SSH source: %s (update this if the operator machine's public IP changes)\n", a.OperatorCIDR)
	if a.DNSZone != "" {
		fmt.Printf("  desktopctl --config %q dns check\n", cfgPath)
	}
	if !isExisting {
		fmt.Println("Next steps:")
		fmt.Printf("  desktopctl --config %q bootstrap\n", cfgPath)
		fmt.Printf("  desktopctl --config %q init-foundation\n", cfgPath)
	}
	fmt.Printf("  desktopctl --config %q create --github-owner %s --repo <owner/repo>\n", cfgPath, a.GitHubOwner)
	fmt.Printf("For a public AMI build, run desktopctl --config %q ami build --public; the stored token needs read:packages.\n", cfgPath)
	return nil
}

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	fmt.Printf("%s: ", label)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}

var requiredScopes = []string{"repo", "workflow", "admin:public_key"}

func validateGitHubToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIBase+"/user", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d — check token scopes", resp.StatusCode)
	}

	// Verify required scopes from the X-OAuth-Scopes response header.
	// Fine-grained tokens don't expose this header; skip scope check in that case.
	scopeHeader := resp.Header.Get("X-OAuth-Scopes")
	if scopeHeader != "" {
		grantedScopes := make(map[string]bool)
		for _, s := range strings.Split(scopeHeader, ",") {
			grantedScopes[strings.TrimSpace(s)] = true
		}
		var missing []string
		for _, required := range requiredScopes {
			if !grantedScopes[required] {
				missing = append(missing, required)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("token is missing required scopes: %s", strings.Join(missing, ", "))
		}
	}

	return nil
}

// importEC2KeyPair imports a public key into AWS EC2 as a named key pair.
// An existing named key pair is reused only when it contains the same public key.
func importEC2KeyPair(ctx context.Context, awsCfg aws.Config, name, publicKeyMaterial string, fresh ...bool) error {
	svc := ec2.NewFromConfig(awsCfg)
	_, err := svc.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(name),
		PublicKeyMaterial: []byte(publicKeyMaterial),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeKeyPair,
				Tags: []ec2types.Tag{
					{Key: aws.String("desktopctl"), Value: aws.String("true")},
				},
			},
		},
	})
	if err != nil {
		// Existing configurations can retain their registered key pair.
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidKeyPair.Duplicate" {
			if len(fresh) > 0 && fresh[0] {
				return fmt.Errorf("EC2 key pair %q already exists; fresh setup refuses reuse", name)
			}
			listed, err := svc.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{KeyNames: []string{name}, IncludePublicKey: aws.Bool(true)})
			if err != nil {
				return fmt.Errorf("describe existing EC2 key pair %q: %w", name, err)
			}
			if len(listed.KeyPairs) != 1 || aws.ToString(listed.KeyPairs[0].PublicKey) == "" {
				return fmt.Errorf("cannot verify public key for existing EC2 key pair %q", name)
			}
			provided, _, _, _, err := ssh.ParseAuthorizedKey([]byte(publicKeyMaterial))
			if err != nil {
				return fmt.Errorf("parse local public key: %w", err)
			}
			registered, _, _, _, err := ssh.ParseAuthorizedKey([]byte(aws.ToString(listed.KeyPairs[0].PublicKey)))
			if err != nil {
				return fmt.Errorf("parse EC2 public key %q: %w", name, err)
			}
			if !bytes.Equal(provided.Marshal(), registered.Marshal()) {
				return fmt.Errorf("EC2 key pair %q does not match the local SSH key; choose a new --ssh-key-name", name)
			}
			return nil
		}
		return err
	}
	return nil
}

// generateSSHKeyPair generates an Ed25519 key pair and returns (pubKeyLine, privKeyPEM, error).
func generateSSHKeyPair(owner string) (string, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	pubLine := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(sshPub)), "\n") + " desktopctl/" + owner + "\n"

	privPEM, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", "", err
	}

	return pubLine, string(pem.EncodeToMemory(privPEM)), nil
}

func registerGitHubSSHKey(ctx context.Context, token, title, pubKey string) error {
	body, err := json.Marshal(map[string]string{
		"title": title,
		"key":   strings.TrimSpace(pubKey),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubAPIBase+"/user/keys",
		strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("HTTP %d — failed to register SSH key", resp.StatusCode)
	}
	return nil
}

func buildSecretJSON(token, privKey, pubKey string) (string, error) {
	m := map[string]string{
		"github_token":    token,
		"ssh_private_key": privKey,
		"ssh_public_key":  strings.TrimSpace(pubKey),
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func fetchAgentSecret(ctx context.Context, awsCfg aws.Config, secretID string) (map[string]string, error) {
	return fetchSecretMap(ctx, awsCfg, secretID)
}

func fetchSecretMap(ctx context.Context, awsCfg aws.Config, secretID string) (map[string]string, error) {
	svc := secretsmanager.NewFromConfig(awsCfg)
	out, err := svc.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	})
	if err != nil {
		var notFound *types.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	m := map[string]string{}
	if out.SecretString != nil {
		if err := json.Unmarshal([]byte(*out.SecretString), &m); err != nil {
			return nil, fmt.Errorf("parse existing secret: %w", err)
		}
	}
	return m, nil
}

func storeOperatorSecretValue(ctx context.Context, awsCfg aws.Config, secretID, key, value, owner, environment string) error {
	values, err := fetchSecretMap(ctx, awsCfg, secretID)
	if err != nil {
		return err
	}
	values[key] = value
	payload, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return storeOperatorSecret(ctx, awsCfg, secretID, string(payload), owner, environment)
}

func mergeAgentKeys(existing map[string]string, anthropicKey, openaiKey, geminiKey string) map[string]string {
	m := make(map[string]string, len(existing))
	for k, v := range existing {
		m[k] = v
	}
	if anthropicKey != "" {
		m["CLAUDE_CODE_OAUTH_TOKEN"] = anthropicKey
	}
	if openaiKey != "" {
		m["OPENAI_API_KEY"] = openaiKey
	}
	if geminiKey != "" {
		m["GEMINI_API_KEY"] = geminiKey
	}
	return m
}

func buildAgentSecretJSON(anthropicKey, openaiKey, geminiKey string) (string, error) {
	m := map[string]string{}
	if anthropicKey != "" {
		m["CLAUDE_CODE_OAUTH_TOKEN"] = anthropicKey
	}
	if openaiKey != "" {
		m["OPENAI_API_KEY"] = openaiKey
	}
	if geminiKey != "" {
		m["GEMINI_API_KEY"] = geminiKey
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func storeAgentSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment string) error {
	if err := restoreSetupSecretIfPending(ctx, awsCfg, secretID); err != nil {
		return err
	}
	return storeDescribedSecret(ctx, awsCfg, secretID, value, owner, environment, "AI provider API keys for desktopctl owner: "+owner)
}

func setupSecretExists(ctx context.Context, awsCfg aws.Config, path string) (bool, error) {
	exists, _, err := setupSecretStatus(ctx, awsCfg, path)
	return exists, err
}

func setupSecretStatus(ctx context.Context, awsCfg aws.Config, path string) (bool, bool, error) {
	out, err := secretsmanager.NewFromConfig(awsCfg).DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(path)})
	if err == nil {
		return true, out.DeletedDate != nil, nil
	}
	var notFound *types.ResourceNotFoundException
	if errors.As(err, &notFound) {
		return false, false, nil
	}
	return false, false, fmt.Errorf("describe secret %q: %w", path, err)
}

func restoreSetupSecretIfPending(ctx context.Context, awsCfg aws.Config, path string) error {
	exists, pending, err := setupSecretStatus(ctx, awsCfg, path)
	if err != nil || !exists || !pending {
		return err
	}
	_, err = secretsmanager.NewFromConfig(awsCfg).RestoreSecret(ctx, &secretsmanager.RestoreSecretInput{SecretId: aws.String(path)})
	if err != nil {
		return fmt.Errorf("restore scheduled setup secret %q: %w", path, err)
	}
	return nil
}

func storeIntegrationSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment, integration string) error {
	return storeDescribedSecret(ctx, awsCfg, secretID, value, owner, environment, integration+" integration secret for desktopctl owner: "+owner)
}

func storeOperatorSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment string) error {
	svc := secretsmanager.NewFromConfig(awsCfg)

	_, err := svc.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(secretID),
	})
	if err == nil {
		if _, err := svc.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(secretID),
			SecretString: aws.String(value),
		}); err != nil {
			return err
		}
		_, err = svc.TagResource(ctx, &secretsmanager.TagResourceInput{
			SecretId: aws.String(secretID),
			Tags:     operatorSecretTags(owner, environment),
		})
		return err
	}

	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe secret: %w", err)
	}

	_, err = svc.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretID),
		SecretString: aws.String(value),
		Description:  aws.String("Operator-only secrets for desktopctl owner: " + owner),
		Tags:         operatorSecretTags(owner, environment),
	})
	return err
}

func operatorSecretTags(owner, environment string) []types.Tag {
	return []types.Tag{
		{Key: aws.String("desktopctl"), Value: aws.String("true")},
		{Key: aws.String("github-owner"), Value: aws.String(owner)},
		{Key: aws.String("environment"), Value: aws.String(environment)},
		{Key: aws.String("desktopctl-scope"), Value: aws.String("operator")},
		{Key: aws.String("ai-desktops-scope"), Value: aws.String("operator")},
	}
}

func storeDescribedSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment, description string) error {
	svc := secretsmanager.NewFromConfig(awsCfg)

	_, err := svc.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(secretID),
	})
	if err == nil {
		_, err = svc.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(secretID),
			SecretString: aws.String(value),
		})
		return err
	}

	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe secret: %w", err)
	}

	_, err = svc.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretID),
		SecretString: aws.String(value),
		Description:  aws.String(description),
		Tags: []types.Tag{
			{Key: aws.String("desktopctl"), Value: aws.String("true")},
			{Key: aws.String("github-owner"), Value: aws.String(owner)},
			{Key: aws.String("environment"), Value: aws.String(environment)},
		},
	})
	return err
}

func storeSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment string) error {
	if err := restoreSetupSecretIfPending(ctx, awsCfg, secretID); err != nil {
		return err
	}
	svc := secretsmanager.NewFromConfig(awsCfg)

	// Check if secret already exists
	_, err := svc.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{
		SecretId: aws.String(secretID),
	})
	if err == nil {
		// Secret exists — update it
		_, err = svc.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
			SecretId:     aws.String(secretID),
			SecretString: aws.String(value),
		})
		return err
	}

	// Only create if the secret doesn't exist; propagate other errors (permissions, throttling, etc.)
	var notFound *types.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("describe secret: %w", err)
	}

	// Create new secret
	_, err = svc.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretID),
		SecretString: aws.String(value),
		Description:  aws.String("GitHub credentials for desktopctl owner: " + owner),
		Tags: []types.Tag{
			{Key: aws.String("desktopctl"), Value: aws.String("true")},
			{Key: aws.String("github-owner"), Value: aws.String(owner)},
			{Key: aws.String("environment"), Value: aws.String(environment)},
		},
	})
	return err
}
