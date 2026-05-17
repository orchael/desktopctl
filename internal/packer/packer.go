package packer

import (
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

// RegionAMIs extracts a region→AMI map from the manifest.
// artifact_id format: "region:ami-id" (single region) or "region1:ami-x,region2:ami-y" (multi-region).
func RegionAMIs(m *Manifest) map[string]string {
	result := make(map[string]string)
	for _, build := range m.Builds {
		if build.ArtifactID == "" {
			continue
		}
		// Parse comma-separated regions:ami pairs
		for _, pair := range strings.Split(build.ArtifactID, ",") {
			parts := strings.SplitN(strings.TrimSpace(pair), ":", 2)
			if len(parts) == 2 {
				region, amiID := parts[0], parts[1]
				result[region] = amiID
			}
		}
	}
	return result
}

// Run executes packer build in the given directory.
// It accepts the packer directory, optional vars file (absolute path), and a list of regions to target.
// Output is streamed to the provided writer.
func Run(ctx context.Context, packerDir string, varsFile string, w io.Writer) error {
	// Check that packer binary exists
	if _, err := exec.LookPath("packer"); err != nil {
		return fmt.Errorf("packer not found in PATH: %w", err)
	}

	// Build packer command
	// Note: varsFile should be an absolute path or relative to packerDir
	args := []string{"build"}
	if varsFile != "" {
		// If varsFile is absolute, use it as-is; otherwise it will be resolved relative to packerDir
		args = append(args, "-var-file="+varsFile)
	}
	args = append(args, ".")

	cmd := exec.CommandContext(ctx, "packer", args...)
	cmd.Dir = packerDir
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("packer build failed: %w", err)
	}
	return nil
}
