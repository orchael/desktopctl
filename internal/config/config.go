package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	EnvProd = "prod"
	EnvDev  = "dev"
	EnvTest = "test"

	ZoneProd = "desktops.orchael.com"
	ZoneDev  = "desktops.orchael.dev"

	DefaultFleetTablePrefix   = "ai-desktops-fleet"
	DefaultAMITablePrefix     = "ai-desktops-ami"
	DefaultBridgePort         = 9445
	DefaultInstanceType       = "t3.large"
	DefaultMobileInstanceType = "m8i.xlarge"
	DefaultVolumeSize         = 100

	// DefaultMobileAVD is the AVD created by --mobile when no --avd flags are given.
	DefaultMobileAVDName   = "flutter_dev"
	DefaultMobileAVDImage  = "system-images;android-35;google_apis;x86_64"
	DefaultMobileAVDDevice = "pixel_6"
)

// NestedVirtInstanceFamilies lists the EC2 instance families that support the
// CpuOptions NestedVirtualization=enabled parameter (Nitro x86_64).
// AWS added first-class nested virtualization support in February 2026.
// 5th-gen Intel (c8i, m8i, r8i) was supported at launch; 4th-gen Intel
// (c7i, m7i, r7i, i7i) was added in June 2026.
// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
var NestedVirtInstanceFamilies = []string{
	"c8i",
	"m8i",
	"r8i",
	"c8id",
	"m8id",
	"r8id",
	"x8i",
	"c7i",
	"m7i",
	"r7i",
	"i7i",
}

// Config holds all operator configuration for ai-desktops.
type Config struct {
	AWS     AWSConfig     `yaml:"aws"`
	Pulumi  PulumiConfig  `yaml:"pulumi"`
	Fleet   FleetConfig   `yaml:"fleet"`
	GitHub  GitHubConfig  `yaml:"github"`
	Desktop DesktopConfig `yaml:"desktop"`
	Agent   AgentConfig   `yaml:"agent"`
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

// AVDConfig describes a single Android Virtual Device to create at desktop boot.
type AVDConfig struct {
	// Name is the AVD identifier passed to avdmanager -n (e.g. "flutter_dev").
	Name string `yaml:"name"`
	// Image is the system image package key (e.g. "system-images;android-35;google_apis;x86_64").
	Image string `yaml:"image"`
	// Device is the hardware profile passed to avdmanager --device (e.g. "pixel_6").
	// Optional; omit to use avdmanager's default.
	Device string `yaml:"device,omitempty"`
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
	// AVDs lists Android Virtual Devices to create at desktop boot.
	// Empty means no AVDs are created. Passed as --avd flags on the create command
	// or set here as the default set for all desktops.
	AVDs []AVDConfig `yaml:"avds,omitempty"`
	// NestedVirtualization enables KVM hardware acceleration on the desktop instance
	// by setting CpuOptions.NestedVirtualization=enabled on the EC2 instance.
	// Requires a supported Nitro x86_64 instance type (c7i, c8i, m7i, m8i, r7i, r8i).
	// See NestedVirtInstanceFamilies for the full list.
	NestedVirtualization bool `yaml:"nested_virtualization,omitempty"`
}

type AgentConfig struct {
	BridgePort int `yaml:"bridge_port"`
	// TrustHost disables SSH host key verification for SSH tunnel connections.
	// Leave false (the default) for normal operation so known_hosts is consulted.
	// Set to true for freshly provisioned desktops whose host key is not yet known.
	TrustHost bool `yaml:"trust_host"`
}

// SupportsNestedVirt reports whether instanceType belongs to a family that
// supports nested virtualization via CpuOptions.NestedVirtualization=enabled.
func SupportsNestedVirt(instanceType string) bool {
	for _, family := range NestedVirtInstanceFamilies {
		if strings.HasPrefix(instanceType, family+".") {
			return true
		}
	}
	return false
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
		c.Fleet.TableName = DefaultFleetTablePrefix + "-" + c.Fleet.Environment
	}
	if c.Fleet.AMITableName == "" {
		c.Fleet.AMITableName = DefaultAMITablePrefix + "-" + c.Fleet.Environment
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
	if c.Desktop.OperatorCIDR == "" {
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
func LoadOrDefault(path string) (*Config, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".ai-desktops", "config.yaml")
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
