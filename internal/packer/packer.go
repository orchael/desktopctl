package packer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

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

// localeEnv returns a copy of os.Environ() with LANG and LC_ALL forced to
// en_US.UTF-8.  Some systems (e.g. minimal Docker images) export LANG=C.UTF-8
// which Python / Ansible reject at startup.
func localeEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+2)
	for _, e := range env {
		if strings.HasPrefix(e, "LANG=") || strings.HasPrefix(e, "LC_ALL=") {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	return out
}

// Init runs packer init to download required plugins.
func Init(ctx context.Context, packerDir string, w io.Writer) error {
	cmd := exec.CommandContext(ctx, "packer", "init", ".")
	cmd.Dir = packerDir
	cmd.Env = localeEnv()
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
// public controls whether the built AMI has public launch permissions.
func Run(ctx context.Context, packerDir string, varsFile string, region string, baseAMI string, public bool, w io.Writer) error {
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
	if public {
		args = append(args, "-var", "ami_public=true")
	}
	args = append(args, ".")

	cmd := exec.CommandContext(ctx, "packer", args...)
	cmd.Dir = packerDir
	cmd.Env = localeEnv()
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("packer build failed: %w", err)
	}
	return nil
}
