package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/orchael/ai-desktops/internal/controlplane"
	"github.com/orchael/ai-desktops/internal/store"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	runtime := controlplane.LoadRuntimeConfig()
	if err := runtime.Validate(); err != nil {
		logger.Error("invalid runtime config", "error", err)
		os.Exit(1)
	}
	cfg, err := controlplane.LoadAppConfig(runtime)
	if err != nil {
		logger.Error("load app config", "error", err)
		os.Exit(1)
	}

	var fleetStore store.Store
	var awsCfg aws.Config
	var awsReady bool
	if runtime.MockAWS {
		awsReady = false
		fleetStore = seedMockStore()
	} else {
		awsCfg, err = controlplane.NewAWSLoader(cfg, runtime).Load(ctx)
		if err != nil {
			logger.Error("load AWS config", "error", err)
			os.Exit(1)
		}
		awsReady = true
		fleetStore = store.New(awsCfg, cfg.Fleet.TableName)
	}
	if fleetStore == nil {
		fleetStore = store.NewInMemoryStore()
	}

	service := controlplane.NewService(cfg, fleetStore, awsCfg, awsReady, runtime.MockAWS, runtime.RefreshTimeout, runtime.LifecycleTimeout)
	server := controlplane.NewServer(service, runtime.StaticDir, logger, runtime.APIToken)

	logger.Info("control plane listening", "addr", runtime.Addr)
	if err := http.ListenAndServe(runtime.Addr, server.Handler()); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func seedMockStore() store.Store {
	s := store.NewInMemoryStore()
	_ = s.Create(context.Background(), &store.Desktop{
		DesktopID:    "d-demo001",
		StackName:    "desktop-d-demo001",
		GitHubOwner:  "orchael",
		Region:       "us-east-2",
		State:        store.StateReady,
		InstanceID:   "i-00000000000000000",
		Hostname:     "d-demo001.desktops.orchael.dev",
		NoVNCURL:     "https://d-demo001.desktops.orchael.dev:8443/novnc/vnc.html",
		SSHTarget:    "ubuntu@d-demo001.desktops.orchael.dev",
		Readiness:    "mock ready",
		Repos:        []string{"orchael/ai-desktops"},
		InstanceType: "m7i.xlarge",
		MarketType:   store.MarketSpot,
		CreatedAt:    "2026-08-14T00:00:00Z",
		UpdatedAt:    "2026-08-14T00:00:00Z",
	})
	return s
}
