packer {
  required_plugins {
    amazon = {
      version = ">= 1.2.0"
      source  = "github.com/hashicorp/amazon"
    }
    ansible = {
      version = ">= 1.1.0"
      source  = "github.com/hashicorp/ansible"
    }
  }
}

variable "bridgectl_version" {
  type        = string
  description = "bridgectl release tag (e.g. v1.4.3)"
}

variable "tailscale_version" {
  type        = string
  description = "Tailscale apt package version to install (e.g. 1.98.9)"
}

variable "helm_version" {
  type        = string
  description = "Pinned Helm release installed from get.helm.sh (e.g. v4.3.1)"
}

variable "helm_sha256" {
  type        = string
  description = "SHA-256 of the pinned Helm linux-amd64 release archive"
}

variable "terraform_version" {
  type        = string
  description = "Terraform release installed and selected through tfenv (e.g. 1.16.5)"
}

variable "vscode_version" {
  type        = string
  description = "Visual Studio Code stable apt package version (e.g. 1.139.1-1790309529)"
}

variable "vscode_sha256" {
  type        = string
  description = "SHA-256 of the pinned Visual Studio Code amd64 Debian package"
}

variable "playwright_version" {
  type        = string
  description = "Playwright version whose Chromium revision is baked into the AMI"
}

variable "go_version" {
  type        = string
  description = "Go version to install (e.g. 1.26.0)"
}

variable "go_sha256" {
  type        = string
  description = "SHA-256 of the pinned Go linux-amd64 archive"
}

variable "uv_version" {
  type        = string
  description = "uv version to install (e.g. 0.4.0)"
}

variable "uv_sha256" {
  type        = string
  description = "SHA-256 of the pinned uv linux-amd64 archive"
}

variable "nvm_ref" {
  type        = string
  description = "Pinned nvm Git commit"
}

variable "pyenv_ref" {
  type        = string
  description = "Pinned pyenv Git commit"
}

variable "rustup_version" {
  type        = string
  description = "Pinned rustup-init release"
}

variable "rustup_sha256" {
  type        = string
  description = "SHA-256 of the pinned rustup-init linux-amd64 executable"
}

variable "rust_toolchain_version" {
  type        = string
  description = "Pinned Rust compiler toolchain installed by rustup"
}

variable "nodejs_version" {
  type        = string
  description = "Pinned NodeSource nodejs apt package version"
}

variable "awscli_version" {
  type        = string
  description = "Pinned AWS CLI v2 version"
}

variable "awscli_sha256" {
  type        = string
  description = "SHA-256 of the pinned AWS CLI v2 linux-amd64 archive"
}

variable "flutter_version" {
  type        = string
  description = "Flutter SDK version to install (e.g. 3.32.0)"
}

variable "android_cmdline_tools_version" {
  type        = string
  description = "Android SDK command-line tools build number (e.g. 11076708); see https://developer.android.com/studio#command-tools"
}

variable "desktop_web_version" {
  type        = string
  description = "desktop-web npm package version to install (e.g. 0.2.0)"
}

variable "github_npm_token" {
  type        = string
  description = "GitHub token with read:packages scope for installing @orchael/desktopctl (set via PKR_VAR_github_npm_token, mapped automatically from GITHUB_NPM_TOKEN by the CLI)"
  sensitive   = true
}

variable "aws_region" {
  type        = string
  description = "AWS region for the source and target AMI"
}

variable "aws_profile" {
  type        = string
  default     = ""
  description = "AWS shared credentials profile selected by desktopctl"
}

variable "ami_regions" {
  type        = list(string)
  default     = []
  description = "Additional regions to copy the built AMI to"
}

# source_ami is always required. The desktopctl CLI resolves the correct AMI
# before invoking packer (either from --base-ami or via an EC2 lookup) and
# passes it via -var source_ami=<id>.
variable "source_ami" {
  type        = string
  description = "Source AMI ID to use as the base for this build."
}

variable "desktopctl_version" {
  type        = string
  description = "desktopctl CLI version that built this AMI (e.g. 0.2.4)"
}

variable "desktopctl_major" {
  type        = string
  description = "Major version of the desktopctl CLI; supplied by the CLI"
}

variable "build_date" {
  type        = string
  description = "UTC build date in YYYYMMDD form; supplied by the CLI"
}

variable "novnc_desktop_version" {
  type        = string
  description = "novnc-desktop base AMI version stamp (extracted from the base AMI name, e.g. 20260525-005909)"
}

variable "ami_public" {
  type        = bool
  default     = false
  description = "When true, set the built AMI's launch permissions to public."
}

variable "subnet_id" {
  type        = string
  default     = ""
  description = "Subnet ID for the Packer build instance. Set by the CLI when no default VPC exists in the target region (e.g. the foundation VPC subnet). Leave empty to let AWS select a default-VPC subnet."
}

source "amazon-ebs" "ubuntu" {
  ami_name        = "desktopctl-v${var.desktopctl_major}-${var.build_date}-{{timestamp}}"
  ami_description = "desktopctl AMI - novnc-desktop elementary base with desktopctl toolchain"
  instance_type   = "c6i.xlarge"
  region          = var.aws_region
  profile         = var.aws_profile != "" ? var.aws_profile : null
  ami_regions     = var.ami_regions
  source_ami      = var.source_ami

  ami_groups = var.ami_public ? ["all"] : []

  subnet_id = var.subnet_id != "" ? var.subnet_id : null

  associate_public_ip_address = true
  ebs_optimized               = true

  launch_block_device_mappings {
    device_name           = "/dev/sda1"
    volume_size           = 40
    volume_type           = "gp3"
    delete_on_termination = true
  }

  tags = {
    Name                       = "desktopctl"
    ManagedBy                  = "desktopctl-packer"
    DesktopctlVersion          = var.desktopctl_version
    DesktopctlMajor            = var.desktopctl_major
    BuildDate                  = var.build_date
    BridgeVersion              = var.bridgectl_version
    TailscaleVersion           = var.tailscale_version
    HelmVersion                = var.helm_version
    TerraformVersion           = var.terraform_version
    VSCodeVersion              = var.vscode_version
    PlaywrightVersion          = var.playwright_version
    GoVersion                  = var.go_version
    UvVersion                  = var.uv_version
    FlutterVersion             = var.flutter_version
    AndroidCmdlineToolsVersion = var.android_cmdline_tools_version
    DesktopWebVersion          = var.desktop_web_version
    NovncDesktopVersion        = var.novnc_desktop_version
    BaseAMI                    = var.source_ami
    Environment                = "base"
  }

  ssh_username            = "ubuntu"
  ssh_timeout             = "10m"
  ssh_keep_alive_interval = "10s"
}

build {
  name    = "desktopctl"
  sources = ["source.amazon-ebs.ubuntu"]

  # Stop unattended-upgrades before Ansible runs so apt installs don't race
  # with the background upgrade process holding /var/lib/dpkg/lock-frontend.
  # The playbook re-enables these services at the end so launched instances
  # still receive automatic security updates.
  provisioner "shell" {
    inline = [
      "sudo systemctl stop apt-daily.timer apt-daily-upgrade.timer || true",
      "sudo systemctl stop unattended-upgrades.service apt-daily.service || true",
      "sudo systemctl kill --kill-who=all apt-daily.service unattended-upgrades.service || true",
      "sudo systemctl mask apt-daily.service apt-daily.timer apt-daily-upgrade.service apt-daily-upgrade.timer unattended-upgrades.service || true",
      "timeout 120 bash -c 'while sudo fuser /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend /var/cache/apt/archives/lock >/dev/null 2>&1; do echo \"Waiting for dpkg lock...\"; sleep 5; done'",
    ]
  }

  provisioner "file" {
    source      = "${path.root}/../ansible/desktop-setup/playbook.yml"
    destination = "/tmp/desktop-setup.yml"
  }

  provisioner "shell" {
    inline = [
      "sudo mkdir -p /opt/desktopctl",
      "sudo mv /tmp/desktop-setup.yml /opt/desktopctl/desktop-setup.yml",
      "sudo chmod 644 /opt/desktopctl/desktop-setup.yml",
    ]
  }

  provisioner "ansible" {
    playbook_file        = "${path.root}/playbook.yml"
    galaxy_file          = "${path.root}/requirements.yml"
    galaxy_force_install = true
    extra_arguments = [
      "--extra-vars", "go_version=${var.go_version} go_sha256=${var.go_sha256} uv_version=${var.uv_version} uv_sha256=${var.uv_sha256} nvm_ref=${var.nvm_ref} pyenv_ref=${var.pyenv_ref} rustup_version=${var.rustup_version} rustup_sha256=${var.rustup_sha256} rust_toolchain_version=${var.rust_toolchain_version} nodejs_version=${var.nodejs_version} awscli_version=${var.awscli_version} awscli_sha256=${var.awscli_sha256} flutter_version=${var.flutter_version} android_cmdline_tools_version=${var.android_cmdline_tools_version} bridgectl_version=${var.bridgectl_version} tailscale_version=${var.tailscale_version} helm_version=${var.helm_version} helm_sha256=${var.helm_sha256} terraform_version=${var.terraform_version} vscode_version=${var.vscode_version} vscode_sha256=${var.vscode_sha256} playwright_version=${var.playwright_version} desktop_web_version=${var.desktop_web_version} novnc_desktop_version=${var.novnc_desktop_version}",
    ]
    ansible_env_vars = [
      "ANSIBLE_HOST_KEY_CHECKING=False",
      "ANSIBLE_COLLECTIONS_PATH=/tmp/desktopctl-collections",
      "ANSIBLE_COLLECTIONS_SCAN_SYS_PATH=False",
      "ANSIBLE_CALLBACKS_ENABLED=ansible.posix.profile_tasks",
      "DESKTOP_WEB_NPM_TOKEN=${var.github_npm_token}",
    ]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
    custom_data = {
      desktopctl_version            = var.desktopctl_version
      desktopctl_major              = var.desktopctl_major
      build_date                    = var.build_date
      bridge_version                = var.bridgectl_version
      tailscale_version             = var.tailscale_version
      helm_version                  = var.helm_version
      terraform_version             = var.terraform_version
      vscode_version                = var.vscode_version
      playwright_version            = var.playwright_version
      go_version                    = var.go_version
      uv_version                    = var.uv_version
      flutter_version               = var.flutter_version
      android_cmdline_tools_version = var.android_cmdline_tools_version
      base_ami                      = var.source_ami
      desktop_web_version           = var.desktop_web_version
      novnc_desktop_version         = var.novnc_desktop_version
    }
  }
}
