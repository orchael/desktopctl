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
	AWS       AWSConfig       `yaml:"aws"`
	Pulumi    PulumiConfig    `yaml:"pulumi"`
	Fleet     FleetConfig     `yaml:"fleet"`
	GitHub    GitHubConfig    `yaml:"github"`
	Desktop   DesktopConfig   `yaml:"desktop"`
	Agent     AgentConfig     `yaml:"agent"`
	Env       string          `yaml:"environment"`
}

type AWSConfig struct {
	Region  string `yaml:"region"`
	Profile string `yaml:"profile"`
}

type PulumiConfig struct {
	BackendBucket string `yaml:"backend_bucket"`
}

type FleetConfig struct {
	TableName string `yaml:"table_name"`
}

type GitHubConfig struct {
	PATSecret string `yaml:"pat_secret"`
}

type DesktopConfig struct {
	DefaultProfile string `yaml:"default_profile"`
	InstanceType   string `yaml:"instance_type"`
	OperatorCIDR   string `yaml:"operator_cidr"`
	SSHKeyPath     string `yaml:"ssh_key_path"`
}

type AgentConfig struct {
	BridgePort int `yaml:"bridge_port"`
}

// DNSZone returns the Route53 hosted zone name for the configured environment.
func (c *Config) DNSZone() (string, error) {
	switch c.Env {
	case EnvProd:
		return ZoneProd, nil
	case EnvDev, "":
		return ZoneDev, nil
	default:
		return "", fmt.Errorf("unknown environment %q: must be %q or %q", c.Env, EnvProd, EnvDev)
	}
}

// Defaults fills any zero-value fields with sensible defaults.
func (c *Config) Defaults() {
	if c.AWS.Region == "" {
		c.AWS.Region = "us-east-1"
	}
	if c.Env == "" {
		c.Env = EnvDev
	}
	if c.Fleet.TableName == "" {
		c.Fleet.TableName = DefaultFleetTable
	}
	if c.Desktop.InstanceType == "" {
		c.Desktop.InstanceType = DefaultInstanceType
	}
	if c.Desktop.OperatorCIDR == "" {
		c.Desktop.OperatorCIDR = "0.0.0.0/0"
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
	if _, err := c.DNSZone(); err != nil {
		return err
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
