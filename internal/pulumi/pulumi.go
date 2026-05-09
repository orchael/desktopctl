// Package pulumi provides a thin wrapper around the Pulumi Automation API for
// managing ai-desktops infrastructure stacks.
package pulumi

import (
	"fmt"
	"strings"
)

// StackRef identifies a Pulumi stack.
type StackRef struct {
	Project     string
	StackName   string
	BackendURL  string
	WorkDir     string
}

// FullName returns the full stack name as "project/stack".
func (r *StackRef) FullName() string {
	return r.Project + "/" + r.StackName
}

// FoundationStackRef returns the StackRef for the shared foundation stack.
func FoundationStackRef(backendURL, env, workDir string) *StackRef {
	return &StackRef{
		Project:    "ai-desktops",
		StackName:  "foundation-" + env,
		BackendURL: backendURL,
		WorkDir:    workDir,
	}
}

// DesktopStackRef returns the StackRef for a single desktop stack.
func DesktopStackRef(backendURL, desktopID, workDir string) *StackRef {
	return &StackRef{
		Project:    "ai-desktops",
		StackName:  "desktop-" + desktopID,
		BackendURL: backendURL,
		WorkDir:    workDir,
	}
}

// StackConfig is the key-value configuration passed to a Pulumi stack.
type StackConfig map[string]string

// FoundationConfig builds the Pulumi config for the foundation stack.
func FoundationConfig(region, zone, fleetTable, operatorCIDR string) StackConfig {
	return StackConfig{
		"aws:region":    region,
		"zone":          zone,
		"fleetTable":    fleetTable,
		"operatorCIDR":  operatorCIDR,
	}
}

// DesktopConfig builds the Pulumi config for a desktop stack.
func DesktopConfig(
	region, desktopID, gitHubOwner, zone, instanceType,
	operatorCIDR, sshKeyPath, patSecret string,
	repos []string,
) StackConfig {
	return StackConfig{
		"aws:region":   region,
		"desktopId":   desktopID,
		"githubOwner": gitHubOwner,
		"zone":        zone,
		"instanceType": instanceType,
		"operatorCIDR": operatorCIDR,
		"sshKeyPath":  sshKeyPath,
		"patSecret":   patSecret,
		"repos":       strings.Join(repos, ","),
	}
}

// OutputKey constants for stack outputs.
const (
	OutputInstanceID    = "instanceId"
	OutputHostname      = "hostname"
	OutputNoVNCURL      = "novncUrl"
	OutputSSHTarget     = "sshTarget"
	OutputWorkspacePath = "workspacePath"
	OutputGitHubOwner   = "githubOwner"
	OutputSubnetID      = "subnetId"
	OutputSGID          = "securityGroupId"
	OutputInstanceProfile = "instanceProfile"
	OutputZoneID        = "zoneId"
	OutputFleetTable    = "fleetTable"
)

// ParseOutputs extracts string values from a raw output map.
// Non-string or missing values are silently skipped.
func ParseOutputs(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// ValidateFoundationOutputs checks that the required foundation stack outputs
// are present.
func ValidateFoundationOutputs(outputs map[string]string) error {
	required := []string{OutputSubnetID, OutputSGID, OutputInstanceProfile, OutputZoneID}
	for _, k := range required {
		if outputs[k] == "" {
			return fmt.Errorf("foundation stack output %q is missing or empty", k)
		}
	}
	return nil
}
