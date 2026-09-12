package packer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryPinsGo126Everywhere(t *testing.T) {
	repoRoot := filepath.Clean("..")
	for _, name := range []string{
		"go.mod",
		"infra/pulumi/control-plane-access/go.mod",
		"infra/pulumi/desktop/go.mod",
		"infra/pulumi/foundation/go.mod",
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(contents), "\ngo 1.26.0\n") {
			t.Errorf("%s must require Go 1.26.0", name)
		}
	}

	for _, name := range []string{
		".github/workflows/ci.yml",
		".github/workflows/lint.yml",
		".github/workflows/publish-cli.yml",
	} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if !strings.Contains(string(contents), "go-version-file: go.mod") {
			t.Errorf("%s must derive the Go version from the root module", name)
		}
	}

	assertFileContains(t, filepath.Join(repoRoot, "Dockerfile.control-plane"), "FROM golang:1.26-bookworm AS api-build")
	assertFileContains(t, "variables.pkrvars.hcl", `go_version                    = "1.26.0"`)
	for _, want := range []string{
		`variable "go_version"`,
		`GoVersion                  = var.go_version`,
		`go_version=${var.go_version}`,
		`go_version                    = var.go_version`,
	} {
		assertFileContains(t, "ubuntu-desktop.pkr.hcl", want)
	}
	for _, want := range []string{
		"    - name: Remove any existing Go installation",
		"        path: /usr/local/go",
		"        state: absent",
		"    - name: Require the pinned Go version",
		`        that: go_ver_out.stdout == 'go version go' ~ go_version ~ ' linux/amd64'`,
	} {
		assertFileContains(t, "playbook.yml", want)
	}
}

func assertFileContains(t *testing.T, name, want string) {
	t.Helper()
	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !strings.Contains(string(contents), want) {
		t.Errorf("%s must contain %q", name, want)
	}
}

func TestAMIPlaybookInstallsHelmWithHomebrew(t *testing.T) {
	playbook, err := os.ReadFile("playbook.yml")
	if err != nil {
		t.Fatalf("read AMI playbook: %v", err)
	}

	contents := string(playbook)
	requiredBlocks := []string{
		`    - name: Require helm_version
      ansible.builtin.assert:
        that: helm_version is defined and helm_version | length > 0`,
		`    - name: Install Helm via Homebrew
      ansible.builtin.command: /home/linuxbrew/.linuxbrew/bin/brew install helm
      args:
        creates: /home/linuxbrew/.linuxbrew/bin/helm
      become: false`,
		`    - name: Verify Helm is installed
      ansible.builtin.command: /home/linuxbrew/.linuxbrew/bin/helm version --short
      register: helm_ver_out
      changed_when: false
      become: false`,
		`    - name: Require the pinned Helm version
      ansible.builtin.assert:
        that: (helm_ver_out.stdout | regex_replace('[+].*$', '')) == helm_version`,
	}
	for _, want := range requiredBlocks {
		if !strings.Contains(contents, want) {
			t.Errorf("AMI playbook does not contain required task:\n%s", want)
		}
	}
}

func TestAMIConfigurationPinsHelmVersion(t *testing.T) {
	vars, err := os.ReadFile("variables.pkrvars.hcl")
	if err != nil {
		t.Fatalf("read Packer variables: %v", err)
	}
	template, err := os.ReadFile("ubuntu-desktop.pkr.hcl")
	if err != nil {
		t.Fatalf("read Packer template: %v", err)
	}

	if !strings.Contains(string(vars), `helm_version                  = "v4.3.0"`) {
		t.Fatal("Packer variables must pin Helm v4.3.0")
	}
	for _, want := range []string{
		`variable "helm_version"`,
		`HelmVersion                = var.helm_version`,
		`helm_version=${var.helm_version}`,
		`helm_version                  = var.helm_version`,
	} {
		if !strings.Contains(string(template), want) {
			t.Errorf("Packer template does not propagate Helm pin %q", want)
		}
	}
}

func TestAMIPlaybookCreatesPrivateCodexHome(t *testing.T) {
	playbook, err := os.ReadFile("playbook.yml")
	if err != nil {
		t.Fatalf("read AMI playbook: %v", err)
	}

	want := `    - name: Create private native Codex home for ubuntu
      ansible.builtin.file:
        path: /home/ubuntu/.codex
        state: directory
        owner: ubuntu
        group: ubuntu
        mode: "0700"`
	if !strings.Contains(string(playbook), want) {
		t.Fatalf("AMI playbook does not create the native Codex home privately:\n%s", want)
	}
}
