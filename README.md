# ai-desktops

[![CI](https://github.com/orchael/ai-desktops/actions/workflows/ci.yml/badge.svg)](https://github.com/orchael/ai-desktops/actions/workflows/ci.yml)
[![Release](https://github.com/orchael/ai-desktops/actions/workflows/publish-cli.yml/badge.svg)](https://github.com/orchael/ai-desktops/actions/workflows/publish-cli.yml)
[![License](https://img.shields.io/github/license/orchael/ai-desktops)](LICENSE)
[![GitHub Release](https://img.shields.io/github/v/release/orchael/ai-desktops)](https://github.com/orchael/ai-desktops/releases)

A Go CLI-driven fleet manager for persistent remote AI coding desktops on AWS.

Each desktop is an EC2 instance running a full Elementary (Pantheon) desktop environment accessible via noVNC, with `ai-agent-bridge` for programmatic AI agent access and a pre-cloned developer workspace. Fleet state is tracked in DynamoDB; infrastructure is managed with Pulumi using S3 as the state backend.

## Prerequisites

### Tools

- Go 1.22+
- [Pulumi CLI](https://www.pulumi.com/docs/install/) — `curl -fsSL https://get.pulumi.com | sh`
- AWS CLI v2 — configured with a profile that has the permissions listed below
- `pnpm` — for the desktop webapp (`apps/desktop-web`)

### AWS permissions

The operator profile needs the following permissions:

**Bootstrap (`bootstrap` command):**
- `s3:CreateBucket`, `s3:HeadBucket`, `s3:PutBucketVersioning`, `s3:PutEncryptionConfiguration`, `s3:PutBucketPublicAccessBlock`
- `dynamodb:CreateTable`, `dynamodb:DescribeTable`

**Foundation (`init-foundation` command):**
- `ec2:CreateVpc`, `ec2:CreateSubnet`, `ec2:CreateInternetGateway`, `ec2:CreateRouteTable`, `ec2:CreateSecurityGroup`, and associated `Describe*`/`Delete*` variants
- `iam:CreateRole`, `iam:PutRolePolicy`, `iam:AttachRolePolicy`, `iam:CreateInstanceProfile`, `iam:AddRoleToInstanceProfile`, and associated `Get*`/`List*`/`Delete*` variants
- `route53:GetHostedZone`, `route53:ListHostedZones`
- Full Pulumi S3 state backend access on the bootstrap bucket

**Desktop lifecycle (`create`, `stop`, `start`, `terminate`):**
- `ec2:RunInstances`, `ec2:StopInstances`, `ec2:StartInstances`, `ec2:TerminateInstances`, `ec2:DescribeInstances`
- `ec2:CreateTags`
- `route53:ChangeResourceRecordSets`, `route53:ListResourceRecordSets`
- `dynamodb:PutItem`, `dynamodb:GetItem`, `dynamodb:UpdateItem`, `dynamodb:Scan`

**Tunnel access (`agent` command):**
- `ssm:StartSession` with document `AWS-StartPortForwardingSession`
- `ssm:DescribeSessions`, `ssm:TerminateSession`

### Route53 prerequisites

A hosted zone for the desktop DNS domain must already exist before running `init-foundation`:

- **Production** (`--env prod`): zone for `desktops.orchael.com`
- **Development** (`--env dev`): zone for `desktops.orchael.dev`

The foundation stack looks up the zone by domain name and exports the zone ID. If the zone is missing, `init-foundation` will fail with a clear error.

### GitHub PAT

Repositories are cloned during desktop boot using a GitHub personal access token retrieved from AWS Secrets Manager at runtime. The token is also written to `/home/ubuntu/.npmrc` on the desktop so the ubuntu user can install packages from GitHub Packages (e.g. `@<owner>/*` scoped packages).

The token requires these scopes:

| Scope | Purpose |
|-------|---------|
| `admin:public_key` | Register SSH keys |
| `repo` | Clone, push, PRs |
| `workflow` | GitHub Actions |
| `security_events` | Code scanning, secret scanning |
| `read:user` | Identity |
| `read:org` | Required by gh CLI auth |
| `read:packages` | Install packages from GitHub Packages |

Run `ai-desktops setup` to store the token in AWS Secrets Manager (at `/ai-desktops/<owner>/github`) before running `create`.

## Installation

```bash
git clone https://github.com/orchael/ai-desktops
cd ai-desktops
go install ./cmd/ai-desktops
```

Or build directly:

```bash
go build -o ai-desktops ./cmd/ai-desktops
```

## Configuration

Create `~/.ai-desktops/config.yaml` (or pass `--config <path>`):

```yaml
aws:
  region: us-east-1
  profile: myprofile

pulumi:
  backend_bucket: my-ai-desktops-pulumi-state

fleet:
  table_name: ai-desktops-fleet
  environment: dev    # dev or prod

github:
  owner: myorg
  pat_secret: /ai-desktops/github-pat

desktop:
  instance_type: t3.xlarge
  operator_cidr: 203.0.113.42/32   # your public IP

agent:
  bridge_port: 9445
```

See `config.example.yaml` at the repo root for a fully commented example.

## Full lifecycle workflow

### 1. Configure the Pulumi state backend

Set the S3 bucket name in your config file before running any infrastructure commands:

```yaml
pulumi:
  backend_bucket: <globally-unique-bucket-name>   # e.g. myorg-ai-desktops-pulumi-state
```

The bucket name must be globally unique across all AWS accounts. Pick something like
`<your-org>-ai-desktops-pulumi-state`.

> **Enforced by the CLI**: `init-foundation`, `create`, and `terminate` check that this
> bucket exists in AWS before doing any work. If the bucket is missing they will print
> the exact command needed to create it.

### 2. Bootstrap Pulumi state backend

```bash
ai-desktops bootstrap
```

Creates the S3 bucket using AWS API calls (not Pulumi). Configures versioning, encryption (SSE), and public-access block. Idempotent — safe to re-run. This step must complete before Pulumi can initialize.

### 3. Initialize shared foundation infrastructure

```bash
ai-desktops init-foundation
```

Creates: VPC, public subnet, internet gateway, security group (SSH from `operator_cidr`, HTTPS from anywhere), IAM role with SSM + Secrets Manager permissions, instance profile, and looks up the Route53 zone.

Preview without applying:

```bash
ai-desktops init-foundation --preview
```

### 4. Build a pre-baked AMI

Pre-baked AMIs reduce desktop boot time by pre-installing the toolchain. Desktop creation
requires an active AMI for its AWS region; cloud-init handles runtime-only work such as
TLS setup, secret injection, workspace creation, and repository cloning.

**Prerequisites for Packer:**
- [Packer](https://www.packer.com/downloads) installed
- AWS credentials configured (same profile as above)

**Build the AMI:**

```bash
ai-desktops ami build --regions us-east-2
```

Build multiple regions sequentially with a comma-separated list:

```bash
ai-desktops ami build --regions us-east-1,us-west-2
```

This runs Packer to build an AMI on top of the latest public `novnc-desktop-ubuntu-24.04-elementary` base with pre-installed:
- Docker
- Go
- uv (Python package manager)
- AWS CLI v2
- neovim (via snap)
- WireGuard tools
- Homebrew
- `ai-agent-bridge`

**Verify the AMI:**

```bash
ai-desktops ami list
```

Output shows the registered AMI ID per region. The CLI automatically detects pre-baked AMIs and uses them during `create`.

**Custom Packer builds:**

Edit `packer/ubuntu-desktop.pkr.hcl` to change versions or add packages. Re-run `ami build` to create a new AMI.

### 5. Create a desktop

```bash
ai-desktops create --repo myorg/my-app --repo myorg/shared-lib
```

Or use full GitHub URLs:

```bash
ai-desktops create --github-owner myorg --repo https://github.com/myorg/my-app --repo https://github.com/myorg/shared-lib
```

Override the root volume size (default 100 GiB):

```bash
ai-desktops create --repo myorg/my-app --volume-size 200
```

- Validates repo owner boundary (all repos must belong to the same GitHub owner)
- Creates a DynamoDB record in state `creating`
- Prints the Pulumi stack name to run next: `cd infra/pulumi/desktop && pulumi stack select <stack> && pulumi up`
- Cloud-init installs tools, retrieves GitHub PAT from AWS Secrets Manager/SSM, and clones repos under `/workspace`

### 6. Inspect fleet

```bash
ai-desktops list
ai-desktops list --all
ai-desktops status d-a1b2c3d4
```

`list` hides terminated desktop records by default. Use `list --all` to include them.

Delete accumulated terminated records after reviewing them:

```bash
ai-desktops purge --dry-run
ai-desktops purge
```

### 7. Open noVNC

```bash
ai-desktops url d-a1b2c3d4
# https://d-a1b2c3d4.desktops.orchael.dev
```

### 8. SSH into desktop

```bash
ai-desktops ssh d-a1b2c3d4
```

### 9. Verify git repos are cloned

Once SSH'd into the desktop, verify that the repositories specified during `create` were cloned successfully:

```bash
# List cloned repos
ls -la /workspace

# Example: verify a specific repo
cd /workspace/my-app
git log --oneline -5
git remote -v
```

If repos are missing or the clone failed, check the cloud-init bootstrap logs from your local machine:

```bash
INSTANCE_ID=$(ai-desktops status d-a1b2c3d4 | grep "Instance ID" | awk -F: '{print $2}' | xargs)
aws ssm start-session --target $INSTANCE_ID --region us-east-2
# Then inside the session:
tail -100 /var/log/cloud-init-output.log
```

### 10. Use AI agent bridge

```bash
# Status and available providers
ai-desktops agent d-a1b2c3d4 status
ai-desktops agent d-a1b2c3d4 providers

# Start an agent session
ai-desktops agent d-a1b2c3d4 start --provider claude --repo myorg/my-app

# List active sessions
ai-desktops agent d-a1b2c3d4 sessions

# Stop a session
ai-desktops agent d-a1b2c3d4 stop <session-id>
```

The CLI connects directly to the desktop via SSH. The bridge is accessed over `localhost:9445` on the desktop itself.

### 11. Run diagnostics

```bash
ai-desktops doctor d-a1b2c3d4
ai-desktops doctor d-a1b2c3d4 --json
```

Checks: EC2 running, SSH reachable, noVNC HTTPS responds, Docker active, bridge active.

### 12. Debug with SSM (if diagnostics fail)

If `doctor` reports issues, use AWS Systems Manager Session Manager to open an interactive shell on the instance for debugging:

```bash
# Get the instance ID from the status output
INSTANCE_ID=$(ai-desktops status d-a1b2c3d4 | grep instance_id | cut -d: -f2 | xargs)

# Start an interactive session
aws ssm start-session --target $INSTANCE_ID --profile <your-profile>
```

Once in the session, you can inspect logs and services:

```bash
# View cloud-init logs (bootstrap output)
tail -100 /var/log/cloud-init-output.log

# Check novnc-desktop service status
systemctl --user status novnc-desktop

# Check ai-agent-bridge service status
systemctl status ai-agent-bridge

# View bridge logs
journalctl -u ai-agent-bridge -n 50

# Restart Pantheon session if noVNC shows black screen
systemctl --user restart pantheon-session
```

Exit the session with `exit` or Ctrl+D. The CLI's `ssh` and `agent` commands use SSH directly; this SSM session is for interactive troubleshooting when SSH fails.

### 13. Stop and start

```bash
ai-desktops stop d-a1b2c3d4    # hibernates: RAM + disk preserved
ai-desktops start d-a1b2c3d4
```

`stop` hibernates the instance — the kernel writes RAM to the encrypted root EBS volume, then the instance stops. `start` resumes it; running processes continue from where they left off. Resume typically takes 30–60 seconds.

### 14. Terminate

```bash
ai-desktops terminate d-a1b2c3d4
```

Runs `pulumi destroy` and marks the record `terminated`. If destroy fails, the instance is left running for debugging and the record is marked `failed`.

## Updating existing desktops

The desktop configuration—including Homebrew setup, GitHub known_hosts, and tool verification—is managed by Ansible and embedded in the CLI binary. To reconfigure an existing desktop with the latest configuration:

```bash
# SSH into the desktop
ai-desktops ssh d-a1b2c3d4

# Inside the desktop, re-run the Ansible playbook
cd /opt/ai-desktops/ansible
ansible-playbook playbook.yml -i inventory.ini
```

The playbook is idempotent and safe to re-run. This allows you to update existing desktops without rebuilding the AMI or recreating instances.

## Smoke test checklist

Run this against `desktops.orchael.dev` before considering the MVP complete:

- [ ] `ai-desktops bootstrap` completes without error; re-run is a no-op
- [ ] `ai-desktops init-foundation` completes; outputs include subnetId, securityGroupId, instanceProfile, zoneId
- [ ] `ai-desktops ami build --regions us-east-2` completes; `ai-desktops ami list` shows registered AMI ID (optional)
- [ ] `ai-desktops create --repos myorg/test-repo` returns a desktop ID
- [ ] `ai-desktops list` shows the new desktop in state `ready`
- [ ] `ai-desktops status <id>` shows hostname, novnc_url, ssh_target
- [ ] noVNC URL loads in browser; desktop appears
- [ ] `ai-desktops ssh <id>` drops into shell
- [ ] SSH to desktop: `docker ps` succeeds; `nvim --version` succeeds; `tmux -V` succeeds
- [ ] SSH to desktop: `ls /workspace/` shows cloned repos
- [ ] SSH to desktop: `cd /workspace/test-repo && git log --oneline -5` shows commit history
- [ ] `ai-desktops agent <id> status` returns 200 through tunnel
- [ ] `ai-desktops stop <id>` transitions to `stopped`; EC2 hibernated in console (state = stopped, RAM dump written)
- [ ] `ai-desktops start <id>` transitions back to `ready`; noVNC accessible again; running processes resumed
- [ ] SSH to desktop: `/workspace/test-repo` still present after stop/start cycle
- [ ] `ai-desktops terminate <id>` succeeds; record transitions to `terminated`; EC2 terminated in console; Route53 record removed

## Known limitations

- **Elementary/Pantheon reliability**: The `novnc-desktop` elementary AMI runs Pantheon on Ubuntu 24.04. If noVNC shows a black screen, SSH in and run `systemctl --user restart pantheon-session`.
- **Root EBS persistence**: Workspace data lives on the root EBS volume (100 GiB gp3, encrypted). EBS is preserved through stop/start but is destroyed on terminate. Commit and push work before terminating.
- **Hibernation requires new desktops**: Hibernation is configured at launch time and cannot be retrofitted onto existing instances. Desktops created before this change use regular stop/start and do not preserve RAM state.
- **Failed desktops left running**: If `terminate` fails mid-way, the EC2 instance is intentionally left running so you can SSH in to diagnose. Clean up manually with `aws ec2 terminate-instances` and `pulumi destroy` from `infra/pulumi/desktop/`.
- **Single availability zone**: Desktops land in the first public subnet from the foundation stack. Multi-AZ placement is not yet supported.
- **No desktop autostop**: There is no idle-timeout or schedule-based stop. Remember to stop or terminate desktops when not in use.

## Repository layout

```
cmd/ai-desktops/          CLI entrypoint and Cobra commands
internal/
  config/                 Config struct and YAML loader
  repo/                   GitHub URL normalization and owner-boundary validation
  store/                  DynamoDB fleet store (Desktop records, lifecycle states)
  desktop/                Manager: ID generation, stack names, lifecycle helpers
  backend/                Pulumi S3 backend bootstrap helpers
  awsx/                   AWS SDK helpers (S3, DynamoDB, EC2, SSM, Secrets Manager)
  pulumi/                 Automation API wrapper, stack config builders, output keys
  provision/              Cloud-init template renderer (embeds Ansible playbooks)
    ansible/              Post-boot configuration (embedded in CLI binary)
      desktop-setup/      Verifies tools, installs Homebrew, configures SSH
  health/                 Readiness checkers (TCP, HTTPS, custom)
  tunnel/                 SSM and SSH tunnel command builders
  agent/                  Typed HTTP client for ai-agent-bridge
  version/                Version string (overridable via ldflags)
infra/
  pulumi/foundation/      Shared VPC/IAM/DNS/SG Pulumi program (separate Go module)
  pulumi/desktop/         Per-desktop EC2/Route53 Pulumi program (separate Go module)
apps/desktop-web/         On-desktop status dashboard (Vite + React + TypeScript)
plans/                    Implementation plan files
tasks/                    Branch-local TODO tracking
```

## License

MIT License - see [LICENSE](LICENSE) file for details.
