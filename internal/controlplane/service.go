package controlplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/orchael/ai-desktops/internal/awsx"
	appconfig "github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
)

// PulumiRunner is the interface for Pulumi lifecycle operations used by the
// control-plane service. *pulumi.Runner satisfies this interface.
type PulumiRunner interface {
	Up(ctx context.Context, ref *pulumi.StackRef, cfg pulumi.StackConfig, progress io.Writer) (map[string]string, error)
	Destroy(ctx context.Context, ref *pulumi.StackRef, progress io.Writer) error
	Outputs(ctx context.Context, ref *pulumi.StackRef) (map[string]string, error)
}

// CreateDesktopRequest is the API request body for POST /api/desktops.
type CreateDesktopRequest struct {
	Owner          string   `json:"owner"`
	Name           string   `json:"name,omitempty"`
	Repos          []string `json:"repos,omitempty"`
	AMIID          string   `json:"ami_id,omitempty"`
	InstanceType   string   `json:"instance_type,omitempty"`
	VolumeSize     int      `json:"volume_size,omitempty"`
	MarketType     string   `json:"market_type,omitempty"`
	SpotMaxPrice   string   `json:"spot_max_price,omitempty"`
	Environment    string   `json:"environment,omitempty"`
	UserDataBase64 string   `json:"user_data_base64,omitempty"`
}

// Service owns control-plane operations over the fleet.
type Service struct {
	cfg              *appconfig.Config
	store            store.Store
	awsCfg           aws.Config
	awsReady         bool
	mockAWS          bool
	pulumiRunner     PulumiRunner
	refreshTimeout   time.Duration
	lifecycleTimeout time.Duration
}

// NewService creates a Service. pulumiRunner may be nil; TerminateDesktop and
// CreateDesktop return an error when it is nil.
func NewService(cfg *appconfig.Config, s store.Store, pulumiRunner PulumiRunner, awsReady bool, mockAWS bool, refreshTimeout time.Duration, lifecycleTimeout time.Duration) *Service {
	return &Service{
		cfg:              cfg,
		store:            s,
		pulumiRunner:     pulumiRunner,
		awsReady:         awsReady,
		mockAWS:          mockAWS,
		refreshTimeout:   refreshTimeout,
		lifecycleTimeout: lifecycleTimeout,
	}
}

// WithAWSConfig attaches an AWS config to the service for EC2 operations.
func (s *Service) WithAWSConfig(awsCfg aws.Config) *Service {
	s.awsCfg = awsCfg
	return s
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
	if _, err := s.store.Get(ctx, "__readyz_probe__"); err != nil && !errors.Is(err, store.ErrNotFound) {
		out.Error = fmt.Sprintf("read fleet table: %v", err)
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

func (s *Service) ListDesktops(ctx context.Context, organizationID string, includeTerminated bool) ([]*store.Desktop, error) {
	desktops, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]*store.Desktop, 0, len(desktops))
	for _, d := range desktops {
		if d.OrganizationID == organizationID && (includeTerminated || d.State != store.StateTerminated) {
			filtered = append(filtered, d)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt > filtered[j].CreatedAt
	})
	return filtered, nil
}

func (s *Service) GetDesktop(ctx context.Context, organizationID, id string) (*DesktopSummary, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.OrganizationID == "" || d.OrganizationID != organizationID {
		return nil, store.ErrNotFound
	}
	summary := &DesktopSummary{Desktop: d}
	live, err := s.liveInstanceState(ctx, d)
	if err == nil {
		summary.LiveState = live
	}
	return summary, nil
}

func (s *Service) RefreshDesktop(ctx context.Context, organizationID, id string) (*DesktopSummary, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.OrganizationID == "" || d.OrganizationID != organizationID {
		return nil, store.ErrNotFound
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
	if live == "terminated" {
		if err := s.store.MarkTerminated(ctx, id); err != nil {
			return nil, err
		}
		d, err = s.store.Get(ctx, id)
		if err != nil {
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

func (s *Service) StopDesktop(ctx context.Context, organizationID, id string) (*OperationResult, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.OrganizationID == "" || d.OrganizationID != organizationID {
		return nil, store.ErrNotFound
	}
	if d.InstanceID == "" {
		return nil, fmt.Errorf("desktop %q has no instance ID", id)
	}
	if err := s.requireAWS(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.lifecycleTimeout)
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

func (s *Service) StartDesktop(ctx context.Context, organizationID, id string) (*OperationResult, error) {
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.OrganizationID == "" || d.OrganizationID != organizationID {
		return nil, store.ErrNotFound
	}
	if d.InstanceID == "" {
		return nil, fmt.Errorf("desktop %q has no instance ID", id)
	}
	if err := s.requireAWS(); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.lifecycleTimeout)
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

func (s *Service) requirePulumi() error {
	if s.pulumiRunner == nil {
		return errors.New("pulumi lifecycle runner is not configured")
	}
	return nil
}

// TerminateDesktop runs pulumi destroy on the desktop stack and marks the
// fleet record terminated. It mirrors the CLI terminate command and returns
// ErrNotFound when the desktop does not belong to the given organization.
func (s *Service) TerminateDesktop(ctx context.Context, organizationID, id string) (*OperationResult, error) {
	if err := s.requirePulumi(); err != nil {
		return nil, err
	}
	d, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.OrganizationID == "" || d.OrganizationID != organizationID {
		return nil, store.ErrNotFound
	}
	if d.State == store.StateTerminated {
		return nil, fmt.Errorf("desktop %q is already terminated", id)
	}

	mgr := desktop.NewManager(s.store)
	if err := mgr.MarkTerminating(ctx, id); err != nil {
		return nil, err
	}

	backendURL := "s3://" + s.cfg.Pulumi.BackendBucket
	workDir := filepath.Join(s.cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	ref := pulumi.DesktopStackRef(backendURL, id, workDir)

	destroyCtx, destroyCancel := context.WithTimeout(ctx, s.lifecycleTimeout)
	defer destroyCancel()
	if destroyErr := s.pulumiRunner.Destroy(destroyCtx, ref, io.Discard); destroyErr != nil {
		_ = mgr.RecordFailure(ctx, id, "terminate", destroyErr.Error())
		return nil, fmt.Errorf("pulumi destroy: %w", destroyErr)
	}

	if err := s.store.MarkTerminated(ctx, id); err != nil {
		_ = mgr.RecordFailure(ctx, id, "terminate", fmt.Sprintf("mark terminated after successful destroy: %v", err))
		return nil, fmt.Errorf("mark terminated: %w", err)
	}
	updated, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &OperationResult{
		DesktopID: id,
		Action:    "terminate",
		State:     updated.State,
		Message:   "desktop terminated",
		Desktop:   updated,
	}, nil
}

// CreateDesktop provisions a new desktop via the Pulumi desktop stack and
// records it in the fleet store. It reads the foundation stack outputs to
// obtain the subnet, security group, and instance profile. The caller must
// set Owner in the request; all other fields are optional with sensible defaults.
func (s *Service) CreateDesktop(ctx context.Context, organizationID string, req *CreateDesktopRequest) (*OperationResult, error) {
	if err := s.requirePulumi(); err != nil {
		return nil, err
	}
	if req.Owner == "" {
		return nil, errors.New("owner is required")
	}

	env := req.Environment
	if env == "" {
		env = s.cfg.Environment()
	}
	zone, err := s.cfg.DNSZone()
	if err != nil {
		return nil, fmt.Errorf("resolve DNS zone: %w", err)
	}
	instanceType := req.InstanceType
	if instanceType == "" {
		instanceType = s.cfg.Desktop.InstanceType
		if instanceType == "" {
			instanceType = appconfig.DefaultInstanceType
		}
	}
	volumeSize := req.VolumeSize
	if volumeSize == 0 {
		volumeSize = appconfig.DefaultVolumeSize
	}

	backendURL := "s3://" + s.cfg.Pulumi.BackendBucket
	foundationWorkDir := filepath.Join(s.cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	foundationRef := pulumi.FoundationStackRef(backendURL, env, foundationWorkDir)

	outputsCtx, outputsCancel := context.WithTimeout(ctx, s.lifecycleTimeout)
	defer outputsCancel()
	foundationOutputs, err := s.pulumiRunner.Outputs(outputsCtx, foundationRef)
	if err != nil {
		return nil, fmt.Errorf("read foundation stack outputs: %w", err)
	}
	if err := pulumi.ValidateFoundationOutputs(foundationOutputs); err != nil {
		return nil, fmt.Errorf("foundation stack: %w", err)
	}

	desktopID, err := desktop.GenerateID()
	if err != nil {
		return nil, fmt.Errorf("generate desktop ID: %w", err)
	}
	mgr := desktop.NewManager(s.store)
	createReq := &desktop.CreateRequest{
		OrganizationID: organizationID,
		GitHubOwner:    req.Owner,
		DesktopName:    req.Name,
		Repos:          req.Repos,
		AMIID:          req.AMIID,
		InstanceType:   instanceType,
		MarketType:     req.MarketType,
		Zone:           zone,
		BackendBucket:  s.cfg.Pulumi.BackendBucket,
		Region:         s.cfg.AWS.Region,
		Environment:    env,
	}
	if err := createReq.Validate(); err != nil {
		return nil, fmt.Errorf("invalid create request: %w", err)
	}
	if err := mgr.CreateRecord(ctx, desktopID, createReq); err != nil {
		return nil, fmt.Errorf("create fleet record: %w", err)
	}

	desktopWorkDir := filepath.Join(s.cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	desktopRef := pulumi.DesktopStackRef(backendURL, desktopID, desktopWorkDir)
	stackCfg := pulumi.DesktopConfig(
		s.cfg.AWS.Region, desktopID, req.Name, req.Owner, zone,
		instanceType,
		foundationOutputs[pulumi.OutputSubnetID],
		foundationOutputs[pulumi.OutputSGID],
		foundationOutputs[pulumi.OutputInstanceProfile],
		s.cfg.Desktop.SSHKeyName,
		req.Repos,
		0, volumeSize,
		req.AMIID, req.UserDataBase64, env,
		false,
		req.MarketType, req.SpotMaxPrice,
		pulumi.WorkspaceConfig{},
	)

	upCtx, upCancel := context.WithTimeout(ctx, s.lifecycleTimeout)
	defer upCancel()
	outputs, upErr := s.pulumiRunner.Up(upCtx, desktopRef, stackCfg, io.Discard)
	if upErr != nil {
		_ = mgr.RecordFailure(ctx, desktopID, "create", upErr.Error())
		return nil, fmt.Errorf("pulumi up: %w", upErr)
	}

	if err := mgr.UpdateFromOutputs(ctx, desktopID, outputs); err != nil {
		return nil, fmt.Errorf("update fleet record from outputs: %w", err)
	}
	if err := mgr.MarkReady(ctx, desktopID, "provisioned"); err != nil {
		return nil, fmt.Errorf("mark ready: %w", err)
	}

	created, err := s.store.Get(ctx, desktopID)
	if err != nil {
		return nil, err
	}
	return &OperationResult{
		DesktopID: desktopID,
		Action:    "create",
		State:     created.State,
		Message:   "desktop created",
		Desktop:   created,
	}, nil
}
