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

variable "aws_region" {
  type        = string
  description = "AWS region for the source and target AMI"
}

variable "ami_public" {
  type        = bool
  default     = false
  description = "When true, set the built AMI's launch permissions to public."
}

# Resolve the latest Ubuntu 24.04 LTS AMI published by Canonical.
data "amazon-ami" "ubuntu" {
  filters = {
    name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
    root-device-type    = "ebs"
    virtualization-type = "hvm"
  }
  owners      = ["099720109477"] # Canonical
  most_recent = true
  region      = var.aws_region
}

source "amazon-ebs" "ubuntu" {
  ami_name        = "ai-desktops-${var.ai_agent_bridge_version}-{{timestamp}}"
  ami_description = "ai-desktops AMI - Ubuntu 24.04 with novnc-desktop and ai-desktops toolchain"
  instance_type   = "t3.medium"
  region          = var.aws_region
  source_ami      = data.amazon-ami.ubuntu.id

  ami_groups = var.ami_public ? ["all"] : []

  associate_public_ip_address = true
  ebs_optimized               = true

  launch_block_device_mappings {
    device_name           = "/dev/sda1"
    volume_size           = 20
    volume_type           = "gp3"
    delete_on_termination = true
  }

  tags = {
    Name               = "ai-desktops"
    ManagedBy          = "ai-desktops-packer"
    BridgeVersion      = var.ai_agent_bridge_version
    GoVersion          = var.go_version
    UvVersion          = var.uv_version
    BaseAMI            = data.amazon-ami.ubuntu.id
    Environment        = "base"
  }

  ssh_username = "ubuntu"
  ssh_timeout  = "10m"
}

build {
  name    = "ai-desktops"
  sources = ["source.amazon-ebs.ubuntu"]

  # Build the desktop-web React app locally before provisioning the AMI so
  # the compiled dist can be uploaded directly without requiring a GitHub
  # SSH key on the launched instance.
  provisioner "shell-local" {
    command = "cd ${path.root}/../apps/desktop-web && pnpm install --frozen-lockfile && pnpm run build"
  }

  # scp requires the destination directory to exist before uploading into it.
  provisioner "shell" {
    inline = ["mkdir -p /tmp/desktop-web-dist"]
  }

  # Upload the pre-built dist and the API server to the instance so the
  # Ansible playbook can move them into place without re-building on the AMI.
  provisioner "file" {
    source      = "${path.root}/../apps/desktop-web/dist/"
    destination = "/tmp/desktop-web-dist/"
  }

  provisioner "file" {
    source      = "${path.root}/../apps/desktop-web/api-server.py"
    destination = "/tmp/desktop-web-api-server.py"
  }

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
      "--extra-vars", "go_version=${var.go_version} uv_version=${var.uv_version} ai_agent_bridge_version=${var.ai_agent_bridge_version}",
    ]
    ansible_env_vars = [
      "ANSIBLE_HOST_KEY_CHECKING=False",
      "ANSIBLE_COLLECTIONS_PATH=/tmp/ai-desktops-collections",
      "ANSIBLE_COLLECTIONS_SCAN_SYS_PATH=False",
    ]
  }

  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
    custom_data = {
      bridge_version       = var.ai_agent_bridge_version
      go_version           = var.go_version
      uv_version           = var.uv_version
      base_ami             = data.amazon-ami.ubuntu.id
    }
  }
}
