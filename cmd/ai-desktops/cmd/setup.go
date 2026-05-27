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
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive first-time configuration wizard",
	Long: `setup walks through configuring ~/.ai-desktops/config.yaml and provisioning
GitHub credentials (token + SSH key) in AWS Secrets Manager.

The wizard will:
  1. Collect AWS and GitHub configuration interactively
  2. Validate the GitHub token
  3. Generate an Ed25519 SSH key pair
  4. Register the public key with GitHub
  5. Store all credentials in AWS Secrets Manager
  6. Write ~/.ai-desktops/config.yaml`,
	RunE: runSetup,
}

func init() {
	rootCmd.AddCommand(setupCmd)
}

type setupAnswers struct {
	AWSRegion     string
	AWSProfile    string
	BackendBucket string
	Environment   string
	GitHubOwner   string
	GitHubToken   string
}

func runSetup(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("Welcome to ai-desktops setup.")
	fmt.Println("This wizard will create ~/.ai-desktops/config.yaml and provision your GitHub credentials.")
	fmt.Println()

	// Determine config path and check if it exists
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	cfgPath := filepath.Join(home, ".ai-desktops", "config.yaml")

	if _, err := os.Stat(cfgPath); err == nil {
		fmt.Printf("config.yaml already exists at %s. Overwrite? [y/N]: ", cfgPath)
		answer, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(answer)) != "y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	a := &setupAnswers{}

	// Use existing config values as defaults where available
	existingRegion := "us-east-1"
	existingProfile := "default"
	existingEnv := "dev"
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
	}

	fmt.Println("── AWS ──────────────────────────────────────────────")
	a.AWSRegion = prompt(reader, fmt.Sprintf("AWS region [%s]", existingRegion), existingRegion)
	a.AWSProfile = prompt(reader, fmt.Sprintf("AWS profile [%s]", existingProfile), existingProfile)
	a.BackendBucket = prompt(reader, "Pulumi state S3 bucket", "")
	a.Environment = prompt(reader, fmt.Sprintf("Fleet environment (dev/prod) [%s]", existingEnv), existingEnv)

	fmt.Println()
	fmt.Println("── GitHub ───────────────────────────────────────────")
	a.GitHubOwner = prompt(reader, "GitHub owner (org or user)", "")
	if a.GitHubOwner == "" {
		return fmt.Errorf("github owner is required")
	}

	fmt.Println()
	fmt.Println("Before continuing, create a GitHub personal access token with these scopes:")
	fmt.Println("  • admin:public_key  (register SSH keys)")
	fmt.Println("  • repo              (clone, push, PRs)")
	fmt.Println("  • workflow          (GitHub Actions)")
	fmt.Println("  • security_events   (code scanning, secret scanning)")
	fmt.Println("  • read:user         (identity)")
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

	// Store secret in AWS Secrets Manager
	secretPath := "/ai-desktops/" + a.GitHubOwner + "/github"
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

	if err := storeSecret(ctx, awsCfg, secretPath, secretValue, a.GitHubOwner); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("store secret: %w", err)
	}
	fmt.Println("✓")

	// Write config
	fmt.Println()
	fmt.Printf("── Writing config ───────────────────────────────────\n")
	newCfg := &config.Config{
		AWS: config.AWSConfig{
			Region:  a.AWSRegion,
			Profile: a.AWSProfile,
		},
		Pulumi: config.PulumiConfig{
			BackendBucket: a.BackendBucket,
		},
		Fleet: config.FleetConfig{
			Environment: a.Environment,
		},
		GitHub: config.GitHubConfig{
			Owner:        a.GitHubOwner,
			GitHubSecret: secretPath,
		},
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
	fmt.Println("Setup complete. Next steps:")
	fmt.Println("  ai-desktops bootstrap       # create S3 Pulumi state bucket")
	fmt.Println("  ai-desktops init-foundation # deploy shared AWS infrastructure")
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

func validateGitHubToken(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/user/keys",
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

func storeSecret(ctx context.Context, awsCfg aws.Config, secretID, value, owner string) error {
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
		},
	})
	return err
}
