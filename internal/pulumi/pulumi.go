// Package pulumi provides helpers for managing desktopctl Pulumi stacks.
// Stacks are driven by invoking the `pulumi` CLI as a subprocess rather than
// using the Pulumi Automation SDK, avoiding the heavyweight SDK dependency.
// The `pulumi` binary must be on PATH.
package pulumi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// StackRef identifies a Pulumi stack.
type StackRef struct {
	Project    string
	StackName  string
	BackendURL string
	WorkDir    string
}

// FullName returns the full stack name as "project/stack".
func (r *StackRef) FullName() string {
	return r.Project + "/" + r.StackName
}

// FoundationStackRef returns the StackRef for the shared foundation stack.
func FoundationStackRef(backendURL, env, region, workDir string) *StackRef {
	return &StackRef{
		Project:    "foundation",
		StackName:  "foundation-" + env + "-" + region,
		BackendURL: backendURL,
		WorkDir:    workDir,
	}
}

// DesktopStackRef returns the StackRef for a single desktop stack.
func DesktopStackRef(backendURL, desktopID, workDir string) *StackRef {
	return &StackRef{
		Project:    "desktop",
		StackName:  "desktop-" + desktopID,
		BackendURL: backendURL,
		WorkDir:    workDir,
	}
}

// StackConfig is the key-value configuration passed to a Pulumi stack.
type StackConfig map[string]string

// FoundationConfig builds the Pulumi config for the foundation stack.
// vpcID is optional; when non-empty the foundation stack will use the existing VPC
// instead of creating a new one.
func FoundationConfig(region, zone, fleetTable, operatorCIDR, environment, vpcID, backendBucket string) StackConfig {
	cfg := StackConfig{
		"aws:region":          region,
		"region":              region,
		"zone":                zone,
		"fleetTable":          fleetTable,
		"operatorCIDR":        operatorCIDR,
		"environment":         environment,
		"pulumiBackendBucket": backendBucket,
	}
	if vpcID != "" {
		cfg["vpcId"] = vpcID
	}
	return cfg
}

// DesktopConfig builds the Pulumi config for a desktop stack.
// subnetID, sgID, and instanceProfile come from the foundation stack outputs.
// sshKeyName is the EC2 key pair name (not a local file path); it may be empty
// if SSH key-pair attachment is not required.
// bridgePort is the localhost port for bridgectl; 0 means use the stack default (9445).
// volumeSize is the root EBS volume size in GiB; 0 means use the stack default (100).
// amiID is the pre-baked AMI ID.
// userDataBase64 is gzip-compressed, base64-encoded cloud-init user-data.
// nestedVirtualization enables KVM by setting CpuOptions.NestedVirtualization=enabled on the EC2 instance.
// marketType is "on-demand" or "spot"; spotMaxPrice is optional.
func DesktopConfig(
	region, desktopID, desktopName, gitHubOwner, zone, instanceType,
	subnetID, sgID, instanceProfile, sshKeyName string,
	repos []string,
	bridgePort, volumeSize int,
	amiID, userDataBase64, environment string,
	nestedVirtualization bool,
	marketType, spotMaxPrice string,
	workspace WorkspaceConfig,
) StackConfig {
	cfg := StackConfig{
		"aws:region":      region,
		"desktopId":       desktopID,
		"githubOwner":     gitHubOwner,
		"zone":            zone,
		"instanceType":    instanceType,
		"subnetId":        subnetID,
		"securityGroupId": sgID,
		"instanceProfile": instanceProfile,
		"repos":           strings.Join(repos, ","),
		"environment":     environment,
	}
	if desktopName != "" {
		cfg["desktopName"] = desktopName
	}
	if sshKeyName != "" {
		cfg["sshKeyName"] = sshKeyName
	}
	if bridgePort > 0 {
		cfg["bridgePort"] = fmt.Sprintf("%d", bridgePort)
	}
	if volumeSize > 0 {
		cfg["volumeSize"] = fmt.Sprintf("%d", volumeSize)
	}
	if amiID != "" {
		cfg["amiId"] = amiID
	}
	if userDataBase64 != "" {
		cfg["userDataBase64"] = userDataBase64
	}
	if nestedVirtualization {
		cfg["nestedVirtualization"] = "true"
	}
	if marketType != "" {
		cfg["marketType"] = marketType
	}
	if spotMaxPrice != "" {
		cfg["spotMaxPrice"] = spotMaxPrice
	}
	if workspace.Mode != "" {
		cfg["workspaceMode"] = workspace.Mode
	}
	if workspace.Name != "" {
		cfg["workspaceName"] = workspace.Name
	}
	if workspace.EFSFileSystemID != "" {
		cfg["workspaceEFSFileSystemId"] = workspace.EFSFileSystemID
	}
	if workspace.EFSAccessPointID != "" {
		cfg["workspaceEFSAccessPointId"] = workspace.EFSAccessPointID
	}
	return cfg
}

type WorkspaceConfig struct {
	Mode             string
	Name             string
	EFSFileSystemID  string
	EFSAccessPointID string
}

// OutputKey constants for stack outputs.
const (
	OutputInstanceID      = "instanceId"
	OutputHostname        = "hostname"
	OutputNoVNCURL        = "novncUrl"
	OutputSSHTarget       = "sshTarget"
	OutputWorkspacePath   = "workspacePath"
	OutputGitHubOwner     = "githubOwner"
	OutputSubnetID        = "subnetId"
	OutputSubnetIDs       = "subnetIds"
	OutputSGID            = "securityGroupId"
	OutputInstanceProfile = "instanceProfile"
	OutputZoneID          = "zoneId"
	OutputFleetTable      = "fleetTable"
	OutputAMITable        = "amiTable"
	OutputMarketType      = "marketType"
	OutputWorkspaceMode   = "workspaceMode"
	OutputEFSFileSystemID = "efsFileSystemId"
	OutputEFSSGID         = "efsSecurityGroupId"
	OutputOperatorSecret  = "operatorCredentialsSecretName"
	OutputOperatorRoleArn = "operatorRoleArn"
	OutputOperatorUser    = "operatorUserName"
)

// Runner drives Pulumi stacks by invoking the `pulumi` CLI as a subprocess.
// The `pulumi` binary must be on PATH. The S3 backend URL and a blank config
// passphrase are injected via environment variables.
type Runner struct {
	// PassPhrase is the Pulumi config encryption passphrase. Defaults to ""
	// which is appropriate when config values are not secrets.
	PassPhrase string
	// AWSProfile is the AWS CLI profile to pass to Pulumi via AWS_PROFILE.
	AWSProfile string
	// AWSRegion selects the S3 backend endpoint as well as the provider region.
	AWSRegion string
}

// NewRunner returns a Runner with default settings.
func NewRunner() *Runner { return &Runner{} }

// StackExists checks the selected backend without creating a stack. It is used
// before fresh foundation initialization to avoid adopting partial old state.
func (r *Runner) StackExists(ctx context.Context, ref *StackRef) (bool, error) {
	out, err := r.runCapture(ctx, ref.WorkDir, r.env(ref.BackendURL), "stack", "select", ref.StackName)
	if err == nil {
		return true, nil
	}
	if strings.Contains(out, "no stack named") || strings.Contains(out, "does not exist") {
		return false, nil
	}
	return false, fmt.Errorf("check stack %s: %s: %w", ref.FullName(), tailOutput(out, 1000), err)
}

// StackHasResource reports whether an existing stack still manages a resource
// with the given logical name. A destroyed stack can remain in the backend.
func (r *Runner) StackHasResource(ctx context.Context, ref *StackRef, logicalName string) (bool, error) {
	out, err := r.runCapture(ctx, ref.WorkDir, r.env(ref.BackendURL), "stack", "export", "--stack", ref.StackName)
	if err != nil {
		return false, fmt.Errorf("export stack %s: %w", ref.FullName(), err)
	}
	var snapshot struct {
		Deployment *struct {
			Resources []struct {
				URN string `json:"urn"`
			} `json:"resources"`
		} `json:"deployment"`
	}
	if err := json.Unmarshal([]byte(out), &snapshot); err != nil {
		return false, fmt.Errorf("decode stack %s: %w", ref.FullName(), err)
	}
	if snapshot.Deployment == nil || snapshot.Deployment.Resources == nil {
		return false, fmt.Errorf("stack %s export has no deployment resources", ref.FullName())
	}
	for _, resource := range snapshot.Deployment.Resources {
		if strings.HasSuffix(resource.URN, "::"+logicalName) {
			return true, nil
		}
	}
	return false, nil
}

// RemoveEmptyStack removes preview-only state. Pulumi refuses removal when the
// stack owns resources, so this cannot destroy an applied foundation.
func (r *Runner) RemoveEmptyStack(ctx context.Context, ref *StackRef) error {
	if err := r.run(ctx, ref.WorkDir, r.env(ref.BackendURL), nil, "stack", "rm", ref.StackName, "--yes", "--non-interactive"); err != nil {
		return fmt.Errorf("remove preview stack %s: %w", ref.FullName(), err)
	}
	return nil
}

// Refresh syncs Pulumi state with the actual cloud provider state for the stack.
// Use this when AWS resources have been modified outside of Pulumi.
func (r *Runner) Refresh(ctx context.Context, ref *StackRef, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return fmt.Errorf("stack select: %w", err)
	}
	if err := r.run(ctx, ref.WorkDir, env, progress, "refresh", "--stack", ref.StackName, "--yes", "--non-interactive", "--color", "never"); err != nil {
		return fmt.Errorf("pulumi refresh: %w", err)
	}
	return nil
}

// RefreshAndUp refreshes the stack state from AWS then runs pulumi up without
// changing any config values. This is used after an instance is started from
// hibernation so that resources whose attributes change (e.g. the public IP
// assigned to the EC2 instance) are reconciled — specifically the Route53 A
// record that points at the instance's new public IP.
func (r *Runner) RefreshAndUp(ctx context.Context, ref *StackRef, progress io.Writer) (map[string]string, error) {
	if err := r.Refresh(ctx, ref, progress); err != nil {
		return nil, err
	}
	env := r.env(ref.BackendURL)
	out, err := r.runCaptureTee(ctx, ref.WorkDir, env, progress, "up", "--stack", ref.StackName, "--yes", "--non-interactive", "--color", "never")
	if err != nil {
		return nil, fmt.Errorf("pulumi up: %s: %w", tailOutput(out, 4000), err)
	}
	return r.outputs(ctx, ref.WorkDir, env, ref.StackName)
}

// SetConfig selects an existing stack and writes one plaintext config value.
func (r *Runner) SetConfig(ctx context.Context, ref *StackRef, key, value string, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", ref.StackName); err != nil {
		return fmt.Errorf("stack select %s: %w", ref.StackName, err)
	}
	if err := r.run(ctx, ref.WorkDir, env, progress, "config", "set", "--stack", ref.StackName, "--plaintext", key, value); err != nil {
		return fmt.Errorf("config set %s: %w", key, err)
	}
	return nil
}

// Up selects (or creates) the stack, applies cfg, runs `pulumi up`, and
// returns the stack's output map. Progress is streamed to progress if non-nil.
func (r *Runner) Up(ctx context.Context, ref *StackRef, cfg StackConfig, progress io.Writer) (map[string]string, error) {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return nil, fmt.Errorf("stack select: %w", err)
	}
	for k, v := range cfg {
		if err := r.run(ctx, ref.WorkDir, env, progress, "config", "set", "--stack", ref.StackName, "--plaintext", k, v); err != nil {
			return nil, fmt.Errorf("config set %s: %w", k, err)
		}
	}
	out, err := r.runCaptureTee(ctx, ref.WorkDir, env, progress, "up", "--stack", ref.StackName, "--yes", "--non-interactive", "--color", "never")
	if err != nil {
		return nil, fmt.Errorf("pulumi up: %s: %w", tailOutput(out, 4000), err)
	}
	return r.outputs(ctx, ref.WorkDir, env, ref.StackName)
}

// Preview selects (or creates) the stack, applies cfg, and runs `pulumi preview`.
func (r *Runner) Preview(ctx context.Context, ref *StackRef, cfg StackConfig, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return fmt.Errorf("stack select: %w", err)
	}
	for k, v := range cfg {
		if err := r.run(ctx, ref.WorkDir, env, progress, "config", "set", "--stack", ref.StackName, "--plaintext", k, v); err != nil {
			return fmt.Errorf("config set %s: %w", k, err)
		}
	}
	return r.run(ctx, ref.WorkDir, env, progress, "preview", "--stack", ref.StackName, "--color", "never")
}

// Outputs reads the current output map for an existing stack without running
// pulumi up or destroy. Returns an error if the stack does not exist.
func (r *Runner) Outputs(ctx context.Context, ref *StackRef) (map[string]string, error) {
	env := r.env(ref.BackendURL)
	out, err := r.runCapture(ctx, ref.WorkDir, env, "stack", "select", ref.StackName)
	if err != nil {
		return nil, fmt.Errorf("stack select %s: %s: %w", ref.StackName, tailOutput(out, 1000), err)
	}
	return r.outputs(ctx, ref.WorkDir, env, ref.StackName)
}

// FoundationOutputs reads the regional foundation, or a legacy environment-only
// foundation if the regional stack does not exist and its scope matches.
func (r *Runner) FoundationOutputs(ctx context.Context, ref *StackRef, environment, region string) (map[string]string, error) {
	exists, err := r.StackExists(ctx, ref)
	if err != nil {
		return nil, err
	}
	if exists {
		return r.Outputs(ctx, ref)
	}

	legacy := *ref
	legacy.StackName = "foundation-" + environment
	exists, err = r.StackExists(ctx, &legacy)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("foundation stack %s does not exist (legacy stack %s also absent)", ref.StackName, legacy.StackName)
	}
	// Local Pulumi.<stack>.yaml files can be absent or stale. History records
	// the configuration used by the last deployment in the selected backend.
	out, err := r.runCapture(ctx, legacy.WorkDir, r.env(legacy.BackendURL), "stack", "history", "--stack", legacy.StackName, "--json", "--page-size", "1")
	if err != nil {
		return nil, fmt.Errorf("read legacy foundation %s deployment history: %w", legacy.StackName, err)
	}
	var history []struct {
		Result string `json:"result"`
		Config map[string]struct {
			Value  string `json:"value"`
			Secret bool   `json:"secret"`
		} `json:"config"`
	}
	if err := json.Unmarshal([]byte(out), &history); err != nil {
		return nil, fmt.Errorf("decode legacy foundation %s deployment history: %w", legacy.StackName, err)
	}
	if len(history) == 0 || history[0].Result != "succeeded" {
		return nil, fmt.Errorf("legacy foundation %s has no latest successful deployment to verify", legacy.StackName)
	}
	for key, want := range map[string]string{"aws:region": region, "foundation:environment": environment} {
		entry, ok := history[0].Config[key]
		if !ok || entry.Secret || entry.Value != want {
			return nil, fmt.Errorf("legacy foundation %s deployed config %s does not match %q", legacy.StackName, key, want)
		}
	}
	return r.Outputs(ctx, &legacy)
}

// Destroy selects the stack and runs `pulumi destroy`. Progress is streamed to
// progress if non-nil. If the stack does not exist in the backend, Destroy
// returns nil (nothing to destroy). If the stack is locked by a previous
// interrupted operation, Destroy automatically runs `pulumi cancel` to release
// the lock and retries the destroy once.
func (r *Runner) Destroy(ctx context.Context, ref *StackRef, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	out, err := r.runCapture(ctx, ref.WorkDir, env, "stack", "select", ref.StackName)
	if progress != nil && out != "" {
		_, _ = fmt.Fprint(progress, out)
	}
	if err != nil {
		if strings.Contains(out, "no stack named") {
			return nil
		}
		return fmt.Errorf("stack select: %w", err)
	}
	out, err = r.runCaptureTee(ctx, ref.WorkDir, env, progress, "destroy", "--stack", ref.StackName, "--yes", "--non-interactive", "--color", "never")
	if err != nil {
		if strings.Contains(out, "currently locked") {
			if progress != nil {
				_, _ = fmt.Fprintln(progress, "stack is locked by a previous operation; running pulumi cancel to release lock...")
			}
			if cancelErr := r.run(ctx, ref.WorkDir, env, progress, "cancel", "--stack", ref.StackName, "--yes"); cancelErr != nil {
				return fmt.Errorf("pulumi destroy (locked; cancel failed: %v): %w", cancelErr, err)
			}
			if err2 := r.run(ctx, ref.WorkDir, env, progress, "destroy", "--stack", ref.StackName, "--yes", "--non-interactive", "--color", "never"); err2 != nil {
				return fmt.Errorf("pulumi destroy: %w", err2)
			}
			return nil
		}
		return fmt.Errorf("pulumi destroy: %w", err)
	}
	return nil
}

func (r *Runner) env(backendURL string) []string {
	e := append(os.Environ(),
		"PULUMI_BACKEND_URL="+backendURL,
		"PULUMI_CONFIG_PASSPHRASE="+r.PassPhrase,
	)
	if r.AWSProfile != "" {
		e = append(e, "AWS_PROFILE="+r.AWSProfile)
	}
	if r.AWSRegion != "" {
		e = append(e, "AWS_REGION="+r.AWSRegion, "AWS_DEFAULT_REGION="+r.AWSRegion)
	}
	return e
}

func (r *Runner) run(ctx context.Context, workDir string, env []string, progress io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "pulumi", args...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = env
	if progress != nil {
		cmd.Stdout = progress
		cmd.Stderr = progress
	}
	return cmd.Run()
}

// runCapture runs a pulumi command and returns its combined stdout+stderr
// output along with any error. Use this when the output must be inspected
// for error classification (e.g. "no stack named").
func (r *Runner) runCapture(ctx context.Context, workDir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "pulumi", args...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runCaptureTee runs a pulumi command, streaming combined output to progress
// while also capturing it for error classification.
func (r *Runner) runCaptureTee(ctx context.Context, workDir string, env []string, progress io.Writer, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "pulumi", args...) //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = env
	var buf strings.Builder
	if progress != nil {
		w := io.MultiWriter(&buf, progress)
		cmd.Stdout = w
		cmd.Stderr = w
	} else {
		cmd.Stdout = &buf
		cmd.Stderr = &buf
	}
	err := cmd.Run()
	return buf.String(), err
}

func (r *Runner) outputs(ctx context.Context, workDir string, env []string, stackName string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "pulumi", "stack", "output", "--stack", stackName, "--json") //nolint:gosec
	cmd.Dir = workDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("stack output: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse outputs: %w", err)
	}
	return ParseOutputs(raw), nil
}

func tailOutput(out string, maxLen int) string {
	out = strings.TrimSpace(out)
	if len(out) <= maxLen {
		return out
	}
	return "..." + out[len(out)-maxLen:]
}

// ParseOutputs extracts string values from a raw output map.
// String arrays are flattened to comma-separated strings so callers can keep
// using StackConfig's string map while consuming multi-value Pulumi outputs.
// Other non-string or missing values are silently skipped.
func ParseOutputs(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch value := v.(type) {
		case string:
			out[k] = value
		case []any:
			var parts []string
			for _, item := range value {
				if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
					parts = append(parts, strings.TrimSpace(s))
				}
			}
			if len(parts) > 0 {
				out[k] = strings.Join(parts, ",")
			}
		case []string:
			var parts []string
			for _, item := range value {
				if s := strings.TrimSpace(item); s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				out[k] = strings.Join(parts, ",")
			}
		default:
			continue
		}
	}
	return out
}

// ValidateFoundationOutputs checks that the required foundation stack outputs
// are present.
func ValidateFoundationOutputs(outputs map[string]string) error {
	return ValidateFoundationOutputsForMode(outputs, false)
}

func ValidateFoundationOutputsForMode(outputs map[string]string, noDNS bool) error {
	if noDNS && outputs[OutputZoneID] != "" {
		return fmt.Errorf("--no-dns requires a no-DNS foundation; this foundation has a Route 53 zone")
	}
	required := []string{OutputSubnetID, OutputSGID, OutputInstanceProfile}
	if !noDNS {
		required = append(required, OutputZoneID)
	}
	for _, k := range required {
		if outputs[k] == "" {
			return fmt.Errorf("foundation stack output %q is missing or empty", k)
		}
	}
	return nil
}
