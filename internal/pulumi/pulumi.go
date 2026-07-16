// Package pulumi provides helpers for managing ai-desktops Pulumi stacks.
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
func FoundationStackRef(backendURL, env, workDir string) *StackRef {
	return &StackRef{
		Project:    "foundation",
		StackName:  "foundation-" + env,
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
func FoundationConfig(region, zone, fleetTable, operatorCIDR, environment, vpcID string) StackConfig {
	cfg := StackConfig{
		"aws:region":   region,
		"zone":         zone,
		"fleetTable":   fleetTable,
		"operatorCIDR": operatorCIDR,
		"environment":  environment,
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
// bridgePort is the localhost port for ai-agent-bridge; 0 means use the stack default (9445).
// volumeSize is the root EBS volume size in GiB; 0 means use the stack default (100).
// amiID is the pre-baked AMI ID.
// userData is the pre-rendered cloud-init user-data.
// nestedVirtualization enables KVM by setting CpuOptions.NestedVirtualization=enabled on the EC2 instance.
func DesktopConfig(
	region, desktopID, gitHubOwner, zone, instanceType,
	subnetID, sgID, instanceProfile, sshKeyName string,
	repos []string,
	bridgePort, volumeSize int,
	amiID, userData, environment string,
	nestedVirtualization bool,
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
	if userData != "" {
		cfg["userData"] = userData
	}
	if nestedVirtualization {
		cfg["nestedVirtualization"] = "true"
	}
	return cfg
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
	OutputSGID            = "securityGroupId"
	OutputInstanceProfile = "instanceProfile"
	OutputZoneID          = "zoneId"
	OutputFleetTable      = "fleetTable"
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
}

// NewRunner returns a Runner with default settings.
func NewRunner() *Runner { return &Runner{} }

// Refresh syncs Pulumi state with the actual cloud provider state for the stack.
// Use this when AWS resources have been modified outside of Pulumi.
func (r *Runner) Refresh(ctx context.Context, ref *StackRef, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return fmt.Errorf("stack select: %w", err)
	}
	if err := r.run(ctx, ref.WorkDir, env, progress, "refresh", "--yes", "--non-interactive", "--color", "never"); err != nil {
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
	if err := r.run(ctx, ref.WorkDir, env, progress, "up", "--yes", "--non-interactive", "--color", "never"); err != nil {
		return nil, fmt.Errorf("pulumi up: %w", err)
	}
	return r.outputs(ctx, ref.WorkDir, env)
}

// Up selects (or creates) the stack, applies cfg, runs `pulumi up`, and
// returns the stack's output map. Progress is streamed to progress if non-nil.
func (r *Runner) Up(ctx context.Context, ref *StackRef, cfg StackConfig, progress io.Writer) (map[string]string, error) {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return nil, fmt.Errorf("stack select: %w", err)
	}
	for k, v := range cfg {
		if err := r.run(ctx, ref.WorkDir, env, progress, "config", "set", "--plaintext", k, v); err != nil {
			return nil, fmt.Errorf("config set %s: %w", k, err)
		}
	}
	if err := r.run(ctx, ref.WorkDir, env, progress, "up", "--yes", "--non-interactive", "--color", "never"); err != nil {
		return nil, fmt.Errorf("pulumi up: %w", err)
	}
	return r.outputs(ctx, ref.WorkDir, env)
}

// Preview selects (or creates) the stack, applies cfg, and runs `pulumi preview`.
func (r *Runner) Preview(ctx context.Context, ref *StackRef, cfg StackConfig, progress io.Writer) error {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, progress, "stack", "select", "--create", ref.StackName); err != nil {
		return fmt.Errorf("stack select: %w", err)
	}
	for k, v := range cfg {
		if err := r.run(ctx, ref.WorkDir, env, progress, "config", "set", "--plaintext", k, v); err != nil {
			return fmt.Errorf("config set %s: %w", k, err)
		}
	}
	return r.run(ctx, ref.WorkDir, env, progress, "preview", "--color", "never")
}

// Outputs reads the current output map for an existing stack without running
// pulumi up or destroy. Returns an error if the stack does not exist.
func (r *Runner) Outputs(ctx context.Context, ref *StackRef) (map[string]string, error) {
	env := r.env(ref.BackendURL)
	if err := r.run(ctx, ref.WorkDir, env, nil, "stack", "select", ref.StackName); err != nil {
		return nil, fmt.Errorf("stack select %s: %w", ref.StackName, err)
	}
	return r.outputs(ctx, ref.WorkDir, env)
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
	out, err = r.runCaptureTee(ctx, ref.WorkDir, env, progress, "destroy", "--yes", "--non-interactive", "--color", "never")
	if err != nil {
		if strings.Contains(out, "currently locked") {
			if progress != nil {
				_, _ = fmt.Fprintln(progress, "stack is locked by a previous operation; running pulumi cancel to release lock...")
			}
			if cancelErr := r.run(ctx, ref.WorkDir, env, progress, "cancel", "--yes"); cancelErr != nil {
				return fmt.Errorf("pulumi destroy (locked; cancel failed: %v): %w", cancelErr, err)
			}
			if err2 := r.run(ctx, ref.WorkDir, env, progress, "destroy", "--yes", "--non-interactive", "--color", "never"); err2 != nil {
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

func (r *Runner) outputs(ctx context.Context, workDir string, env []string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "pulumi", "stack", "output", "--json") //nolint:gosec
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
