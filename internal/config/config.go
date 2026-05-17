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

	ZoneProd = "desktops.orchael.com"
	ZoneDev  = "desktops.orchael.dev"

	DefaultFleetTable  = "ai-desktops-fleet"
	DefaultBridgePort  = 9445
	DefaultInstanceType = "t3.large"
)

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
	TableName     string `yaml:"table_name"`
	AMITableName  string `yaml:"ami_table_name"`
	Environment   string `yaml:"environment"`
}

type GitHubConfig struct {
	Owner     string `yaml:"owner"`
	PATSecret string `yaml:"pat_secret"`
}

type DesktopConfig struct {
	DefaultProfile string            `yaml:"default_profile"`
	InstanceType   string            `yaml:"instance_type"`
	OperatorCIDR   string            `yaml:"operator_cidr"`
	SSHKeyPath     string            `yaml:"ssh_key_path"`
	SSHKeyName     string            `yaml:"ssh_key_name"`
	// ActiveAMI specifies which AMI to use for each region. History is stored in DynamoDB.
	ActiveAMI      map[string]string `yaml:"active_ami,omitempty"`
}

type AgentConfig struct {
	BridgePort int  `yaml:"bridge_port"`
	// TrustHost disables SSH host key verification for SSH tunnel connections.
	// Leave false (the default) for normal operation so known_hosts is consulted.
	// Set to true for freshly provisioned desktops whose host key is not yet known.
	TrustHost  bool `yaml:"trust_host"`
}

// DNSZone returns the Route53 hosted zone name for the configured environment.
func (c *Config) DNSZone() (string, error) {
	switch c.Environment() {
	case EnvProd:
		return ZoneProd, nil
	case EnvDev:
		return ZoneDev, nil
	default:
		return "", fmt.Errorf("unknown environment %q: must be %q or %q", c.Environment(), EnvProd, EnvDev)
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
	if c.Desktop.InstanceType == "" {
		c.Desktop.InstanceType = DefaultInstanceType
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
	if c.GitHub.PATSecret == "" {
		c.GitHub.PATSecret = "/ai-desktops/github/pat"
	}
}

// Validate checks that required configuration is present.
func (c *Config) Validate() error {
	if c.Pulumi.BackendBucket == "" {
		return errors.New("pulumi.backend_bucket must be set")
	}
	if c.Desktop.OperatorCIDR == "" {
		return errors.New("desktop.operator_cidr must be set (e.g. your public IP with /32)")
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
