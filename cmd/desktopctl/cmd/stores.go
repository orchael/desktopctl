package cmd

import (
	"context"
	"fmt"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
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

// openAMIStore returns a DynamoDB-backed AMI store, or an in-memory store when
// AMITableName is empty. It returns an error if AWS config cannot be loaded.
func openAMIStore(ctx context.Context) (store.AMIStore, error) {
	if cfg.Fleet.AMITableName == "" {
		return store.NewInMemoryAMIStore(), nil
	}
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return nil, fmt.Errorf("AWS config for AMI store: %w", err)
	}
	return store.NewDynamoAMIStoreFn(awsCfg, cfg.Fleet.AMITableName), nil
}
