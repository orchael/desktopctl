package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

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

// NoVNCURL returns the browser URL for the given hostname.
// noVNC listens on HTTPS port 8443 (novnc-desktop v0.1.5+).
func NoVNCURL(hostname string) string {
	return "https://" + hostname + ":8443/novnc"
}

// SSHTarget returns the SSH connection string for the given hostname.
func SSHTarget(hostname string) string {
	return "ubuntu@" + hostname
}

// CreateRequest holds the parameters for creating a new desktop.
type CreateRequest struct {
	GitHubOwner   string
	Repos         []string
	InstanceType  string
	Zone          string
	OperatorCIDR  string
	SSHKeyPath    string
	SSHKeyName    string // EC2 key pair name (registered in AWS)
	PATSecret     string
	BackendBucket string
	Region        string
	Profile       string
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
	return nil
}

// CreateRecord initialises a fleet record in the creating state.
func (m *Manager) CreateRecord(ctx context.Context, id string, req *CreateRequest) error {
	zone := req.Zone
	hostname := Hostname(id, zone)
	d := &store.Desktop{
		DesktopID:     id,
		StackName:     StackName(id),
		GitHubOwner:   req.GitHubOwner,
		State:         store.StateCreating,
		Hostname:      hostname,
		NoVNCURL:      NoVNCURL(hostname),
		SSHTarget:     SSHTarget(hostname),
		WorkspacePath: "/workspace",
		Repos:         req.Repos,
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
	return m.Store.Update(ctx, d)
}

// MarkStopped marks the desktop as stopped.
func (m *Manager) MarkStopped(ctx context.Context, id string) error {
	d, err := m.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	d.State = store.StateStopped
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
