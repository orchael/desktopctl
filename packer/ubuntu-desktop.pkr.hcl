terraform {
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

source "amazon-ebs" "ubuntu" {
  ami_name        = "ai-desktops-base-${var.novnc_desktop_version}-{{timestamp}}"
  ami_description = "ai-desktops base AMI with pre-installed toolchain"
  instance_type   = "t3.medium"
  region          = var.aws_region

  # Ubuntu 22.04 LTS (Jammy) x86_64 HVM SSD — Canonical official AMI
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
  }

  tag_maps = {
    Environment = "base"
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
      "set -e",
      "echo 'Installing base toolchain...'",

      # Update package lists
      "sudo apt-get update",
      "sudo DEBIAN_FRONTEND=noninteractive apt-get upgrade -y",

      # Install system packages
      "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y",
      "  git",
      "  docker.io",
      "  tmux",
      "  curl",
      "  wget",
      "  unzip",
      "  ca-certificates",
      "  apt-transport-https",
      "  gnupg",
      "  lsb-release",
      "  software-properties-common",
      "  snapd",
      "  awscli",
      "  certbot",
      "  python3-certbot-dns-route53",
      "  nginx",

      # Enable and start docker
      "sudo systemctl enable docker",
      "sudo systemctl start docker",
      "sudo usermod -aG docker ubuntu",

      # Install neovim via snap
      "echo 'Installing neovim...'",
      "sudo snap install nvim --classic",

      # Install Go
      "echo 'Installing Go ${var.go_version}...'",
      "curl -fsSL https://go.dev/dl/go${var.go_version}.linux-amd64.tar.gz | sudo tar -xzf - -C /usr/local/",
      "sudo tee -a /etc/profile.d/golang.sh > /dev/null <<< 'export PATH=$PATH:/usr/local/go/bin'",

      # Install uv (Python package manager)
      "echo 'Installing uv...'",
      "curl -LsSf https://astral.sh/uv/install.sh | sudo sh",

      # Install Homebrew
      "echo 'Installing Homebrew...'",
      "/bin/bash -c \"$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\"",
      "echo 'eval \"$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)\"' | tee -a /etc/profile",

      # Install WireGuard tools
      "echo 'Installing WireGuard...'",
      "sudo DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools",

      # Install novnc-desktop (without TLS — certs configured at boot time)
      "echo 'Installing novnc-desktop...'",
      "curl -fsSL https://raw.githubusercontent.com/orchael/novnc-desktop/${var.novnc_desktop_version}/install.sh | sudo bash -s -- --desktop-type elementary --http-port 8080",

      # Install ai-agent-bridge
      "echo 'Installing ai-agent-bridge...'",
      "curl -fsSL https://raw.githubusercontent.com/orchael/ai-agent-bridge/${var.ai_agent_bridge_version}/install.sh | sudo bash -s -- --bind 127.0.0.1 --port 9445",
      "sudo systemctl enable ai-agent-bridge",

      # Clean up
      "echo 'Cleaning up...'",
      "sudo apt-get clean",
      "sudo apt-get autoclean -y",
      "history -c"
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
