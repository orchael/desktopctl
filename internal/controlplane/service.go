package controlplane

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/orchael/ai-desktops/internal/awsx"
	appconfig "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/store"
)

// Service owns control-plane operations over the fleet.
type Service struct {
	cfg            *appconfig.Config
	store          store.Store
	awsCfg         aws.Config
	awsReady       bool
	mockAWS        bool
	refreshTimeout time.Duration
}

func NewService(cfg *appconfig.Config, s store.Store, awsCfg aws.Config, awsReady bool, mockAWS bool, refreshTimeout time.Duration) *Service {
	return &Service{cfg: cfg, store: s, awsCfg: awsCfg, awsReady: awsReady, mockAWS: mockAWS, refreshTimeout: refreshTimeout}
}

type Readiness struct {
	OK          bool   `json:"ok"`
	Environment string `json:"environment"`
	Region      string `json:"region"`
	FleetTable  string `json:"fleet_table"`
	Identity    string `json:"identity,omitempty"`
	Error       string `json:"error,omitempty"`
}

type DesktopSummary struct {
	*store.Desktop
	LiveState string `json:"live_state,omitempty"`
}

type OperationResult struct {
	DesktopID string               `json:"desktop_id"`
	Action    string               `json:"action"`
	State     store.LifecycleState `json:"state"`
	Message   string               `json:"message"`
	Desktop   *store.Desktop       `json:"desktop,omitempty"`
}

func (s *Service) Ready(ctx context.Context) Readiness {
	out := Readiness{
		Environment: s.cfg.Environment(),
		Region:      s.cfg.AWS.Region,
		FleetTable:  s.cfg.Fleet.TableName,
	}
	if _, err := s.store.List(ctx); err != nil {
		out.Error = fmt.Sprintf("list fleet table: %v", err)
		return out
	}
	if s.mockAWS {
		out.OK = true
		out.Identity = "mock"
		return out
	}
	if !s.awsReady {
		out.Error = "AWS config unavailable"
		return out
	}
	identity, err := awsx.GetCallerIdentity(ctx, s.awsCfg)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.OK = true
	out.Identity = identity
	return out
}

func (s *Service) ListDesktops(ctx context.Context, includeTerminated bool) ([]*store.Desktop, error) {
	desktops, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]*store.Desktop, 0, len(desktops))
	for _, d := range desktops {
		if includeTerminated || d.State != store.StateTerminated {
			filtered = append(filtered, d)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt > filtered[j].CreatedAt
	})
	return filtered, nil
}

func (s *Service) GetDesktop(ctx context.Context, id string) (*DesktopSummary, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	summary := &DesktopSummary{Desktop: d}
	live, err := s.liveInstanceState(ctx, d)
	if err == nil {
		summary.LiveState = live
	}
	return summary, nil
}

func (s *Service) RefreshDesktop(ctx context.Context, id string) (*DesktopSummary, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	live, err := s.liveInstanceState(ctx, d)
	if err != nil {
		return nil, err
	}
	if live == "stopped" || live == "stopping" {
		d.State = store.StateStopped
		if d.StopReason == "" {
			d.StopReason = store.StopReasonAWSStopped
		}
		if d.StoppedAt == "" {
			d.StoppedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if err := s.store.Update(ctx, d); err != nil {
			return nil, err
		}
	}
	if live == "running" && d.State == store.StateStopped {
		d.State = store.StateReady
		d.StopReason = ""
		d.StoppedAt = ""
		if d.Readiness == "" {
			d.Readiness = "running"
		}
		if err := s.store.Update(ctx, d); err != nil {
			return nil, err
		}
	}
	return &DesktopSummary{Desktop: d, LiveState: live}, nil
}

func (s *Service) StopDesktop(ctx context.Context, id string) (*OperationResult, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.InstanceID == "" {
		return nil, fmt.Errorf("desktop %q has no instance ID", id)
	}
	if err := s.requireAWS(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.refreshTimeout)
	defer cancel()
	if d.NestedVirt {
		err = awsx.HaltInstance(callCtx, s.awsCfg, d.InstanceID)
	} else {
		err = awsx.StopInstance(callCtx, s.awsCfg, d.InstanceID)
	}
	if err != nil {
		return nil, err
	}
	mgr := desktop.NewManager(s.store)
	if err := mgr.MarkStoppedWithReason(ctx, id, store.StopReasonUserRequest); err != nil {
		return nil, err
	}
	updated, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &OperationResult{
		DesktopID: id,
		Action:    "stop",
		State:     updated.State,
		Message:   "desktop stopped",
		Desktop:   updated,
	}, nil
}

func (s *Service) StartDesktop(ctx context.Context, id string) (*OperationResult, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.InstanceID == "" {
		return nil, fmt.Errorf("desktop %q has no instance ID", id)
	}
	if err := s.requireAWS(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.refreshTimeout)
	defer cancel()
	if err := awsx.StartInstance(callCtx, s.awsCfg, d.InstanceID); err != nil {
		return nil, err
	}
	mgr := desktop.NewManager(s.store)
	if err := mgr.MarkRunning(ctx, id, "started"); err != nil {
		return nil, err
	}
	updated, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &OperationResult{
		DesktopID: id,
		Action:    "start",
		State:     updated.State,
		Message:   "desktop started; DNS refresh is not yet automated in the control plane",
		Desktop:   updated,
	}, nil
}

func (s *Service) liveInstanceState(ctx context.Context, d *store.Desktop) (string, error) {
	if d.InstanceID == "" {
		return "", nil
	}
	if s.mockAWS {
		return "mock", nil
	}
	if err := s.requireAWS(); err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.refreshTimeout)
	defer cancel()
	return awsx.InstanceState(callCtx, s.awsCfg, d.InstanceID)
}

func (s *Service) requireAWS() error {
	if !s.awsReady {
		return errors.New("AWS config unavailable")
	}
	return nil
}
