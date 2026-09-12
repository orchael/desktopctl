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
	}
	for _, want := range requiredBlocks {
		if !strings.Contains(contents, want) {
			t.Errorf("AMI playbook does not contain required task:\n%s", want)
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
