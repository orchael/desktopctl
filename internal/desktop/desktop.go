package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/orchael/ai-desktops/internal/store"
)

// Manager orchestrates desktop lifecycle operations.
type Manager struct {
	Store store.Store
}

// NewManager creates a Manager with the given fleet store.
func NewManager(s store.Store) *Manager {
	return &Manager{Store: s}
}

// GenerateID generates a short random desktop identifier.
func GenerateID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "d-" + hex.EncodeToString(b), nil
}

// StackName returns the Pulumi stack name for the given desktop ID.
// This matches the name used by pulumi.DesktopStackRef (i.e. what the
// `pulumi stack select` command receives).
func StackName(desktopID string) string {
	return "desktop-" + desktopID
}

// Hostname returns the DNS hostname for the given desktop ID and zone.
func Hostname(desktopID, zone string) string {
	return desktopID + "." + zone
}

// NoVNCURL returns the browser URL for the noVNC client.
// The desktop-web dashboard owns /; noVNC is proxied under /novnc/.
func NoVNCURL(hostname string) string {
	return "https://" + hostname + ":8443/novnc/vnc.html"
}

// SSHTarget returns the SSH connection string for the given hostname.
func SSHTarget(hostname string) string {
	return "ubuntu@" + hostname
}

// CreateRequest holds the parameters for creating a new desktop.
type CreateRequest struct {
	DesktopName   string
	GitHubOwner   string
	Repos         []string
	InstanceType  string
	Zone          string
	OperatorCIDR  string
	SSHKeyPath    string
	SSHKeyName    string // EC2 key pair name (registered in AWS)
	GitHubSecret  string
	Secrets       []string // AWS Secrets Manager paths injected into the ubuntu environment
	TailscaleNet  string   // optional Tailscale tailnet/network name
	StepCAServer  string   // optional step-ca DNS name used for bridgectl trust/certs
	AVDNames      []string // Android Virtual Device names created at boot
	NestedVirt    bool     // true when the instance was launched with AmdSevSnp=disabled (--mobile / --nested-virtualization)
	MarketType    string   // on-demand or spot
	BackendBucket string
	Region        string
	Environment   string
	Profile       string
	AMIID         string // Pre-baked AMI ID (optional)
	WorkspaceMode string
	WorkspaceName string
	WorkspaceID   string
}

// Validate checks that the CreateRequest is well-formed.
func (r *CreateRequest) Validate() error {
	if r.GitHubOwner == "" {
		return fmt.Errorf("github_owner is required")
	}
	if r.Zone == "" {
		return fmt.Errorf("zone is required")
	}
	if r.BackendBucket == "" {
		return fmt.Errorf("pulumi backend bucket is required")
	}
	if r.WorkspaceMode == "" {
		r.WorkspaceMode = "local"
	}
	switch r.WorkspaceMode {
	case "local":
		if r.WorkspaceName != "" {
			return fmt.Errorf("workspace_name requires workspace_mode efs")
		}
	case "efs":
		if r.WorkspaceName == "" {
			return fmt.Errorf("workspace_name is required for workspace_mode efs")
		}
	default:
		return fmt.Errorf("workspace_mode must be local or efs")
	}
	return nil
}

// CreateRecord initialises a fleet record in the creating state.
func (m *Manager) CreateRecord(ctx context.Context, id string, req *CreateRequest) error {
	zone := req.Zone
	hostname := Hostname(id, zone)
	d := &store.Desktop{
		DesktopID:     id,
		DesktopName:   req.DesktopName,
		StackName:     StackName(id),
		GitHubOwner:   req.GitHubOwner,
		Region:        req.Region,
		Environment:   req.Environment,
		State:         store.StateCreating,
		Hostname:      hostname,
		NoVNCURL:      NoVNCURL(hostname),
		SSHTarget:     SSHTarget(hostname),
		AMIID:         req.AMIID,
		InstanceType:  req.InstanceType,
		NestedVirt:    req.NestedVirt,
		WorkspacePath: "/workspace",
		WorkspaceMode: req.WorkspaceMode,
		WorkspaceName: req.WorkspaceName,
		WorkspaceID:   req.WorkspaceID,
		Repos:         req.Repos,
		Secrets:       req.Secrets,
		TailscaleNet:  req.TailscaleNet,
		StepCAServer:  req.StepCAServer,
		AVDNames:      req.AVDNames,
		MarketType:    normalizeMarketType(req.MarketType),
	}
	return m.Store.Create(ctx, d)
}

// UpdateFromOutputs writes Pulumi stack outputs back into the fleet record.
func (m *Manager) UpdateFromOutputs(ctx context.Context, id string, outputs map[string]string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	if v, ok := outputs["instanceId"]; ok {
		d.InstanceID = v
	}
	if v, ok := outputs["hostname"]; ok {
		d.Hostname = v
	}
	if v, ok := outputs["novncUrl"]; ok {
		d.NoVNCURL = v
	}
	if v, ok := outputs["sshTarget"]; ok {
		d.SSHTarget = v
	}
	if v, ok := outputs["marketType"]; ok {
		d.MarketType = normalizeMarketType(v)
	}
	if v, ok := outputs["instanceType"]; ok {
		d.InstanceType = v
	}
	if v, ok := outputs["workspacePath"]; ok {
		d.WorkspacePath = v
	}
	if v, ok := outputs["workspaceMode"]; ok {
		d.WorkspaceMode = v
	}
	return m.Store.Update(ctx, d)
}

// MarkReady marks the desktop as ready with the given readiness summary.
func (m *Manager) MarkReady(ctx context.Context, id, readinessSummary string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State = store.StateReady
	d.Readiness = readinessSummary
	d.FailurePhase = ""
	d.FailureMsg = ""
	return m.Store.Update(ctx, d)
}

// MarkStopped marks the desktop as stopped.
func (m *Manager) MarkStopped(ctx context.Context, id string) error {
	return m.MarkStoppedWithReason(ctx, id, "")
}

// MarkStoppedWithReason marks the desktop as stopped and records why, when known.
func (m *Manager) MarkStoppedWithReason(ctx context.Context, id, reason string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State = store.StateStopped
	d.StopReason = reason
	if reason != "" {
		d.StoppedAt = storeTimestamp()
	}
	return m.Store.Update(ctx, d)
}

// MarkRunning marks a previously stopped desktop as ready again.
func (m *Manager) MarkRunning(ctx context.Context, id, readinessSummary string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State = store.StateReady
	d.Readiness = readinessSummary
	d.StopReason = ""
	d.StoppedAt = ""
	d.FailurePhase = ""
	d.FailureMsg = ""
	return m.Store.Update(ctx, d)
}

// MarkTerminating marks the desktop as terminating.
func (m *Manager) MarkTerminating(ctx context.Context, id string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State = store.StateTerminating
	return m.Store.Update(ctx, d)
}

// AddSecrets appends new secret paths to the desktop record, deduplicating
// against any paths already present.
func (m *Manager) AddSecrets(ctx context.Context, id string, paths []string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	existing := make(map[string]bool, len(d.Secrets))
	for _, p := range d.Secrets {
		existing[p] = true
	}
	for _, p := range paths {
		if !existing[p] {
			d.Secrets = append(d.Secrets, p)
			existing[p] = true
		}
	}
	return m.Store.Update(ctx, d)
}

// RemoveSecrets removes secret paths from the desktop record. Unknown paths are
// ignored so repeated remove operations are idempotent.
func (m *Manager) RemoveSecrets(ctx context.Context, id string, paths []string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}

	remove := make(map[string]bool, len(paths))
	for _, p := range paths {
		remove[p] = true
	}

	remaining := make([]string, 0, len(d.Secrets))
	for _, p := range d.Secrets {
		if !remove[p] {
			remaining = append(remaining, p)
		}
	}
	d.Secrets = remaining
	return m.Store.Update(ctx, d)
}

// RecordFailure records a provisioning or lifecycle failure.
func (m *Manager) RecordFailure(ctx context.Context, id, phase, message string) error {
	return m.Store.RecordFailure(ctx, id, phase, message)
}

// RepoNames extracts the repository short names from a desktop record.
func RepoNames(d *store.Desktop) []string {
	names := make([]string, 0, len(d.Repos))
	for _, r := range d.Repos {
		parts := strings.Split(strings.TrimSuffix(r, ".git"), "/")
		if len(parts) > 0 {
			names = append(names, parts[len(parts)-1])
		}
	}
	return names
}

func normalizeMarketType(marketType string) string {
	if marketType == store.MarketSpot {
		return store.MarketSpot
	}
	return store.MarketOnDemand
}

func storeTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
