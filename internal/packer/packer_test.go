package packer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestRegionAMIs_UUIDFilter_OnlyMatchingBuildReturned(t *testing.T) {
	const lastUUID = "uuid-last-run"
	const otherUUID = "uuid-old-run"

	manifest := &Manifest{
		LastRunUUID: lastUUID,
		Builds: []ManifestBuild{
			{
				ArtifactID:    "us-east-1:ami-new",
				PackerRunUUID: lastUUID,
			},
			{
				ArtifactID:    "us-east-1:ami-old",
				PackerRunUUID: otherUUID,
			},
		},
	}

	result := RegionAMIs(manifest)
	if len(result) != 1 {
		t.Fatalf("expected 1 region, got %d", len(result))
	}
	amiID, ok := result["us-east-1"]
	if !ok {
		t.Fatal("us-east-1 not found in result")
	}
	if amiID != "ami-new" {
		t.Errorf("expected ami-new (from last run), got %q", amiID)
	}
}

func TestRegionAMIs_UUIDFilter_NoBuildMatchesLastRunUUID(t *testing.T) {
	const lastUUID = "uuid-last-run"

	manifest := &Manifest{
		LastRunUUID: lastUUID,
		Builds: []ManifestBuild{
			{
				ArtifactID:    "us-east-1:ami-old",
				PackerRunUUID: "uuid-some-other-run",
			},
			{
				ArtifactID:    "us-west-2:ami-also-old",
				PackerRunUUID: "uuid-another-old-run",
			},
		},
	}

	result := RegionAMIs(manifest)
	if len(result) != 0 {
		t.Errorf("expected empty map when no build matches LastRunUUID, got %d entries", len(result))
	}
}

func TestRegionAMIs_UUIDFilter_EmptyArtifactIDSkipped(t *testing.T) {
	const lastUUID = "uuid-last-run"

	manifest := &Manifest{
		LastRunUUID: lastUUID,
		Builds: []ManifestBuild{
			{
				ArtifactID:    "",
				PackerRunUUID: lastUUID,
			},
		},
	}

	result := RegionAMIs(manifest)
	if len(result) != 0 {
		t.Errorf("expected empty map when ArtifactID is empty, got %d entries", len(result))
	}
}

// writeFakePacker creates a shell script named "packer" in dir that exits with exitCode.
func writeFakePacker(t *testing.T, dir string, exitCode int) {
	t.Helper()
	path := filepath.Join(dir, "packer")
	content := fmt.Sprintf("#!/bin/sh\nexit %d\n", exitCode)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatalf("writeFakePacker: %v", err)
	}
}

func TestParseVarsFile_BasicParsing(t *testing.T) {
	f, err := os.CreateTemp("", "*.pkrvars.hcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("aws_region = \"us-east-1\"\nsource_ami = \"ami-12345678\"\n")
	f.Close()

	vars, err := ParseVarsFile(f.Name())
	if err != nil {
		t.Fatalf("ParseVarsFile: %v", err)
	}
	if vars["aws_region"] != "us-east-1" {
		t.Errorf("aws_region: got %q", vars["aws_region"])
	}
	if vars["source_ami"] != "ami-12345678" {
		t.Errorf("source_ami: got %q", vars["source_ami"])
	}
}

func TestParseVarsFile_IgnoresCommentsAndBlanks(t *testing.T) {
	f, err := os.CreateTemp("", "*.pkrvars.hcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("# this is a comment\n\nname = \"my-ami\"\n")
	f.Close()

	vars, err := ParseVarsFile(f.Name())
	if err != nil {
		t.Fatalf("ParseVarsFile: %v", err)
	}
	if len(vars) != 1 {
		t.Errorf("expected 1 var, got %d", len(vars))
	}
	if vars["name"] != "my-ami" {
		t.Errorf("name: got %q", vars["name"])
	}
}

func TestParseVarsFile_IgnoresNonStringValues(t *testing.T) {
	f, err := os.CreateTemp("", "*.pkrvars.hcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("count = 3\nenabled = true\nname = \"ami\"\n")
	f.Close()

	vars, err := ParseVarsFile(f.Name())
	if err != nil {
		t.Fatalf("ParseVarsFile: %v", err)
	}
	if _, ok := vars["count"]; ok {
		t.Error("count should not be parsed (not a quoted string)")
	}
	if _, ok := vars["enabled"]; ok {
		t.Error("enabled should not be parsed (not a quoted string)")
	}
	if vars["name"] != "ami" {
		t.Errorf("name: got %q", vars["name"])
	}
}

func TestParseVarsFile_MissingFile(t *testing.T) {
	_, err := ParseVarsFile(filepath.Join(os.TempDir(), "nonexistent-vars.pkrvars.hcl"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseVarsFile_EmptyFile(t *testing.T) {
	f, err := os.CreateTemp("", "*.pkrvars.hcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()

	vars, err := ParseVarsFile(f.Name())
	if err != nil {
		t.Fatalf("ParseVarsFile: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("expected empty map, got %d vars", len(vars))
	}
}

func TestParseVarsFile_IgnoresLineWithoutEquals(t *testing.T) {
	f, err := os.CreateTemp("", "*.pkrvars.hcl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("justakeynovalue\nname = \"ok\"\n")
	f.Close()

	vars, err := ParseVarsFile(f.Name())
	if err != nil {
		t.Fatalf("ParseVarsFile: %v", err)
	}
	if _, ok := vars["justakeynovalue"]; ok {
		t.Error("line without '=' should be ignored")
	}
	if vars["name"] != "ok" {
		t.Errorf("name: got %q", vars["name"])
	}
}

func TestInit_Success(t *testing.T) {
	binDir := t.TempDir()
	writeFakePacker(t, binDir, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var buf bytes.Buffer
	if err := Init(context.Background(), t.TempDir(), &buf); err != nil {
		t.Fatalf("Init: %v", err)
	}
}

func TestInit_Failure(t *testing.T) {
	binDir := t.TempDir()
	writeFakePacker(t, binDir, 1)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var buf bytes.Buffer
	err := Init(context.Background(), t.TempDir(), &buf)
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "packer init failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_Success(t *testing.T) {
	binDir := t.TempDir()
	writeFakePacker(t, binDir, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var buf bytes.Buffer
	if err := Run(context.Background(), t.TempDir(), "", "us-east-1", false, &buf); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_Failure(t *testing.T) {
	binDir := t.TempDir()
	writeFakePacker(t, binDir, 1)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var buf bytes.Buffer
	err := Run(context.Background(), t.TempDir(), "", "us-east-1", false, &buf)
	if err == nil {
		t.Fatal("expected error from Run")
	}
	if !strings.Contains(err.Error(), "packer build failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRun_WithVarsFileAndPublic(t *testing.T) {
	binDir := t.TempDir()
	writeFakePacker(t, binDir, 0)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	varsFile := filepath.Join(workDir, "test.pkrvars.hcl")
	if err := os.WriteFile(varsFile, []byte("name = \"test\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Run(context.Background(), workDir, varsFile, "us-west-2", true, &buf); err != nil {
		t.Fatalf("Run with vars and public: %v", err)
	}
}
