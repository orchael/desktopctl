package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

const (
	legacyAgentMarker   = "# --- retrieve AI provider API keys and write bridgectl agents.env ---"
	legacyDesktopMarker = "# --- retrieve desktop secrets and inject into ubuntu environment ---"
	legacyInsertMarker  = "# --- suppress Claude Code first-run onboarding (blocks non-interactive use) ---"
	maxLegacyCloudInit  = 1024 * 1024
)

var legacyAgentPathPattern = regexp.MustCompile(`(?m)^    AGENT_SECRET="([^"]+)"$`)

func decodedUserData(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode saved user data: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("decompress saved user data: %w", err)
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, maxLegacyCloudInit+1))
	if err != nil {
		return "", fmt.Errorf("read saved user data: %w", err)
	}
	if len(data) > maxLegacyCloudInit {
		return "", fmt.Errorf("saved user data exceeds %d bytes", maxLegacyCloudInit)
	}
	return string(data), nil
}

// legacySection finds one complete cloud-init runcmd section. A legacy format
// is accepted only when the known markers and YAML list boundaries are unique.
func legacySection(cloud, marker string) (int, int, error) {
	if strings.Count(cloud, marker) != 1 {
		return 0, 0, fmt.Errorf("cloud-init does not contain exactly one %q section", marker)
	}
	markerAt := strings.Index(cloud, marker)
	start := strings.LastIndex(cloud[:markerAt], "\n") + 1
	if cloud[start:markerAt] != "  " {
		return 0, 0, fmt.Errorf("cloud-init %q section is not a runcmd comment", marker)
	}
	seenListItem := false
	for lineStart := strings.IndexByte(cloud[markerAt:], '\n') + markerAt + 1; lineStart > markerAt && lineStart < len(cloud); {
		lineEnd := strings.IndexByte(cloud[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(cloud)
		} else {
			lineEnd += lineStart
		}
		line := cloud[lineStart:lineEnd]
		if strings.HasPrefix(line, "  - ") {
			if seenListItem {
				return start, lineStart, nil
			}
			seenListItem = true
		} else if seenListItem && (strings.HasPrefix(line, "  # ---") || (line != "" && line[0] != ' ' && line[0] != '\t')) {
			return start, lineStart, nil
		}
		if lineEnd == len(cloud) {
			break
		}
		lineStart = lineEnd + 1
	}
	if !seenListItem {
		return 0, 0, fmt.Errorf("cloud-init %q section has no runcmd item", marker)
	}
	return start, len(cloud), nil
}

func replaceLegacySection(cloud, generated, marker string) (string, error) {
	count := strings.Count(cloud, marker)
	if count > 1 {
		return "", fmt.Errorf("cloud-init contains duplicate %q sections", marker)
	}
	replacement := ""
	if strings.Contains(generated, marker) {
		newStart, newEnd, err := legacySection(generated, marker)
		if err != nil {
			return "", err
		}
		replacement = generated[newStart:newEnd]
	}
	if count == 0 {
		if replacement == "" {
			return cloud, nil
		}
		if strings.Count(cloud, legacyInsertMarker) != 1 {
			return "", fmt.Errorf("cloud-init has no unambiguous secret insertion point")
		}
		at := strings.Index(cloud, legacyInsertMarker)
		start := strings.LastIndex(cloud[:at], "\n") + 1
		if cloud[start:at] != "  " {
			return "", fmt.Errorf("cloud-init secret insertion point is not a runcmd comment")
		}
		return cloud[:start] + replacement + cloud[start:], nil
	}
	start, end, err := legacySection(cloud, marker)
	if err != nil {
		return "", err
	}
	return cloud[:start] + replacement + cloud[end:], nil
}

func legacyRestoreConfig(d *store.Desktop) (pulumi.StackConfig, error) {
	config := d.RestoreSpec.Config
	cloud, err := decodedUserData(config["userDataBase64"])
	if err != nil {
		return nil, err
	}
	if strings.Count(cloud, legacyInsertMarker) != 1 {
		return nil, fmt.Errorf("cloud-init has no unambiguous secret insertion point")
	}
	oldDesktopStart, oldDesktopEnd := 0, 0
	if strings.Contains(cloud, legacyDesktopMarker) {
		oldDesktopStart, oldDesktopEnd, err = legacySection(cloud, legacyDesktopMarker)
		if err != nil {
			return nil, err
		}
	}
	agentMatches := legacyAgentPathPattern.FindStringSubmatch(cloud)
	if strings.Contains(cloud, legacyAgentMarker) && len(agentMatches) != 2 {
		return nil, fmt.Errorf("desktop %s has no unambiguous saved agent secret path", d.DesktopID)
	}
	agentPath := ""
	if strings.Contains(cloud, legacyAgentMarker) && len(agentMatches) == 2 {
		agentPath = agentMatches[1]
	}
	if agentPath == "" && cfg != nil && containsString(d.Secrets, cfg.GitHub.AgentSecret) {
		agentPath = cfg.GitHub.AgentSecret
	}
	if !containsString(d.Secrets, agentPath) {
		agentPath = ""
	}
	profilePath := ""
	for _, path := range d.Secrets {
		if strings.Contains(cloud[oldDesktopStart:oldDesktopEnd], "could not retrieve desktop secret "+path+"\" >&2\n      exit 1") {
			profilePath = path
			break
		}
	}
	boot := &provision.BootstrapConfig{DesktopID: d.DesktopID, AWSRegion: d.Region, AgentSecretPath: agentPath, DesktopSecretPaths: runtimeDesktopSecretPaths(agentPath, d.Secrets), ProfileSecretPath: profilePath}
	generated, err := provision.RenderCloudInit(boot)
	if err != nil {
		return nil, fmt.Errorf("render legacy secret sections: %w", err)
	}
	cloud, err = replaceLegacySection(cloud, generated, legacyDesktopMarker)
	if err != nil {
		return nil, err
	}
	cloud, err = replaceLegacySection(cloud, generated, legacyAgentMarker)
	if err != nil {
		return nil, err
	}
	compressed, err := gzipBase64UserData(cloud)
	if err != nil {
		return nil, err
	}
	result := make(pulumi.StackConfig, len(config))
	for key, value := range config {
		result[key] = value
	}
	result["userDataBase64"] = compressed
	return result, nil
}
