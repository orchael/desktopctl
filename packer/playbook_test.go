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

func TestAMIPlaybookInstallsGoogleCloudCLI(t *testing.T) {
	for _, want := range []string{
		"    - name: Download Google Cloud apt signing key",
		"url: https://packages.cloud.google.com/apt/doc/apt-key.gpg",
		"dest: /etc/apt/keyrings/cloud.google.asc",
		"deb [signed-by=/etc/apt/keyrings/cloud.google.asc] https://packages.cloud.google.com/apt cloud-sdk main",
		"    - name: Install Google Cloud CLI",
		"        name: google-cloud-cli",
		"    - name: Verify Google Cloud CLI is installed",
		"ansible.builtin.command: gcloud version",
	} {
		assertFileContains(t, "playbook.yml", want)
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

func TestAMIPlaybookInstallsPinnedVSCode(t *testing.T) {
	for _, want := range []string{
		`    - name: Require vscode_version`,
		`    - name: Require vscode_sha256`,
		`url: https://packages.microsoft.com/keys/microsoft.asc`,
		`URIs: https://packages.microsoft.com/repos/code`,
		`Signed-By: /etc/apt/keyrings/microsoft.asc`,
		`url: "https://packages.microsoft.com/repos/code/pool/main/c/code/code_{{ vscode_version }}_amd64.deb"`,
		`checksum: "sha256:{{ vscode_sha256 }}"`,
		`deb: /opt/ai-desktops/vscode.deb`,
		`ansible.builtin.command: dpkg-query -W -f='${Version}' code`,
		`vscode_installed_version.stdout == vscode_version`,
		`path: /usr/share/applications/com.microsoft.VSCode.desktop`,
		`ansible.builtin.command: desktop-file-validate /usr/share/applications/com.microsoft.VSCode.desktop`,
		`ansible.builtin.command: /usr/bin/code --version`,
	} {
		assertFileContains(t, "playbook.yml", want)
	}
	assertFileContains(t, "variables.pkrvars.hcl", `vscode_version                = "1.139.1-1790309529"`)
	assertFileContains(t, "variables.pkrvars.hcl", `vscode_sha256                 = "cc8e35cf69ff4c7e515e19fa981bf6aba41f61ddb61c79370e9fe460c5dbaf8b"`)
	for _, want := range []string{
		`variable "vscode_version"`,
		`variable "vscode_sha256"`,
		`VSCodeVersion              = var.vscode_version`,
		`vscode_version=${var.vscode_version}`,
		`vscode_sha256=${var.vscode_sha256}`,
	} {
		assertFileContains(t, "ubuntu-desktop.pkr.hcl", want)
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

func TestAMIPlaybookConfiguresCodexUpdateCheck(t *testing.T) {
	for _, want := range []string{
		"../internal/provision/codex_home_config.py",
		"/home/ubuntu/.config/bridgectl/codex-home/config.toml",
		"Configure bridgectl Codex home update setting",
	} {
		assertFileContains(t, "playbook.yml", want)
	}
}

func TestAMIPlaybookIncludesPinnedDeveloperTerminalRole(t *testing.T) {
	assertFileContains(t, "playbook.yml", `    - name: Configure pinned developer terminal workflow
      ansible.builtin.include_role:
        name: developer_terminal`)

	for _, want := range []string{
		`developer_terminal_nvchad_starter_revision: "e3572e1f5e1c297212c3deeb17b7863139ce663e"`,
		`developer_terminal_tmux_plugin_manager_revision: "e261deb1b47614eed3400089ce7197dc68acc4eb"`,
		`developer_terminal_catppuccin_tmux_revision: "d2d25bd3393fe43f19eb4fff6cdd2bdf5578e622"`,
		`developer_terminal_gitmux_version: "v0.11.5"`,
		`developer_terminal_gitmux_linux_amd64_checksum: "sha256:d46a10f5fe07ab5b8a902ac29c937e4d3c8d7f33ea30fa335d682601697b5a71"`,
	} {
		assertFileContains(t, filepath.Join("roles", "developer_terminal", "defaults", "main.yml"), want)
	}

	for name, wants := range map[string][]string{
		"chadrc.lua.j2": {`theme = "catppuccin"`},
		"tmux.conf.j2": {
			"set-option -g prefix C-a", "bind | split-window -h", "bind - split-window -v",
			"set-option -g automatic-rename off", "# set -g mouse on",
			"set -g @continuum-restore 'on'", "@catppuccin_window_status_style \"rounded\"",
			"catppuccin_status_application", "catppuccin_status_session", "catppuccin_status_cpu",
			"command -v gitmux", `"$HOME/.config/gitmux/gitmux.conf" #{q:pane_current_path}`,
			"run -b '~/.tmux/plugins/tpm/tpm'",
		},
		"gitmux.conf.j2": {"layout: [branch, remote-branch, divergence, \" - \", flags]"},
	} {
		for _, want := range wants {
			assertFileContains(t, filepath.Join("roles", "developer_terminal", "templates", name), want)
		}
	}

	tasksPath := filepath.Join("roles", "developer_terminal", "tasks", "main.yml")
	for _, want := range []string{
		"Refuse to replace an unmanaged Neovim configuration",
		"Refuse to replace an unmanaged tmux configuration",
		"Install pinned tmux plugins",
		"Verify NvChad starts without interaction",
		"Verify tmux starts in detached mode",
		"developer_terminal_gitmux_version_output.stdout | trim\n    != (developer_terminal_gitmux_version | regex_replace('^v', ''))",
		"Mark Neovim configuration as managed before installation",
		"Mark tmux configuration as managed before installation",
		"not (developer_terminal_nvim_config.stat.islnk | default(false))\n        and (",
		"not (developer_terminal_tmux_config.stat.islnk | default(false))\n        and (",
	} {
		assertFileContains(t, tasksPath, want)
	}

	assertFileContains(t, "playbook.yml", `    - name: Configure GitHub CLI editor for ubuntu
      ansible.builtin.command: /usr/bin/gh config set editor vim`)
}

func TestAMIPlaybookTrustsBallastTapBeforeLoadingIt(t *testing.T) {
	playbook, err := os.ReadFile("playbook.yml")
	if err != nil {
		t.Fatalf("read AMI playbook: %v", err)
	}

	contents := string(playbook)
	trust := strings.Index(contents, "brew trust --tap everydaydevopsio/ballast")
	tap := strings.Index(contents, "brew tap everydaydevopsio/ballast")
	if trust < 0 || tap < 0 {
		t.Fatal("AMI playbook must explicitly trust and tap everydaydevopsio/ballast")
	}
	if trust > tap {
		t.Fatal("AMI playbook must trust everydaydevopsio/ballast before Homebrew loads the tap")
	}
}

func TestAMIConfigurationPinsPlaywright(t *testing.T) {
	assertFileContains(t, "variables.pkrvars.hcl", `playwright_version            = "1.63.0"`)
	for _, want := range []string{
		`variable "playwright_version"`,
		`PlaywrightVersion          = var.playwright_version`,
		`playwright_version=${var.playwright_version}`,
		`playwright_version            = var.playwright_version`,
	} {
		assertFileContains(t, "ubuntu-desktop.pkr.hcl", want)
	}
	assertFileContains(t, "playbook.yml", `playwright_version={{ playwright_version }}`)
}

func TestAMIPlaybookInstallsSharedPlaywrightChromium(t *testing.T) {
	for _, want := range []string{
		`- name: Require playwright_version`,
		`path: /opt/ai-desktops/playwright-browsers`,
		`PLAYWRIGHT_BROWSERS_PATH: /opt/ai-desktops/playwright-browsers`,
		`playwright@{{ playwright_version }}`,
		`install --with-deps chromium`,
		`DefaultEnvironment=PLAYWRIGHT_BROWSERS_PATH=/opt/ai-desktops/playwright-browsers`,
		`path: /etc/environment`,
		`- name: Verify shared Playwright Chromium launches as ubuntu`,
		`PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1"`,
		"- /usr/bin/env\n          - -u\n          - DISPLAY\n          - -u\n          - DBUS_SESSION_BUS_ADDRESS",
		`chromium.launch({headless: true, timeout: 90000})`,
		`until: playwright_launch_result.rc == 0`,
	} {
		assertFileContains(t, "playbook.yml", want)
	}
}

func TestDesktopSetupVerifiesDeveloperTerminalWorkflow(t *testing.T) {
	setup := filepath.Join("..", "ansible", "desktop-setup", "playbook.yml")
	for _, want := range []string{
		"Inspect AMI developer terminal workflow capability",
		"when: developer_terminal_ami_workflow.stat.exists",
		"Inspect managed Neovim workflow",
		"when: developer_terminal_nvim_workflow.stat.exists",
		"Inspect managed tmux workflow",
		"when: developer_terminal_tmux_workflow.stat.exists",
		"Verify NvChad starts without interaction",
		"Verify managed Neovim ownership",
		"Verify GitHub CLI editor configuration",
		"Verify managed tmux configuration and plugins",
		"Verify tmux starts in detached mode",
		"install_plugins",
		"status-right",
	} {
		assertFileContains(t, setup, want)
	}

	assertFileContains(t, "playbook.yml", "Write developer terminal workflow capability marker")
	for _, want := range []string{"install_plugins", "status-right"} {
		assertFileContains(t, filepath.Join("..", "tests", "integration", "fr05_toolchain_test.go"), want)
	}

	contents, err := os.ReadFile(setup)
	if err != nil {
		t.Fatalf("read desktop setup: %v", err)
	}
	tmuxStart := strings.Index(string(contents), "- name: Verify managed tmux configuration and plugins")
	tmuxEnd := strings.Index(string(contents), "- name: Verify tmux starts in detached mode")
	if tmuxStart < 0 || tmuxEnd <= tmuxStart {
		t.Fatal("desktop setup is missing the managed tmux verification block")
	}
	if strings.Contains(string(contents)[tmuxStart:tmuxEnd], ".config/nvim") {
		t.Fatal("tmux-marker-gated verification must not assert Neovim state")
	}
}
