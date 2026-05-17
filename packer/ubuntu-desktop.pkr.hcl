packer {
  required_plugins {
    amazon = {
      version = ">= 1.2.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

variable "novnc_desktop_version" {
  type        = string
  description = "novnc-desktop release tag (e.g. v0.1.5)"
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
  description = "AWS region for the source and target AMI (e.g. us-east-2)"
}

variable "source_ami" {
  type        = string
  default     = ""
  description = "Optional AMI ID to use as base. If not specified, uses latest Ubuntu 22.04 LTS from Canonical."
}

source "amazon-ebs" "ubuntu" {
  ami_name        = "ai-desktops-base-${var.novnc_desktop_version}-{{timestamp}}"
  ami_description = "ai-desktops base AMI with pre-installed toolchain"
  instance_type   = "t3.medium"
  region          = var.aws_region

  # Use explicit source_ami if provided; otherwise filter for latest Ubuntu 22.04 LTS
  source_ami = var.source_ami != "" ? var.source_ami : null

  # Ubuntu 22.04 LTS (Jammy) x86_64 HVM SSD — Canonical official AMI
  # Only used if source_ami is not specified
  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"] # Canonical
  }

  ssh_username = "ubuntu"

  # Tag the AMI with component versions
  tags = {
    Name           = "ai-desktops-base-${var.novnc_desktop_version}"
    ManagedBy      = "ai-desktops-packer"
    NovncVersion   = var.novnc_desktop_version
    BridgeVersion  = var.ai_agent_bridge_version
    GoVersion      = var.go_version
    UvVersion      = var.uv_version
    Environment    = "base"
  }
}

build {
  name = "ai-desktops-base"
  sources = [
    "source.amazon-ebs.ubuntu"
  ]

  # Update package lists and install base toolchain
  provisioner "shell" {
    inline = [
      "set -eux",
      "export DEBIAN_FRONTEND=noninteractive",
      "echo 'Installing base toolchain...'",

      # Update package lists and install base packages
      "sudo apt-get update -y",
      "sudo apt-get upgrade -y",
      "sudo apt-get install -y software-properties-common",
      "sudo add-apt-repository -y universe",
      "sudo apt-get update -y",
      "sudo apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release unzip build-essential",
      "sudo apt-get install -y git docker.io tmux nginx ansible",

      # Install neovim via snap
      "echo 'Installing neovim...'",
      "sudo snap install nvim --classic",

      # Enable and start docker
      "sudo systemctl enable docker",
      "sudo systemctl start docker",
      "sudo usermod -aG docker ubuntu",

      # Install Go
      "echo 'Installing Go ${var.go_version}...'",
      "curl -fsSL https://go.dev/dl/go${var.go_version}.linux-amd64.tar.gz | sudo tar -xzf - -C /usr/local/",
      "echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/golang.sh > /dev/null",

      # Install uv (Python package manager) to a system-wide location
      "echo 'Installing uv...'",
      "curl -LsSf https://astral.sh/uv/install.sh | bash",
      "sudo mkdir -p /usr/local/bin",
      "sudo cp ~/.local/bin/uv /usr/local/bin/uv",
      "sudo chmod +x /usr/local/bin/uv",
      "which uv || echo 'WARNING: uv installation may have failed'",

      # Install AWS CLI v2
      "echo 'Installing AWS CLI v2...'",
      "curl -fsSL 'https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip' -o '/tmp/awscliv2.zip' && sudo unzip -q /tmp/awscliv2.zip -d /tmp && sudo /tmp/aws/install && sudo rm -rf /tmp/awscliv2.zip /tmp/aws",

      # Clean up (Homebrew, novnc-desktop, and ai-agent-bridge will be installed at boot time via Ansible/cloud-init)
      "echo 'Cleaning up...'",
      "sudo apt-get clean",
      "sudo apt-get autoclean -y"
    ]
  }

  # Capture build manifest
  post-processor "manifest" {
    output     = "manifest.json"
    strip_path = true
    custom_data = {
      novnc_version   = var.novnc_desktop_version
      bridge_version  = var.ai_agent_bridge_version
      go_version      = var.go_version
      uv_version      = var.uv_version
    }
  }
}
