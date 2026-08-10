# Packer AMI Build Workflow

## Overview

`ai-desktops` builds pre-baked Ubuntu 24.04 desktop AMIs with Packer. The AMI contains the
stateless toolchain and services needed by every desktop. Cloud-init remains responsible for
runtime-only work: TLS certificates, secret injection, workspace setup, and repository cloning.

## Version Pins

Review `packer/variables.pkrvars.hcl` before building:

```hcl
ai_agent_bridge_version = "v0.9.0"
go_version              = "1.24.0"
uv_version              = "0.4.0"
```

The Packer build starts from the latest public `novnc-desktop-ubuntu-24.04-elementary-*` AMI in
each requested region. The resulting image adds Docker, GitHub CLI, Python, Go, uv, Homebrew,
Ansible, AWS CLI, neovim, Node.js, provider runtimes, and a pinned `ai-agent-bridge` package.

## Build

Run from the repository root:

```bash
ai-desktops ami build --regions us-east-2
```

Build multiple regions sequentially with a comma-separated list:

```bash
ai-desktops ami build --regions us-east-1,us-west-2
```

The CLI initializes the Packer plugins once, then for each region:

1. Resolves the latest matching public `novnc-desktop` base AMI.
2. Runs the Packer build with the region-specific source AMI.
3. Parses `packer/manifest.json`.
4. Saves the AMI record to DynamoDB and marks it active in `config.yaml`.

Use `--base-ami <id>` to override source AMI lookup for a single-region build.

## Verify

```bash
ai-desktops ami list
ai-desktops create --repo myorg/myrepo --preview
```

The preview must reference the active AMI ID. After creating a desktop, verify the baked tools:

```bash
ai-desktops doctor <desktop-id>
ai-desktops ssh <desktop-id> -- 'docker version && gh --version && go version'
ai-desktops ssh <desktop-id> -- 'sudo -u ubuntu /home/linuxbrew/.linuxbrew/bin/ballast --version'
```

## Troubleshooting

Packer stops unattended-upgrades before Ansible runs and waits for dpkg locks to clear. If a
build fails:

1. Review the Packer and Ansible error output.
2. Confirm the version pins exist in `packer/variables.pkrvars.hcl`.
3. Confirm the matching public `novnc-desktop` base AMI exists in the requested region.
4. Re-run one region at a time with `--regions <region>` while diagnosing.

## File Locations

- Packer config: `packer/ubuntu-desktop.pkr.hcl`
- Packer variables: `packer/variables.pkrvars.hcl`
- Packer Ansible playbook: `packer/playbook.yml`
- Generated manifest: `packer/manifest.json`
