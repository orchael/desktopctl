package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/spf13/cobra"
)

// toolRequirement describes a binary that must be on PATH, with an optional
// minimum version and how to extract that version from the binary's output.
type toolRequirement struct {
	name       string
	minVersion string // semver-ish prefix, e.g. "1.9" or "2."
	versionCmd []string
	versionFn  func(out string) string // extracts version string from command output
	purpose    string
}

var checkTools = []toolRequirement{
	{
		name:       "aws",
		minVersion: "2.",
		versionCmd: []string{"aws", "--version"},
		versionFn:  func(out string) string { return extractField(out, "aws-cli/", " ") },
		purpose:    "AWS operations (AMI management, SSM, Secrets Manager)",
	},
	{
		name:       "packer",
		minVersion: "1.9",
		versionCmd: []string{"packer", "version"},
		versionFn:  func(out string) string { return extractField(out, "Packer v", "\n") },
		purpose:    "AMI builds (ami build)",
	},
	{
		name:       "pulumi",
		minVersion: "3.",
		versionCmd: []string{"pulumi", "version"},
		versionFn:  func(out string) string { return strings.TrimPrefix(strings.TrimSpace(out), "v") },
		purpose:    "Desktop provisioning (create, terminate, init-foundation)",
	},
	{
		name:       "ssh",
		minVersion: "",
		versionCmd: []string{"ssh", "-V"},
		versionFn:  func(out string) string { return extractField(out, "OpenSSH_", ",") },
		purpose:    "Desktop SSH sessions",
	},
}

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check that all required tools are installed and at minimum versions",
	Long: `check verifies that the tools needed to operate ai-desktops are present
on PATH and meet minimum version requirements. It also verifies that AWS
credentials are functional.

Tools checked:
  aws     >= 2.x   — AWS operations, AMI management, SSM, Secrets Manager
  packer  >= 1.9   — AMI builds (ami build command)
  pulumi  >= 3.x   — Desktop provisioning (create, terminate, init-foundation)
  ssh     any      — Desktop SSH sessions

A missing or outdated tool is reported as FAIL; a tool whose version cannot
be parsed is reported as WARN. AWS credential errors are reported as FAIL.`,
	RunE: runCheck,
}

func init() {
	rootCmd.AddCommand(checkCmd)
}

type checkResult struct {
	name    string
	status  string // OK, WARN, FAIL
	version string
	message string
	purpose string
}

func runCheck(_ *cobra.Command, _ []string) error {
	var results []checkResult
	allOK := true

	for _, req := range checkTools {
		r := checkTool(req)
		results = append(results, r)
		if r.status == "FAIL" {
			allOK = false
		}
	}

	// AWS credential check
	results = append(results, checkAWSCredentials())
	if results[len(results)-1].status == "FAIL" {
		allOK = false
	}

	if jsonOut {
		return printCheckJSON(results)
	}
	printCheckTable(results)

	if !allOK {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func checkTool(req toolRequirement) checkResult {
	r := checkResult{name: req.name, purpose: req.purpose}

	path, err := exec.LookPath(req.name)
	if err != nil {
		r.status = "FAIL"
		r.message = "not found in PATH"
		return r
	}
	_ = path

	// Run version command (stderr is often where version appears, e.g. ssh -V)
	cmd := exec.Command(req.versionCmd[0], req.versionCmd[1:]...) //nolint:gosec
	out, _ := cmd.CombinedOutput()
	ver := req.versionFn(string(out))
	r.version = ver

	if req.minVersion == "" {
		r.status = "OK"
		return r
	}

	if ver == "" {
		r.status = "WARN"
		r.message = fmt.Sprintf("could not parse version (raw: %s)", strings.TrimSpace(string(out)))
		return r
	}

	if !versionAtLeast(ver, req.minVersion) {
		r.status = "FAIL"
		r.message = fmt.Sprintf("version %s is below minimum %s", ver, req.minVersion)
		return r
	}

	r.status = "OK"
	return r
}

func checkAWSCredentials() checkResult {
	r := checkResult{name: "aws-credentials", purpose: "authenticate to AWS"}

	ctx := context.Background()
	awsRegion := "us-east-1"
	if cfg.AWS.Region != "" {
		awsRegion = cfg.AWS.Region
	}

	awsCfg, err := awsx.LoadConfig(ctx, awsRegion, cfg.AWS.Profile)
	if err != nil {
		r.status = "FAIL"
		r.message = fmt.Sprintf("could not load AWS config: %v", err)
		return r
	}

	identity, err := awsx.GetCallerIdentity(ctx, awsCfg)
	if err != nil {
		r.status = "FAIL"
		r.message = fmt.Sprintf("AWS credentials invalid or expired: %v", err)
		return r
	}

	r.status = "OK"
	r.version = identity
	return r
}

func printCheckTable(results []checkResult) {
	fmt.Fprintf(os.Stdout, "%-20s %-6s %-25s %s\n", "TOOL", "STATUS", "VERSION", "PURPOSE")
	fmt.Fprintf(os.Stdout, "%s\n", strings.Repeat("-", 80))
	for _, r := range results {
		mark := "✓"
		if r.status == "FAIL" {
			mark = "✗"
		} else if r.status == "WARN" {
			mark = "!"
		}
		ver := r.version
		if ver == "" {
			ver = "-"
		}
		fmt.Fprintf(os.Stdout, "%-20s %s %-4s %-25s %s\n", r.name, mark, r.status, ver, r.purpose)
		if r.message != "" {
			fmt.Fprintf(os.Stdout, "  → %s\n", r.message)
		}
	}
}

func printCheckJSON(results []checkResult) error {
	fmt.Fprintln(os.Stdout, "[")
	for i, r := range results {
		comma := ","
		if i == len(results)-1 {
			comma = ""
		}
		fmt.Fprintf(os.Stdout, "  {\"name\":%q,\"status\":%q,\"version\":%q,\"message\":%q}%s\n",
			r.name, r.status, r.version, r.message, comma)
	}
	fmt.Fprintln(os.Stdout, "]")
	return nil
}

// extractField returns the substring after prefix and before the first
// occurrence of suffix. Returns "" if prefix is not found.
func extractField(s, prefix, suffix string) string {
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(prefix):]
	if suffix == "" {
		return strings.TrimSpace(rest)
	}
	end := strings.Index(rest, suffix)
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// versionAtLeast returns true when ver >= min, comparing dot-separated numeric
// components. Non-numeric components fall back to string comparison.
func versionAtLeast(ver, min string) bool {
	vp := strings.SplitN(ver, ".", 4)
	mp := strings.SplitN(min, ".", 4)
	for i := 0; i < len(mp) && i < len(vp); i++ {
		v, verr := strconv.Atoi(strings.TrimRight(vp[i], "abcdefghijklmnopqrstuvwxyz-+~"))
		m, merr := strconv.Atoi(strings.TrimRight(mp[i], "abcdefghijklmnopqrstuvwxyz-+~"))
		if verr != nil || merr != nil {
			// fall back to string comparison for this component
			if vp[i] > mp[i] {
				return true
			}
			if vp[i] < mp[i] {
				return false
			}
			continue
		}
		if v > m {
			return true
		}
		if v < m {
			return false
		}
	}
	return len(vp) >= len(mp)
}
