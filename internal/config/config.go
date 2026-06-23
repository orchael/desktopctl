package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	EnvProd = "prod"
	EnvDev  = "dev"
	EnvTest = "test"

	ZoneProd = "desktops.orchael.com"
	ZoneDev  = "desktops.orchael.dev"

	DefaultFleetTable   = "ai-desktops-fleet"
	DefaultAMITable     = "ai-desktops-ami"
	DefaultBridgePort   = 9445
	DefaultInstanceType = "t3.large"
	DefaultVolumeSize   = 100

	DefaultWireGuardPort      = 51820
	DefaultWireGuardSubnet    = "10.99.0.0/24"
	DefaultWireGuardInterface = "wg-aidesktops"
	// DefaultWireGuardServerIP is the VPN IP assigned to the desktop server (.1 in the subnet).
	DefaultWireGuardServerIP = "10.99.0.1"
)

// Config holds all operator configuration for ai-desktops.
type Config struct {
	AWS       AWSConfig       `yaml:"aws"`
	Pulumi    PulumiConfig    `yaml:"pulumi"`
	Fleet     FleetConfig     `yaml:"fleet"`
	GitHub    GitHubConfig    `yaml:"github"`
	Desktop   DesktopConfig   `yaml:"desktop"`
	Agent     AgentConfig     `yaml:"agent"`
	WireGuard WireGuardConfig `yaml:"wireguard,omitempty"`
}

// WireGuardConfig holds global WireGuard VPN settings and the operator peer list.
type WireGuardConfig struct {
	Enabled   bool            `yaml:"enabled"`
	Port      int             `yaml:"port,omitempty"`
	Subnet    string          `yaml:"subnet,omitempty"`
	Interface string          `yaml:"interface,omitempty"`
	Peers     []WireGuardPeer `yaml:"peers,omitempty"`
}

// WireGuardPeer represents a single VPN client (operator device) authorized to
// connect to desktops. The private key is never stored here; it is printed once
// when the peer is created and must be saved by the operator.
type WireGuardPeer struct {
	Name      string `yaml:"name"`
	PublicKey string `yaml:"public_key"`
	AllowedIP string `yaml:"allowed_ip"` // e.g. "10.99.0.2/32"
}

// Env returns the configured environment, falling back to dev.
func (c *Config) Environment() string {
	if c.Fleet.Environment != "" {
		return c.Fleet.Environment
	}
	return EnvDev
}

type AWSConfig struct {
	Region  string `yaml:"region"`
	Profile string `yaml:"profile"`
}

type PulumiConfig struct {
	BackendBucket string `yaml:"backend_bucket"`
	// InfraDir is the path to the repo root containing infra/pulumi/foundation
	// and infra/pulumi/desktop. Defaults to "." (current working directory).
	InfraDir string `yaml:"infra_dir"`
}

type FleetConfig struct {
	TableName    string `yaml:"table_name"`
	AMITableName string `yaml:"ami_table_name"`
	Environment  string `yaml:"environment"`
}

type GitHubConfig struct {
	Owner string `yaml:"owner"`
	// GitHubSecret is the AWS Secrets Manager secret path holding the JSON blob
	// with github_token, ssh_private_key, and ssh_public_key.
	// Set by `ai-desktops setup`. Default: /ai-desktops/<owner>/github.
	GitHubSecret string `yaml:"github_secret,omitempty"`
	// AgentSecret is the AWS Secrets Manager secret path holding the JSON blob
	// with AI provider API keys (CLAUDE_CODE_OAUTH_TOKEN, OPENAI_API_KEY, GEMINI_API_KEY).
	// Set by `ai-desktops setup`. Default: /ai-desktops/<owner>/agents.
	AgentSecret string `yaml:"agent_secret,omitempty"`
	// PATSecret is the legacy field name. Loaded if github_secret is absent.
	// Deprecated: use github_secret set by `ai-desktops setup`.
	PATSecret string `yaml:"pat_secret,omitempty"`
	// GitUserName is the git commit author name written to the desktop's global git config.
	GitUserName string `yaml:"git_user_name,omitempty"`
	// GitUserEmail is the git commit author email written to the desktop's global git config.
	GitUserEmail string `yaml:"git_user_email,omitempty"`
}

type DesktopConfig struct {
	DefaultProfile string `yaml:"default_profile"`
	InstanceType   string `yaml:"instance_type"`
	VolumeSize     int    `yaml:"volume_size,omitempty"`
	OperatorCIDR   string `yaml:"operator_cidr"`
	SSHKeyPath     string `yaml:"ssh_key_path"`
	SSHKeyName     string `yaml:"ssh_key_name"`
	// ActiveAMI specifies which AMI to use for each region. History is stored in DynamoDB.
	ActiveAMI map[string]string `yaml:"active_ami,omitempty"`
}

type AgentConfig struct {
	BridgePort int `yaml:"bridge_port"`
	// TrustHost disables SSH host key verification for SSH tunnel connections.
	// Leave false (the default) for normal operation so known_hosts is consulted.
	// Set to true for freshly provisioned desktops whose host key is not yet known.
	TrustHost bool `yaml:"trust_host"`
}

// DNSZone returns the Route53 hosted zone name for the configured environment.
// The test environment shares the dev zone so no separate hosted zone is needed.
func (c *Config) DNSZone() (string, error) {
	switch c.Environment() {
	case EnvProd:
		return ZoneProd, nil
	case EnvDev, EnvTest:
		return ZoneDev, nil
	default:
		return "", fmt.Errorf("unknown environment %q: must be one of %q, %q, %q", c.Environment(), EnvProd, EnvDev, EnvTest)
	}
}

// Defaults fills any zero-value fields with sensible defaults.
func (c *Config) Defaults() {
	if c.AWS.Region == "" {
		c.AWS.Region = "us-east-1"
	}
	if c.Fleet.Environment == "" {
		c.Fleet.Environment = EnvDev
	}
	if c.Fleet.TableName == "" {
		c.Fleet.TableName = DefaultFleetTable
	}
	if c.Fleet.AMITableName == "" {
		c.Fleet.AMITableName = DefaultAMITable
	}
	if c.Desktop.InstanceType == "" {
		c.Desktop.InstanceType = DefaultInstanceType
	}
	if c.Desktop.VolumeSize <= 0 {
		c.Desktop.VolumeSize = DefaultVolumeSize
	}
	if c.Pulumi.InfraDir == "" {
		c.Pulumi.InfraDir = "."
	}
	if c.Desktop.SSHKeyPath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			// Try id_ed25519 first (modern default), then fall back to id_rsa
			for _, keyFile := range []string{"id_ed25519", "id_rsa"} {
				keyPath := filepath.Join(home, ".ssh", keyFile)
				if _, err := os.Stat(keyPath); err == nil {
					c.Desktop.SSHKeyPath = keyPath
					break
				}
			}
		}
		// If UserHomeDir fails or neither key exists, leave SSHKeyPath empty
	}

	if c.Agent.BridgePort == 0 {
		c.Agent.BridgePort = DefaultBridgePort
	}
	if c.WireGuard.Enabled {
		if c.WireGuard.Port == 0 {
			c.WireGuard.Port = DefaultWireGuardPort
		}
		if c.WireGuard.Subnet == "" {
			c.WireGuard.Subnet = DefaultWireGuardSubnet
		}
		if c.WireGuard.Interface == "" {
			c.WireGuard.Interface = DefaultWireGuardInterface
		}
	}
	if c.GitHub.AgentSecret == "" && c.GitHub.Owner != "" {
		c.GitHub.AgentSecret = "/ai-desktops/" + c.GitHub.Owner + "/agents"
	}
	if c.GitHub.GitHubSecret == "" {
		if c.GitHub.PATSecret != "" {
			// Migrate legacy config: treat pat_secret as the secret path.
			// WARNING: the legacy pat_secret value stored a plain GitHub token in SSM
			// Parameter Store. Cloud-init expects a Secrets Manager JSON blob with
			// github_token, ssh_private_key, and ssh_public_key. If this path still
			// points at the old SSM plain-PAT, desktop provisioning will fail when it
			// tries to parse the secret. Run `ai-desktops setup` to re-provision
			// credentials in the correct format and update your config.
			fmt.Fprintf(os.Stderr, "WARNING: config uses deprecated pat_secret field (%s).\n"+
				"Run `ai-desktops setup` to migrate to github_secret format.\n", c.GitHub.PATSecret)
			c.GitHub.GitHubSecret = c.GitHub.PATSecret
		} else if c.GitHub.Owner != "" {
			c.GitHub.GitHubSecret = "/ai-desktops/" + c.GitHub.Owner + "/github"
		} else {
			c.GitHub.GitHubSecret = "/ai-desktops/github/pat"
		}
	}
}

// Validate checks that required configuration is present.
func (c *Config) Validate() error {
	if c.Pulumi.BackendBucket == "" {
		return errors.New("pulumi.backend_bucket must be set")
	}
	if c.WireGuard.Enabled {
		// When WireGuard is enabled, operator_cidr is unused; SG rules use the WireGuard subnet.
		// Allow it to be empty; init-foundation will use the WireGuard subnet instead.
	} else if c.Desktop.OperatorCIDR == "" {
		c.Desktop.OperatorCIDR = "0.0.0.0/0"
	}
	if _, err := c.DNSZone(); err != nil {
		return err
	}
	return nil
}

// Save writes the config to the given file path as YAML.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config to %s: %w", path, err)
	}
	return nil
}

// Load reads config from the given file path, applies defaults, and returns the
// resulting Config. Overrides supplied via flags should be applied after Load.
func Load(path string) (*Config, error) {
	c := &Config{}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(c); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	c.Defaults()
	return c, nil
}

// LoadOrDefault loads config from path; if path is empty it looks for the
// default config at ~/.ai-desktops/config.yaml. Returns an empty-default
// config when no file exists so the caller can still override via flags.
// DefaultPath returns the default config file path (~/.ai-desktops/config.yaml).
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ai-desktops", "config.yaml"), nil
}

func LoadOrDefault(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	c, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c = &Config{}
			c.Defaults()
			return c, nil
		}
		return nil, err
	}
	return c, nil
}
