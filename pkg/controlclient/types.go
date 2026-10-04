// Package controlclient is the public, secret-free ai-desktops control API contract.
package controlclient

import "time"

type AcquireRequest struct {
	Consumer       string `json:"consumer"`
	RunID          string `json:"run_id"`
	OrganizationID string `json:"organization_id"`
	Repository     string `json:"repository"`
	BaseRef        string `json:"base_ref"`
	WorkerProfile  string `json:"worker_profile"`
	TTLSeconds     int    `json:"ttl_seconds"`
}
type DesktopReference struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Readiness string `json:"readiness"`
	URL       string `json:"url,omitempty"`
}
type BridgectlReference struct {
	Available            bool   `json:"available"`
	Target               string `json:"target"`
	ServerName           string `json:"server_name,omitempty"`
	CredentialProfile    string `json:"credential_profile,omitempty"`
	BridgeOrganizationID string `json:"bridge_organization_id,omitempty"`
	BridgeInstallationID string `json:"bridge_installation_id,omitempty"`
	BridgeURL            string `json:"bridge_url,omitempty"`
}
type WorkspaceReference struct {
	Path      string             `json:"path"`
	Branch    string             `json:"branch"`
	BaseSHA   string             `json:"base_sha"`
	Bridgectl BridgectlReference `json:"bridgectl"`
}
type Lease struct {
	ID          string             `json:"id"`
	Request     AcquireRequest     `json:"request"`
	State       string             `json:"state"`
	Desktop     DesktopReference   `json:"desktop"`
	Workspace   WorkspaceReference `json:"workspace"`
	FailureCode string             `json:"failure_code,omitempty"`
	CreatedAt   time.Time          `json:"created_at"`
	ExpiresAt   time.Time          `json:"expires_at"`
}
