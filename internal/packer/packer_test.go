package packer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseManifest_singleRegion(t *testing.T) {
	manifest := Manifest{
		Builds: []ManifestBuild{
			{
				ArtifactID:  "us-east-2:ami-0abc123456def7890",
				BuilderType: "amazon-ebs",
				Name:        "amazon-ebs.ubuntu",
			},
		},
	}

	// Write to temp file
	data, _ := json.Marshal(manifest)
	tmpfile, _ := os.CreateTemp("", "manifest.json")
	defer os.Remove(tmpfile.Name())
	tmpfile.Write(data)
	tmpfile.Close()

	// Parse
	parsed, err := ParseManifest(tmpfile.Name())
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}

	if len(parsed.Builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(parsed.Builds))
	}

	if parsed.Builds[0].ArtifactID != "us-east-2:ami-0abc123456def7890" {
		t.Errorf("artifact ID mismatch: %q", parsed.Builds[0].ArtifactID)
	}
}

func TestRegionAMIs_singleRegion(t *testing.T) {
	manifest := &Manifest{
		Builds: []ManifestBuild{
			{
				ArtifactID: "us-east-2:ami-0abc123456def7890",
			},
		},
	}

	result := RegionAMIs(manifest)
	if len(result) != 1 {
		t.Fatalf("expected 1 region, got %d", len(result))
	}

	if amiID, ok := result["us-east-2"]; !ok {
		t.Error("us-east-2 not found in result")
	} else if amiID != "ami-0abc123456def7890" {
		t.Errorf("AMI ID mismatch: expected ami-0abc123456def7890, got %q", amiID)
	}
}

func TestRegionAMIs_multiRegion(t *testing.T) {
	manifest := &Manifest{
		Builds: []ManifestBuild{
			{
				ArtifactID: "us-east-1:ami-xxxx,us-west-2:ami-yyyy",
			},
		},
	}

	result := RegionAMIs(manifest)
	if len(result) != 2 {
		t.Fatalf("expected 2 regions, got %d", len(result))
	}

	tests := map[string]string{
		"us-east-1": "ami-xxxx",
		"us-west-2": "ami-yyyy",
	}

	for region, expected := range tests {
		if amiID, ok := result[region]; !ok {
			t.Errorf("%s not found in result", region)
		} else if amiID != expected {
			t.Errorf("AMI ID mismatch for %s: expected %q, got %q", region, expected, amiID)
		}
	}
}

func TestRegionAMIs_empty(t *testing.T) {
	manifest := &Manifest{Builds: []ManifestBuild{}}
	result := RegionAMIs(manifest)
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d regions", len(result))
	}
}

func TestParseManifest_invalidJSON(t *testing.T) {
	tmpfile, _ := os.CreateTemp("", "manifest.json")
	defer os.Remove(tmpfile.Name())
	tmpfile.WriteString("{ invalid json")
	tmpfile.Close()

	_, err := ParseManifest(tmpfile.Name())
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseManifest_fileNotFound(t *testing.T) {
	_, err := ParseManifest(filepath.Join(os.TempDir(), "nonexistent-manifest.json"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}
