package packer

import (
	"os"
	"strings"
	"testing"
)

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
