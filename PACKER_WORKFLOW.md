# Packer AMI Build Workflow

## Overview

The AMI build is now split into two stages:

1. **Packer Stage** (fast, ~5-10 min): Build base AMI with toolchain
2. **Post-Launch Stage** (manual, ~5-10 min): Install novnc-desktop on running instance

This avoids cloud-init race conditions and dpkg lock issues during the AMI build.

## Workflow

### 1. Build Base AMI with Packer

```bash
cd /home/marka/src/orchael/ai-desktops

packer build \
  -var-file=packer/variables.pkrvars.hcl \
  packer/ubuntu-desktop.pkr.hcl
```

This creates an AMI with:
- Go (specified version)
- Python + uv
- Docker
- Ansible
- AWS CLI v2
- nginx, tmux, git, build tools
- neovim (snap)

**No novnc-desktop** — that's installed post-launch.

### 2. Launch EC2 Instance from Base AMI

Use the AMI ID from Packer output to launch an instance:

```bash
aws ec2 run-instances \
  --image-id ami-xxxxxxxxx \
  --instance-type t3.medium \
  --key-name your-key \
  --security-groups default \
  --region us-east-2
```

Wait for the instance to fully boot and stabilize (~1-2 min).

### 3. Install novnc-desktop via Post-Launch Ansible

Once the instance is running and stable, provision novnc-desktop:

```bash
INSTANCE_IP=<instance-public-ip>

ansible-playbook \
  -i "${INSTANCE_IP}," \
  -u ubuntu \
  --private-key /path/to/key.pem \
  ansible/post-launch-novnc.yml \
  -e "novnc_version=v0.1.5 bridge_version=v0.1.0"
```

**Required variables:**
- `novnc_version`: Release tag (e.g., `v0.1.5`)
- `bridge_version`: Release tag (e.g., `v0.1.0`)

### 4. Create Final AMI from Provisioned Instance

Once novnc-desktop is installed and tested, create a new AMI:

```bash
aws ec2 create-image \
  --instance-id i-xxxxxxxxx \
  --name "ai-desktops-novnc-$(date +%Y%m%d)" \
  --description "ai-desktops with novnc-desktop pre-installed"
```

## Why This Approach?

**Problem with Packer-based installation:**
- Ubuntu's cloud-init runs unattended-upgrades in background during boot
- Packer starts provisioning immediately after SSH is available
- Race condition: Packer and cloud-init both try to use dpkg/apt
- Result: "dpkg was interrupted" errors

**Solution: Post-launch provisioning**
- Wait for cloud-init to fully complete before provisioning
- Instance is stable and ready for apt operations
- No dpkg lock conflicts
- Faster Packer build (skips novnc-desktop complexity)
- Cleaner separation of concerns

## Troubleshooting

**If Ansible provisioning fails:**

1. SSH into the instance manually:
   ```bash
   ssh -i /path/to/key.pem ubuntu@<instance-ip>
   ```

2. Run the post-launch playbook with more verbosity:
   ```bash
   ansible-playbook \
     -i "${INSTANCE_IP}," \
     -u ubuntu \
     --private-key /path/to/key.pem \
     ansible/post-launch-novnc.yml \
     -e "novnc_version=v0.1.5 bridge_version=v0.1.0" \
     -vvv
   ```

3. Check the instance's cloud-init status:
   ```bash
   ssh -i /path/to/key.pem ubuntu@<instance-ip> cloud-init status
   ```

## File Locations

- Packer config: `packer/ubuntu-desktop.pkr.hcl`
- Packer variables: `packer/variables.pkrvars.hcl`
- Post-launch playbook: `ansible/post-launch-novnc.yml`
- novnc-desktop role: `/home/marka/src/novnc-desktop/`
