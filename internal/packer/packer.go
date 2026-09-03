package packer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// packerEnv returns os.Environ() with LANG and LC_ALL set to en_US.UTF-8 when
// absent. Packer's ansible provisioner runs "ansible-playbook --version" as a
// preflight check; Ansible requires a UTF-8 locale or it exits non-zero.
// It also maps GITHUB_NPM_TOKEN → PKR_VAR_github_npm_token so Packer can pass it
// to the Ansible provisioner as the npm registry auth token without requiring
// a separate variable to be set. GITHUB_NPM_TOKEN must have read:packages scope.
func packerEnv() []string {
	env := os.Environ()
	hasLang, hasLC, hasPKRToken := false, false, false
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "LANG="):
			hasLang = true
		case strings.HasPrefix(e, "LC_ALL="):
			hasLC = true
		case strings.HasPrefix(e, "PKR_VAR_github_npm_token="):
			hasPKRToken = true
		}
	}
	if !hasLang {
		env = append(env, "LANG=en_US.UTF-8")
	}
	if !hasLC {
		env = append(env, "LC_ALL=en_US.UTF-8")
	}
	if !hasPKRToken {
		if token := os.Getenv("GITHUB_NPM_TOKEN"); token != "" {
			env = append(env, "PKR_VAR_github_npm_token="+token)
		}
	}
	return env
}

// ManifestBuild represents a single build in the Packer manifest.
type ManifestBuild struct {
	ArtifactID    string            `json:"artifact_id"`
	BuilderType   string            `json:"builder_type"`
	Name          string            `json:"name"`
	Files         []ManifestFile    `json:"files"`
	PackerRunUUID string            `json:"packer_run_uuid"`
	BuildTime     int64             `json:"build_time"`
	CustomData    map[string]string `json:"custom_data"`
}

// ManifestFile represents a file in a manifest build.
type ManifestFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Manifest represents the structure of a Packer manifest.json file.
type Manifest struct {
	Builds      []ManifestBuild `json:"builds"`
	LastRunUUID string          `json:"last_run_uuid"`
}

// ParseManifest reads and parses a Packer manifest.json file.
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// RegionAMIs extracts a region→AMI map from the last run in the manifest.
// artifact_id format: "region:ami-id" (single region) or "region1:ami-x,region2:ami-y" (multi-region).
// Only builds matching last_run_uuid are included so stale entries from prior builds are ignored.
func RegionAMIs(m *Manifest) map[string]string {
	result := make(map[string]string)
	for _, build := range m.Builds {
		if build.ArtifactID == "" || build.PackerRunUUID != m.LastRunUUID {
			continue
		}
		for pair := range strings.SplitSeq(build.ArtifactID, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
			if len(parts) == 2 {
				region, amiID := parts[0], parts[1]
				result[region] = amiID
			}
		}
	}
	return result
}

// ParseVarsFile reads a .pkrvars.hcl file and returns a map of string values.
// Only simple string assignments of the form  key = "value"  are extracted;
// other HCL constructs are silently ignored.
func ParseVarsFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open vars file: %w", err)
	}
	defer f.Close()

	vars := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v := strings.TrimSpace(rest)
		if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
			vars[k] = v[1 : len(v)-1]
		}
	}
	return vars, scanner.Err()
}

// Init runs packer init to download required plugins.
func Init(ctx context.Context, packerDir string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "packer", "init", ".")
	cmd.Dir = packerDir
	cmd.Env = packerEnv()
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("packer init failed: %w", err)
	}
	return nil
}

// Run executes packer build in the given directory.
// baseAMI is always required; the caller must resolve it before invoking Run.
// cliVersion is the ai-desktops CLI version baked into the AMI tag.
// desktopWebVersion is the @markcallen/desktop-web npm package version to install.
// novncDesktopVersion is the version string extracted from the base novnc-desktop AMI name.
// subnetID is the subnet for the Packer build instance; pass "" to rely on the default VPC.
// public controls whether the built AMI has public launch permissions.
func Run(ctx context.Context, packerDir string, varsFile string, region string, baseAMI string, cliVersion string, desktopWebVersion string, novncDesktopVersion string, subnetID string, public bool, w io.Writer) error {
	args := []string{"build"}
	if varsFile != "" {
		args = append(args, "-var-file="+varsFile)
	}
	if region != "" {
		args = append(args, "-var", "aws_region="+region)
	}
	// Always override source_ami via -var so the vars file value cannot
	// accidentally trigger a stale lookup.
	args = append(args, "-var", "source_ami="+baseAMI)
	args = append(args, "-var", "ai_desktops_version="+cliVersion)
	args = append(args, "-var", "desktop_web_version="+desktopWebVersion)
	args = append(args, "-var", "novnc_desktop_version="+novncDesktopVersion)
	if subnetID != "" {
		args = append(args, "-var", "subnet_id="+subnetID)
	}
	if public {
		args = append(args, "-var", "ami_public=true")
	}
	args = append(args, ".")

	cmd := exec.CommandContext(ctx, "packer", args...)
	cmd.Dir = packerDir
	cmd.Env = packerEnv()

	logPath := PackerBuildLogPath(packerDir, region, time.Now().UTC())
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return fmt.Errorf("create packer log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create packer log %s: %w", logPath, err)
	}
	defer logFile.Close()

	fmt.Fprintf(w, "Packer build log: %s\n", logPath)
	out := io.MultiWriter(w, logFile)
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("packer build failed: %w", err)
	}
	return nil
}

// PackerBuildLogPath returns the per-run log file used to persist Packer and
// Ansible output, including profile_tasks timing summaries.
func PackerBuildLogPath(packerDir, region string, t time.Time) string {
	safeRegion := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(region)
	if safeRegion == "" {
		safeRegion = "default"
	}
	name := fmt.Sprintf("packer-%s-%s.log", safeRegion, t.UTC().Format("20060102-150405"))
	return filepath.Join(packerDir, "build-logs", name)
}
