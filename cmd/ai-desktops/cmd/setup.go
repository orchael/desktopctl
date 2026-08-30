package cmd

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
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
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// githubAPIBase is the GitHub REST API base URL. Overridden in tests.
var githubAPIBase = "https://api.github.com"

var (
	setupConfigOut string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive configuration wizard (safe to re-run)",
	Long: `setup walks through configuring ~/.ai-desktops/config.yaml and provisioning
GitHub credentials (token + SSH key) in AWS Secrets Manager.

When run on an existing configuration all current values are used as defaults.
The GitHub token and SSH key are only rotated when explicitly requested.

The wizard will:
  1. Collect AWS and GitHub configuration interactively
  2. Optionally validate and rotate the GitHub token + SSH key
  3. Store updated credentials in AWS Secrets Manager
  4. Write ~/.ai-desktops/config.yaml (or --config-out path)`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().StringVar(&setupConfigOut, "config-out", "", "write config to this path instead of ~/.ai-desktops/config.yaml")
	rootCmd.AddCommand(setupCmd)
}

type setupAnswers struct {
	AWSRegion     string
	AWSProfile    string
	BackendBucket string
	Environment   string
	GitHubOwner   string
	GitHubToken   string
	GitUserName   string
	GitUserEmail  string
	AnthropicKey  string
	OpenAIKey     string
	GeminiKey     string
	SSHKeyPath    string
	SSHKeyName    string
}

func runSetup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	reader := bufio.NewReader(os.Stdin)

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	cfgPath := setupConfigOut
	if cfgPath == "" {
		cfgPath = filepath.Join(home, ".ai-desktops", "config.yaml")
	}

	_, statErr := os.Stat(cfgPath)
	isExisting := statErr == nil

	if isExisting {
		fmt.Println("Welcome to ai-desktops setup — updating existing configuration.")
		fmt.Printf("Config: %s\n", cfgPath)
		fmt.Println("Press Enter to keep the current value shown in brackets.")
	} else {
		fmt.Println("Welcome to ai-desktops setup.")
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

	fmt.Println()
	fmt.Println("── SSH (Desktop Access) ─────────────────────────────")

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
			defaultKeyName := "ai-desktops-" + existingOwner
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
						if err := importEC2KeyPair(ctx, ec2Cfg, a.SSHKeyName, strings.TrimSpace(string(pubBytes))); err != nil {
							fmt.Println("✗")
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

	secretPath := "/ai-desktops/" + a.GitHubOwner + "/github"

	// On an existing setup ask whether to rotate the token; on first run always collect it.
	updateToken := true
	if isExisting {
		fmt.Print("\nUpdate GitHub token and SSH key? [y/N]: ")
		answer, _ := reader.ReadString('\n')
		updateToken = strings.TrimSpace(strings.ToLower(answer)) == "y"
		if updateToken {
			fmt.Println()
			fmt.Println("WARNING: a new SSH key will be generated and registered with GitHub.")
			fmt.Println("The previous SSH key will remain registered and should be removed manually")
			fmt.Println("from https://github.com/settings/keys to avoid orphaned credentials.")
			fmt.Print("Continue? [y/N]: ")
			confirm, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(confirm)) != "y" {
				fmt.Println("Credential rotation cancelled.")
				updateToken = false
			}
		}
	}

	if updateToken {
		fmt.Println()
		fmt.Println("Before continuing, create a GitHub personal access token with these scopes:")
		fmt.Println("  • admin:public_key  (register SSH keys)")
		fmt.Println("  • repo              (clone, push, PRs)")
		fmt.Println("  • workflow          (GitHub Actions)")
		fmt.Println("  • read:user         (identity)")
		fmt.Println("  • read:org          (optional; gh CLI org features)")
		fmt.Println("  • read:packages     (optional; only needed for npm_github_scopes)")
		fmt.Println()
		fmt.Println("Create the token at: https://github.com/settings/tokens/new")
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

		// Generate SSH key pair
		fmt.Println()
		fmt.Printf("── Generating SSH key ───────────────────────────────\n")
		fmt.Printf("Generating Ed25519 SSH key pair for owner: %s\n", a.GitHubOwner)

		pub, priv, err := generateSSHKeyPair(a.GitHubOwner)
		if err != nil {
			return fmt.Errorf("generate SSH key pair: %w", err)
		}
		fmt.Printf("  Public key:  %s\n", strings.TrimSpace(pub))

		// Register SSH key with GitHub
		fmt.Printf("Registering SSH public key with GitHub account '%s'... ", a.GitHubOwner)
		if err := registerGitHubSSHKey(ctx, a.GitHubToken, "ai-desktops/"+a.GitHubOwner, pub); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("register SSH key: %w", err)
		}
		fmt.Println("✓")

		// Store/update secret in AWS Secrets Manager
		fmt.Println()
		fmt.Printf("── Storing secret ───────────────────────────────────\n")
		fmt.Printf("Storing secret at %s in %s... ", secretPath, a.AWSRegion)

		awsCfg, err := awscfg.LoadDefaultConfig(ctx,
			awscfg.WithRegion(a.AWSRegion),
			awscfg.WithSharedConfigProfile(a.AWSProfile),
		)
		if err != nil {
			fmt.Println("✗")
			return fmt.Errorf("load AWS config: %w", err)
		}

		secretValue, err := buildSecretJSON(a.GitHubToken, priv, pub)
		if err != nil {
			fmt.Println("✗")
			return fmt.Errorf("build secret JSON: %w", err)
		}

		if err := storeSecret(ctx, awsCfg, secretPath, secretValue, a.GitHubOwner, a.Environment); err != nil {
			fmt.Println("✗")
			return fmt.Errorf("store secret: %w", err)
		}
		fmt.Println("✓")
	} else {
		fmt.Println("  GitHub token unchanged.")
	}

	// Collect AI provider API keys.
	// Preserve any existing custom secret path; only derive the default on first run.
	agentSecretPath := "/ai-desktops/" + a.GitHubOwner + "/agents"
	if isExisting && cfg != nil && cfg.GitHub.AgentSecret != "" {
		agentSecretPath = cfg.GitHub.AgentSecret
	}
	updateAgentKeys := true
	if isExisting && cfg != nil && cfg.GitHub.AgentSecret != "" {
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
	newCfg.AWS.Region = a.AWSRegion
	newCfg.AWS.Profile = a.AWSProfile
	newCfg.Pulumi.BackendBucket = a.BackendBucket
	newCfg.Fleet.Environment = a.Environment
	newCfg.GitHub.Owner = a.GitHubOwner
	newCfg.GitHub.GitUserName = a.GitUserName
	newCfg.GitHub.GitUserEmail = a.GitUserEmail
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
	if !isExisting {
		fmt.Println("Next steps:")
		fmt.Println("  ai-desktops bootstrap       # create S3 Pulumi state bucket")
		fmt.Println("  ai-desktops init-foundation # deploy shared AWS infrastructure")
	}
	fmt.Printf("  ai-desktops create --github-owner %s --repo <owner/repo>\n", a.GitHubOwner)
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
// If a key pair with that name already exists it is left unchanged (treated as success).
func importEC2KeyPair(ctx context.Context, awsCfg aws.Config, name, publicKeyMaterial string) error {
	svc := ec2.NewFromConfig(awsCfg)
	_, err := svc.ImportKeyPair(ctx, &ec2.ImportKeyPairInput{
		KeyName:           aws.String(name),
		PublicKeyMaterial: []byte(publicKeyMaterial),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeKeyPair,
				Tags: []ec2types.Tag{
					{Key: aws.String("ai-desktops"), Value: aws.String("true")},
				},
			},
		},
	})
	if err != nil {
		// InvalidKeyPair.Duplicate means the key pair already exists — not an error.
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidKeyPair.Duplicate" {
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
	pubLine := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(sshPub)), "\n") + " ai-desktops/" + owner + "\n"

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
	return storeDescribedSecret(ctx, awsCfg, secretID, value, owner, environment, "AI provider API keys for ai-desktops owner: "+owner)
}

func storeIntegrationSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment, integration string) error {
	return storeDescribedSecret(ctx, awsCfg, secretID, value, owner, environment, integration+" integration secret for ai-desktops owner: "+owner)
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
		Description:  aws.String("Operator-only secrets for ai-desktops owner: " + owner),
		Tags:         operatorSecretTags(owner, environment),
	})
	return err
}

func operatorSecretTags(owner, environment string) []types.Tag {
	return []types.Tag{
		{Key: aws.String("ai-desktops"), Value: aws.String("true")},
		{Key: aws.String("github-owner"), Value: aws.String(owner)},
		{Key: aws.String("environment"), Value: aws.String(environment)},
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
			{Key: aws.String("ai-desktops"), Value: aws.String("true")},
			{Key: aws.String("github-owner"), Value: aws.String(owner)},
			{Key: aws.String("environment"), Value: aws.String(environment)},
		},
	})
	return err
}

func storeSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner, environment string) error {
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
		Description:  aws.String("GitHub credentials for ai-desktops owner: " + owner),
		Tags: []types.Tag{
			{Key: aws.String("ai-desktops"), Value: aws.String("true")},
			{Key: aws.String("github-owner"), Value: aws.String(owner)},
			{Key: aws.String("environment"), Value: aws.String(environment)},
		},
	})
	return err
}
