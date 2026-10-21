package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/orchael/desktopctl/internal/config"
	"github.com/orchael/desktopctl/internal/dnszone"
	"github.com/spf13/cobra"
)

var setupValueFlags = []string{
	"environment", "dns-zone", "public-ami-owner", "backend-bucket", "github-owner", "github-token", "git-user-name",
	"git-user-email", "operator-cidr", "ssh-key-path", "ssh-key-name",
	"claude-oauth-token", "openai-api-key", "gemini-api-key",
}

func anySetupValueFlagChanged(cmd *cobra.Command) bool {
	for _, name := range setupValueFlags {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func setupInput(cmd *cobra.Command, flag, envName, fallback string) string {
	if value := cmd.Flag(flag); value != nil && value.Changed {
		return strings.TrimSpace(value.Value.String())
	}
	if value, ok := os.LookupEnv(envName); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func setupConfigPath() (string, error) {
	if setupConfigOut != "" {
		return setupConfigOut, nil
	}
	if cfgFile != "" {
		return cfgFile, nil
	}
	return config.DefaultPath()
}

func setupSavedConfig() (*config.Config, error) {
	path, err := setupConfigPath()
	if err != nil {
		return nil, err
	}
	return config.LoadOrDefault(path)
}

func nonInteractiveAnswers(cmd *cobra.Command, current *config.Config) (*setupAnswers, error) {
	if current == nil {
		current = &config.Config{}
	}
	a := &setupAnswers{}
	a.AWSRegion = setupInput(cmd, "region", "DESKTOPCTL_REGION", current.AWS.Region)
	a.AWSProfile = setupInput(cmd, "profile", "DESKTOPCTL_PROFILE", current.AWS.Profile)
	a.BackendBucket = setupInput(cmd, "backend-bucket", "DESKTOPCTL_BACKEND_BUCKET", current.Pulumi.BackendBucket)
	a.Environment = setupInput(cmd, "environment", "DESKTOPCTL_ENVIRONMENT", current.Environment())
	a.DNSZone = setupInput(cmd, "dns-zone", "DESKTOPCTL_DNS_ZONE", current.DNS.Zone)
	publicAMIOwner := current.Desktop.PublicAMIOwner
	if publicAMIOwner == "" {
		publicAMIOwner = defaultPublicAMIOwner
	}
	a.PublicAMIOwner = setupInput(cmd, "public-ami-owner", "DESKTOPCTL_PUBLIC_AMI_OWNER", publicAMIOwner)
	if err := validatePublicAMIOwner(a.PublicAMIOwner); err != nil {
		return nil, err
	}
	if a.DNSZone != "" {
		var err error
		a.DNSZone, err = dnszone.Normalize(a.DNSZone)
		if err != nil {
			return nil, fmt.Errorf("desktop DNS zone: %w", err)
		}
	}
	a.GitHubOwner = setupInput(cmd, "github-owner", "DESKTOPCTL_GITHUB_OWNER", current.GitHub.Owner)
	a.GitHubToken = setupInput(cmd, "github-token", "DESKTOPCTL_GITHUB_TOKEN", "")
	a.GitUserName = setupInput(cmd, "git-user-name", "DESKTOPCTL_GIT_USER_NAME", current.GitHub.GitUserName)
	a.GitUserEmail = setupInput(cmd, "git-user-email", "DESKTOPCTL_GIT_USER_EMAIL", current.GitHub.GitUserEmail)
	a.OperatorCIDR = setupInput(cmd, "operator-cidr", "DESKTOPCTL_OPERATOR_CIDR", current.Desktop.OperatorCIDR)
	a.SSHKeyPath = setupInput(cmd, "ssh-key-path", "DESKTOPCTL_SSH_KEY_PATH", current.Desktop.SSHKeyPath)
	defaultKeyName := ""
	if setupSameIdentity(current, a.AWSRegion, a.Environment, a.GitHubOwner) {
		defaultKeyName = current.Desktop.SSHKeyName
	}
	a.SSHKeyName = setupInput(cmd, "ssh-key-name", "DESKTOPCTL_SSH_KEY_NAME", defaultKeyName)
	a.AnthropicKey = setupInput(cmd, "claude-oauth-token", "CLAUDE_CODE_OAUTH_TOKEN", "")
	a.OpenAIKey = setupInput(cmd, "openai-api-key", "OPENAI_API_KEY", "")
	a.GeminiKey = setupInput(cmd, "gemini-api-key", "GEMINI_API_KEY", "")
	if a.AWSRegion == "" || a.AWSProfile == "" || a.BackendBucket == "" || a.GitHubOwner == "" {
		return nil, fmt.Errorf("non-interactive setup requires region, profile, backend bucket, and GitHub owner")
	}
	if a.Environment != config.EnvProd && a.Environment != config.EnvDev && a.Environment != config.EnvTest {
		return nil, fmt.Errorf("invalid environment %q", a.Environment)
	}
	if err := validateOperatorCIDR(a.OperatorCIDR); err != nil {
		return nil, fmt.Errorf("operator CIDR: %w", err)
	}
	if a.SSHKeyPath == "" {
		return nil, fmt.Errorf("non-interactive setup requires --ssh-key-path or DESKTOPCTL_SSH_KEY_PATH")
	}
	if a.SSHKeyName == "" {
		a.SSHKeyName = fmt.Sprintf("desktopctl-%s-%s-%s", a.Environment, a.AWSRegion, a.GitHubOwner)
	}
	return a, nil
}

func runNonInteractiveSetup(cmd *cobra.Command) error {
	ctx := context.Background()
	path, err := setupConfigPath()
	if err != nil {
		return err
	}
	saved, err := setupSavedConfig()
	if err != nil {
		return fmt.Errorf("load saved setup config: %w", err)
	}
	a, err := nonInteractiveAnswers(cmd, saved)
	if err != nil {
		return err
	}
	publicKey, err := os.ReadFile(a.SSHKeyPath + ".pub")
	if err != nil {
		return fmt.Errorf("read EC2 public key: %w", err)
	}
	if _, err := os.Stat(a.SSHKeyPath); err != nil {
		return fmt.Errorf("read local SSH private key: %w", err)
	}
	awsCfg, err := awscfg.LoadDefaultConfig(ctx, awscfg.WithRegion(a.AWSRegion), awscfg.WithSharedConfigProfile(a.AWSProfile))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	base := fmt.Sprintf("/desktopctl/%s/%s/%s", a.Environment, a.AWSRegion, a.GitHubOwner)
	githubPath := base + "/github"
	agentPath := base + "/agents"
	sameIdentity := setupSameIdentity(saved, a.AWSRegion, a.Environment, a.GitHubOwner)
	if sameIdentity && saved.GitHub.AgentSecret != "" {
		agentPath = saved.GitHub.AgentSecret
	}
	agentExists := false
	for _, name := range []string{githubPath, agentPath, base} {
		exists, _, err := setupSecretStatus(ctx, awsCfg, name)
		if err != nil {
			return err
		}
		if exists && !sameIdentity && !setupAllowExisting {
			return fmt.Errorf("AWS secret %q already exists; use the matching config or --allow-existing", name)
		}
		if name == agentPath {
			agentExists = exists
		}
	}
	for _, name := range []string{githubPath, agentPath} {
		if err := restoreSetupSecretIfPending(ctx, awsCfg, name); err != nil {
			return err
		}
	}
	github, err := fetchSecretMap(ctx, awsCfg, githubPath)
	if err != nil {
		return fmt.Errorf("fetch existing GitHub secret: %w", err)
	}
	token := a.GitHubToken
	if token == "" {
		token = github["github_token"]
	}
	if token == "" {
		return fmt.Errorf("GitHub token required: set DESKTOPCTL_GITHUB_TOKEN or --github-token")
	}
	if a.GitHubToken != "" {
		if err := validateGitHubToken(ctx, token); err != nil {
			return fmt.Errorf("validate GitHub token: %w", err)
		}
	}
	needGitHubKey := setupRotateGitHubSSHKey || github["ssh_private_key"] == "" || github["ssh_public_key"] == ""
	if needGitHubKey {
		pub, priv, err := generateSSHKeyPair(a.GitHubOwner)
		if err != nil {
			return fmt.Errorf("generate GitHub SSH key: %w", err)
		}
		if err := registerGitHubSSHKey(ctx, token, "desktopctl/"+a.GitHubOwner, pub); err != nil {
			return fmt.Errorf("register GitHub SSH key: %w", err)
		}
		github["ssh_private_key"], github["ssh_public_key"] = priv, strings.TrimSpace(pub)
	}
	github["github_token"] = token
	if err := importEC2KeyPair(ctx, awsCfg, a.SSHKeyName, strings.TrimSpace(string(publicKey)), !sameIdentity && !setupAllowExisting); err != nil {
		return fmt.Errorf("register EC2 key pair: %w", err)
	}
	if a.GitHubToken != "" || needGitHubKey {
		payload, err := json.Marshal(github)
		if err != nil {
			return err
		}
		if err := storeSecret(ctx, awsCfg, githubPath, string(payload), a.GitHubOwner, a.Environment); err != nil {
			return fmt.Errorf("store GitHub secret: %w", err)
		}
	}
	newAgentValues := a.AnthropicKey != "" || a.OpenAIKey != "" || a.GeminiKey != ""
	if newAgentValues {
		existing, err := fetchAgentSecret(ctx, awsCfg, agentPath)
		if err != nil {
			return fmt.Errorf("fetch existing agent secret: %w", err)
		}
		merged := mergeAgentKeys(existing, a.AnthropicKey, a.OpenAIKey, a.GeminiKey)
		payload, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		if err := storeAgentSecret(ctx, awsCfg, agentPath, string(payload), a.GitHubOwner, a.Environment); err != nil {
			return fmt.Errorf("store agent secret: %w", err)
		}
	}
	updated := *saved
	updated.GitHub.Owner = a.GitHubOwner
	updated.Fleet.Environment = a.Environment
	updated.SetRegion(a.AWSRegion)
	updated.AWS.Profile = a.AWSProfile
	updated.Pulumi.BackendBucket = a.BackendBucket
	updated.DNS.Zone = a.DNSZone
	updated.GitHub.GitHubSecret = githubPath
	if agentExists || newAgentValues {
		updated.GitHub.AgentSecret = agentPath
	} else {
		updated.GitHub.AgentSecret = ""
	}
	updated.GitHub.GitUserName = a.GitUserName
	updated.GitHub.GitUserEmail = a.GitUserEmail
	updated.Desktop.OperatorCIDR = a.OperatorCIDR
	updated.Desktop.PublicAMIOwner = a.PublicAMIOwner
	updated.Desktop.SSHKeyPath = a.SSHKeyPath
	updated.Desktop.SSHKeyName = a.SSHKeyName
	if !sameIdentity {
		updated.Fleet.TableName = ""
		updated.Fleet.AMITableName = ""
		updated.Pool.TableName = ""
		updated.Operator.Secret = ""
	}
	updated.Defaults()
	if err := ensureSetupDNS(ctx, a, &updated); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := updated.Save(path); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Setup complete: %s (profile %s, region %s, environment %s)\n", path, a.AWSProfile, a.AWSRegion, a.Environment)
	return nil
}
