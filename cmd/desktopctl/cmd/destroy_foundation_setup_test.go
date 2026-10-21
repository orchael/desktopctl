package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
	"github.com/orchael/desktopctl/internal/config"
)

func testCleanupConfig() *config.Config {
	return &config.Config{
		AWS:     config.AWSConfig{Region: "ca-central-1"},
		Fleet:   config.FleetConfig{Environment: "prod"},
		GitHub:  config.GitHubConfig{Owner: "orchael", GitHubSecret: "/desktopctl/prod/ca-central-1/orchael/github", AgentSecret: "/desktopctl/prod/ca-central-1/orchael/agents"},
		Desktop: config.DesktopConfig{SSHKeyName: "desktopctl-prod-ca-central-1-orchael"},
	}
}

func TestSetupCleanupTargets(t *testing.T) {
	c := testCleanupConfig()
	targets, err := setupCleanupTargets(c, "prod")
	if err != nil || targets.keyPair != c.Desktop.SSHKeyName || len(targets.secrets) != 2 {
		t.Fatalf("targets = %+v, err = %v", targets, err)
	}
	for _, tc := range []struct {
		name string
		edit func(*config.Config)
		env  string
	}{
		{"wrong environment", func(*config.Config) {}, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *testCleanupConfig()
			tc.edit(&copy)
			if _, err := setupCleanupTargets(&copy, tc.env); err == nil {
				t.Fatal("expected cleanup target rejection")
			}
		})
	}
	c.Desktop.SSHKeyName = "shared-key"
	c.GitHub.AgentSecret = "/desktopctl/prod/us-east-1/orchael/agents"
	targets, err = setupCleanupTargets(c, "prod")
	if err != nil {
		t.Fatal(err)
	}
	if targets.keyPair != "" || len(targets.secrets) != 1 || targets.secrets[0] != c.GitHub.GitHubSecret || len(targets.skipped) != 2 {
		t.Fatalf("custom resources should be skipped while standard secret remains: %+v", targets)
	}
}

type fakeKeyDeleter struct {
	names []string
	err   error
}

func (f *fakeKeyDeleter) DeleteKeyPair(_ context.Context, in *ec2.DeleteKeyPairInput, _ ...func(*ec2.Options)) (*ec2.DeleteKeyPairOutput, error) {
	f.names = append(f.names, *in.KeyName)
	return &ec2.DeleteKeyPairOutput{}, f.err
}

type fakeSecretDeleter struct {
	described []string
	deleted   []string
	missing   map[string]bool
	pending   map[string]bool
}

func (f *fakeSecretDeleter) DescribeSecret(_ context.Context, in *secretsmanager.DescribeSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	name := *in.SecretId
	f.described = append(f.described, name)
	if f.missing[name] {
		return nil, &smithy.GenericAPIError{Code: "ResourceNotFoundException"}
	}
	if f.pending[name] {
		now := time.Now()
		return &secretsmanager.DescribeSecretOutput{DeletedDate: &now}, nil
	}
	return &secretsmanager.DescribeSecretOutput{}, nil
}

func (f *fakeSecretDeleter) DeleteSecret(_ context.Context, in *secretsmanager.DeleteSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error) {
	if *in.RecoveryWindowInDays != 7 {
		return nil, errors.New("unexpected recovery window")
	}
	f.deleted = append(f.deleted, *in.SecretId)
	return &secretsmanager.DeleteSecretOutput{}, nil
}

func TestCleanupSetupResourcesSkipsAbsentAndPending(t *testing.T) {
	targets, err := setupCleanupTargets(testCleanupConfig(), "prod")
	if err != nil {
		t.Fatal(err)
	}
	keys := &fakeKeyDeleter{err: &smithy.GenericAPIError{Code: "InvalidKeyPair.NotFound"}}
	secrets := &fakeSecretDeleter{missing: map[string]bool{targets.secrets[0]: true}, pending: map[string]bool{targets.secrets[1]: true}}
	if err := cleanupSetupResources(context.Background(), keys, secrets, targets); err != nil {
		t.Fatal(err)
	}
	if len(keys.names) != 1 || len(secrets.described) != 2 || len(secrets.deleted) != 0 {
		t.Fatalf("keys=%v described=%v deleted=%v", keys.names, secrets.described, secrets.deleted)
	}
	secrets = &fakeSecretDeleter{}
	if err := cleanupSetupResources(context.Background(), &fakeKeyDeleter{}, secrets, targets); err != nil {
		t.Fatal(err)
	}
	if len(secrets.deleted) != 2 {
		t.Fatalf("deleted %v", secrets.deleted)
	}
}
