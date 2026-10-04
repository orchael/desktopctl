package controlplane

import (
	"errors"
	"os"
	"strconv"
	"time"

	appconfig "github.com/orchael/desktopctl/internal/config"
)

const (
	defaultAddr             = ":8080"
	defaultRefreshTimeout   = 15 * time.Second
	defaultLifecycleTimeout = 12 * time.Minute
)

// RuntimeConfig contains control-plane-only settings loaded from environment.
type RuntimeConfig struct {
	Addr                    string
	ConfigPath              string
	StaticDir               string
	MockAWS                 bool
	OperatorAccessKeyID     string
	OperatorSecretAccessKey string
	OperatorRoleARN         string
	OperatorExternalID      string
	APIToken                string
	RefreshTimeout          time.Duration
	LifecycleTimeout        time.Duration
}

// LoadRuntimeConfig reads runtime settings from environment variables.
func LoadRuntimeConfig() RuntimeConfig {
	timeout := defaultRefreshTimeout
	if raw := os.Getenv("CONTROL_PLANE_REFRESH_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	lifecycleTimeout := defaultLifecycleTimeout
	if raw := os.Getenv("CONTROL_PLANE_LIFECYCLE_TIMEOUT"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			lifecycleTimeout = parsed
		}
	}
	mockAWS := false
	if raw := os.Getenv("CONTROL_PLANE_MOCK_AWS"); raw != "" {
		mockAWS, _ = strconv.ParseBool(raw)
	}
	addr := os.Getenv("CONTROL_PLANE_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	return RuntimeConfig{
		Addr:                    addr,
		ConfigPath:              firstNonEmpty(os.Getenv("AI_DESKTOPS_CONFIG"), os.Getenv("CONFIG_PATH")),
		StaticDir:               os.Getenv("CONTROL_PLANE_STATIC_DIR"),
		MockAWS:                 mockAWS,
		OperatorAccessKeyID:     os.Getenv("OPERATOR_AWS_ACCESS_KEY_ID"),
		OperatorSecretAccessKey: os.Getenv("OPERATOR_AWS_SECRET_ACCESS_KEY"),
		OperatorRoleARN:         os.Getenv("OPERATOR_ROLE_ARN"),
		OperatorExternalID:      os.Getenv("OPERATOR_EXTERNAL_ID"),
		APIToken:                os.Getenv("CONTROL_PLANE_API_TOKEN"),
		RefreshTimeout:          timeout,
		LifecycleTimeout:        lifecycleTimeout,
	}
}

// LoadAppConfig loads the shared ai-desktops operator config.
func LoadAppConfig(runtime RuntimeConfig) (*appconfig.Config, error) {
	cfg, err := appconfig.LoadOrDefault(runtime.ConfigPath)
	if err != nil {
		return nil, err
	}
	if env := os.Getenv("AI_DESKTOPS_ENVIRONMENT"); env != "" {
		cfg.Fleet.Environment = env
	}
	if region := os.Getenv("AWS_REGION"); region != "" {
		cfg.AWS.Region = region
	}
	if backendBucket := os.Getenv("PULUMI_BACKEND_BUCKET"); backendBucket != "" {
		cfg.Pulumi.BackendBucket = backendBucket
	}
	if fleetTable := os.Getenv("AI_DESKTOPS_FLEET_TABLE"); fleetTable != "" {
		cfg.Fleet.TableName = fleetTable
	}
	if amiTable := os.Getenv("AI_DESKTOPS_AMI_TABLE"); amiTable != "" {
		cfg.Fleet.AMITableName = amiTable
	}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c RuntimeConfig) Validate() error {
	if c.MockAWS {
		return nil
	}
	if c.OperatorAccessKeyID == "" {
		return errors.New("OPERATOR_AWS_ACCESS_KEY_ID is required unless CONTROL_PLANE_MOCK_AWS=true")
	}
	if c.OperatorSecretAccessKey == "" {
		return errors.New("OPERATOR_AWS_SECRET_ACCESS_KEY is required unless CONTROL_PLANE_MOCK_AWS=true")
	}
	if c.OperatorRoleARN == "" {
		return errors.New("OPERATOR_ROLE_ARN is required unless CONTROL_PLANE_MOCK_AWS=true")
	}
	if c.OperatorExternalID == "" {
		return errors.New("OPERATOR_EXTERNAL_ID is required unless CONTROL_PLANE_MOCK_AWS=true")
	}
	if c.APIToken == "" {
		return errors.New("CONTROL_PLANE_API_TOKEN is required unless CONTROL_PLANE_MOCK_AWS=true")
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
