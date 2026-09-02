package controlplane

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRuntimeConfigReadsOperatorAWSVariables(t *testing.T) {
	t.Setenv("OPERATOR_AWS_ACCESS_KEY_ID", "operator-access-key")
	t.Setenv("OPERATOR_AWS_SECRET_ACCESS_KEY", "operator-secret-key")
	t.Setenv("OPERATOR_ROLE_ARN", "arn:aws:iam::123456789012:role/operator")
	t.Setenv("OPERATOR_EXTERNAL_ID", "external-id")

	cfg := LoadRuntimeConfig()

	if cfg.OperatorAccessKeyID != "operator-access-key" {
		t.Errorf("OperatorAccessKeyID = %q", cfg.OperatorAccessKeyID)
	}
	if cfg.OperatorSecretAccessKey != "operator-secret-key" {
		t.Errorf("OperatorSecretAccessKey = %q", cfg.OperatorSecretAccessKey)
	}
	if cfg.OperatorRoleARN != "arn:aws:iam::123456789012:role/operator" {
		t.Errorf("OperatorRoleARN = %q", cfg.OperatorRoleARN)
	}
	if cfg.OperatorExternalID != "external-id" {
		t.Errorf("OperatorExternalID = %q", cfg.OperatorExternalID)
	}
}

func TestLoadAppConfigReadsDeploymentOverrides(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AI_DESKTOPS_ENVIRONMENT", "prod")
	t.Setenv("PULUMI_BACKEND_BUCKET", "prod-state")
	t.Setenv("AI_DESKTOPS_FLEET_TABLE", "prod-fleet")
	t.Setenv("AI_DESKTOPS_AMI_TABLE", "prod-ami")

	cfg, err := LoadAppConfig(RuntimeConfig{ConfigPath: filepath.Join(t.TempDir(), "missing.yaml")})
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}
	if cfg.AWS.Region != "us-west-2" {
		t.Errorf("AWS.Region = %q", cfg.AWS.Region)
	}
	if cfg.Fleet.Environment != "prod" {
		t.Errorf("Fleet.Environment = %q", cfg.Fleet.Environment)
	}
	if cfg.Pulumi.BackendBucket != "prod-state" {
		t.Errorf("Pulumi.BackendBucket = %q", cfg.Pulumi.BackendBucket)
	}
	if cfg.Fleet.TableName != "prod-fleet" {
		t.Errorf("Fleet.TableName = %q", cfg.Fleet.TableName)
	}
	if cfg.Fleet.AMITableName != "prod-ami" {
		t.Errorf("Fleet.AMITableName = %q", cfg.Fleet.AMITableName)
	}
}

func TestRuntimeConfigValidateRequiresOperatorAWSVariables(t *testing.T) {
	tests := []struct {
		name    string
		cfg     RuntimeConfig
		missing string
	}{
		{
			name:    "access key",
			cfg:     RuntimeConfig{OperatorSecretAccessKey: "secret", OperatorRoleARN: "role", APIToken: "token"},
			missing: "OPERATOR_AWS_ACCESS_KEY_ID",
		},
		{
			name:    "secret key",
			cfg:     RuntimeConfig{OperatorAccessKeyID: "key", OperatorRoleARN: "role", APIToken: "token"},
			missing: "OPERATOR_AWS_SECRET_ACCESS_KEY",
		},
		{
			name:    "role ARN",
			cfg:     RuntimeConfig{OperatorAccessKeyID: "key", OperatorSecretAccessKey: "secret", APIToken: "token"},
			missing: "OPERATOR_ROLE_ARN",
		},
		{
			name:    "external ID",
			cfg:     RuntimeConfig{OperatorAccessKeyID: "key", OperatorSecretAccessKey: "secret", OperatorRoleARN: "role", APIToken: "token"},
			missing: "OPERATOR_EXTERNAL_ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.missing) {
				t.Fatalf("Validate() error = %v, want missing %s", err, tt.missing)
			}
		})
	}
}
