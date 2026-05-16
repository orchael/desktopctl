package cmd

import (
	"context"
	"fmt"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/store"
)

// openStore returns a DynamoDB-backed store when AWS config is available,
// or an in-memory store as a fallback (local dev / no credentials).
func openStore(ctx context.Context) (store.Store, error) {
	if cfg.Fleet.TableName == "" {
		return store.NewInMemoryStore(), nil
	}
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return nil, fmt.Errorf("AWS config for store: %w", err)
	}
	return store.New(awsCfg, cfg.Fleet.TableName), nil
}

// openAMIStore returns a DynamoDB-backed AMI store when AWS config is available,
// or an in-memory store as a fallback (local dev / no credentials).
func openAMIStore(ctx context.Context) (store.AMIStore, error) {
	if cfg.Fleet.TableName == "" {
		return store.NewInMemoryAMIStore(), nil
	}
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return nil, fmt.Errorf("AWS config for AMI store: %w", err)
	}
	return store.NewDynamoAMIStoreFn(awsCfg, cfg.Fleet.TableName), nil
}
