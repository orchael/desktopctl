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

variable "ai_agent_bridge_version" {
  type        = string
  description = "ai-agent-bridge release tag (e.g. v0.1.0)"
}

variable "go_version" {
  type        = string
  description = "Go version to install (e.g. 1.23.0)"
}

variable "uv_version" {
  type        = string
  description = "uv version to install (e.g. 0.4.0)"
}

variable "flutter_version" {
  type        = string
  description = "Flutter SDK version to install (e.g. 3.32.0)"
}

variable "desktop_web_version" {
  type        = string
  description = "desktop-web npm package version to install (e.g. 0.2.0)"
}

variable "github_npm_token" {
  type        = string
  description = "GitHub token with read:packages scope for installing @markcallen/desktop-web (set via PKR_VAR_github_npm_token, mapped automatically from GITHUB_NPM_TOKEN by the CLI)"
  sensitive   = true
}

variable "aws_region" {
  type        = string
  description = "AWS region for the source and target AMI"
}

# source_ami is always required. The ai-desktops CLI resolves the correct AMI
# before invoking packer (either from the vars file or via an EC2 lookup) and
# passes it via -var source_ami=<id>.
variable "source_ami" {
  type        = string
  description = "Source AMI ID to use as the base for this build."
}

variable "ai_desktops_version" {
  type        = string
  description = "ai-desktops CLI version that built this AMI (e.g. 0.2.4)"
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
  ami_name        = "ai-desktops-${var.ai_agent_bridge_version}-{{timestamp}}"
  ami_description = "ai-desktops AMI - novnc-desktop elementary base with ai-desktops toolchain"
  instance_type   = "t3.medium"
  region          = var.aws_region
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
    Name               = "ai-desktops"
    ManagedBy          = "ai-desktops-packer"
    AiDesktopsVersion  = var.ai_desktops_version
    BridgeVersion      = var.ai_agent_bridge_version
    GoVersion          = var.go_version
    UvVersion          = var.uv_version
    FlutterVersion     = var.flutter_version
    DesktopWebVersion  = var.desktop_web_version
    NovncDesktopVersion = var.novnc_desktop_version
    BaseAMI            = var.source_ami
    Environment        = "base"
  }

  ssh_username = "ubuntu"
  ssh_timeout  = "10m"
}

build {
  name    = "ai-desktops"
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
      "timeout 120 bash -c 'while sudo fuser /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend /var/cache/apt/archives/lock >/dev/null 2>&1; do echo \"Waiting for dpkg lock...\"; sleep 5; done'",
    ]
  }

  provisioner "ansible" {
    playbook_file        = "${path.root}/playbook.yml"
    galaxy_file          = "${path.root}/requirements.yml"
    galaxy_force_install = true
    extra_arguments = [
      "--extra-vars", "go_version=${var.go_version} uv_version=${var.uv_version} flutter_version=${var.flutter_version} ai_agent_bridge_version=${var.ai_agent_bridge_version} desktop_web_version=${var.desktop_web_version} novnc_desktop_version=${var.novnc_desktop_version}",
    ]
    ansible_env_vars = [
      "ANSIBLE_HOST_KEY_CHECKING=False",
      "ANSIBLE_COLLECTIONS_PATH=/tmp/ai-desktops-collections",
      "ANSIBLE_COLLECTIONS_SCAN_SYS_PATH=False",
      "DESKTOP_WEB_NPM_TOKEN=${var.github_npm_token}",
    ]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
    custom_data = {
      ai_desktops_version   = var.ai_desktops_version
      bridge_version        = var.ai_agent_bridge_version
      go_version            = var.go_version
      uv_version            = var.uv_version
      flutter_version       = var.flutter_version
      base_ami              = var.source_ami
      desktop_web_version   = var.desktop_web_version
      novnc_desktop_version = var.novnc_desktop_version
    }
  }
}
