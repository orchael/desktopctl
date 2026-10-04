package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/orchael/desktopctl/internal/awsx"
)

// requireTools checks that every named binary exists on PATH and returns a
// combined error listing all missing tools if any are absent.
func requireTools(tools ...string) error {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required tool(s) not found in PATH: %s", strings.Join(missing, ", "))
	}
	return nil
}

// requireBackend verifies that the configured Pulumi S3 backend bucket exists
// in AWS. Commands that drive Pulumi (init-foundation, create, terminate) call
// this before doing any work so that missing-bootstrap produces a clear,
// actionable error instead of a cryptic Pulumi failure.
func requireBackend(ctx context.Context) error {
	if cfg.Pulumi.BackendBucket == "" {
		return fmt.Errorf(`pulumi.backend_bucket is not set in your config.

Add it to ~/.ai-desktops/config.yaml:

  pulumi:
    backend_bucket: <globally-unique-bucket-name>

Then run:

  ai-desktops bootstrap

to create the S3 bucket with versioning, encryption, and public-access block.`)
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config: %w", err)
	}

	exists, err := awsx.BucketExists(ctx, awsCfg, cfg.Pulumi.BackendBucket)
	if err != nil {
		return fmt.Errorf("check Pulumi backend bucket: %w", err)
	}
	if !exists {
		return fmt.Errorf(`Pulumi state backend bucket %q does not exist.

Run the following to create it:

  ai-desktops bootstrap

bootstrap creates the S3 bucket with versioning, encryption, and
public-access block enabled. It is safe to re-run.`, cfg.Pulumi.BackendBucket)
	}
	return nil
}
