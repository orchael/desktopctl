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
- `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`, `secretsmanager:PutSecretValue`, `secretsmanager:CreateSecret` on `/ai-desktops/<owner>/tailscale/*` and `/ai-desktops/<owner>/step-ca/*` (required when using `--tailscale` or `--step-ca`)
- `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`, `secretsmanager:PutSecretValue`, `secretsmanager:CreateSecret`, `secretsmanager:TagResource` on `/ai-desktops/<owner>` (operator-only CLI secrets such as `TAILSCALE_API_KEY`)
- `TAILSCALE_API_KEY` in the local environment, or in the `/ai-desktops/<owner>` operator secret, lets `terminate` remove the matching Tailscale machine when the desktop record has `tailscale_network` set. If it is absent, termination continues and the Tailscale machine must be removed manually.

**Tunnel access (`agent` command):**
- `ssm:StartSession` with document `AWS-StartPortForwardingSession`
- `ssm:DescribeSessions`, `ssm:TerminateSession`

### Route53 prerequisites

A hosted zone for the desktop DNS domain must already exist before running `init-foundation`:

- **Production** (`--env prod`): zone for `desktops.orchael.com`
- **Development** (`--env dev`): zone for `desktops.orchael.dev`

The foundation stack looks up the zone by domain name and exports the zone ID. If the zone is missing, `init-foundation` will fail with a clear error.

### GitHub credentials

Repositories are cloned during desktop boot via SSH using a key retrieved from AWS Secrets Manager. The GitHub personal access token is also used to authenticate `gh` CLI and write `/home/ubuntu/.npmrc` so the ubuntu user can install packages from GitHub Packages (e.g. `@<owner>/*` scoped packages).

Recommended token scopes (required scopes are marked):

| Scope | Required | Purpose |
|-------|----------|---------|
| `admin:public_key` | Yes | Register SSH keys |
| `repo` | Yes | Clone, push, PRs |
| `read:user` | Yes | Identity |
| `read:packages` | Yes | Install packages from GitHub Packages |
| `workflow` | Recommended | GitHub Actions |
| `security_events` | Recommended | Code scanning, secret scanning |
| `read:org` | Optional | `gh` CLI org features; missing scope produces a warning but auth continues |

Run `ai-desktops setup` to configure credentials. The wizard:

1. Collects your AWS region/profile, GitHub owner, and git identity.
2. Generates an Ed25519 SSH key pair and registers the public key with GitHub.
3. Stores a JSON secret in AWS Secrets Manager at `/ai-desktops/<owner>/github`:
   ```json
   {
     "github_token": "ghp_...",
     "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
     "ssh_public_key": "ssh-ed25519 AAAA..."
   }
   ```
4. Optionally collects AI provider API keys (Anthropic, OpenAI, Gemini) and stores them at `/ai-desktops/<owner>/agents`.
5. Writes `~/.ai-desktops/config.yaml`.

Run `ai-desktops setup` before running `create`. It is safe to re-run — it prompts whether to rotate an existing token or SSH key.

At desktop boot, cloud-init retrieves the JSON secret, installs the SSH private key at `/home/ubuntu/.ssh/github_ed25519`, and configures SSH to use it for `github.com`. Repos are then cloned via `git@github.com:<owner>/<repo>.git`.

### Codex and Claude Code Agent Auth

`ai-desktops setup` stores provider API keys in AWS Secrets Manager, but Codex ChatGPT auth and Claude Code long-lived OAuth tokens have their own local credential flows:

- Codex CLI writes auth state to `auth.json` under `CODEX_HOME`, or `~/.codex/auth.json` when `CODEX_HOME` is unset.
- Claude Code's `claude setup-token` command prints a long-lived `CLAUDE_CODE_OAUTH_TOKEN`; Anthropic's Claude Code docs state that command does not save the token, so copy it when it is printed.

Use the helper script to merge these credentials into `/ai-desktops/<owner>/agents` without overwriting unrelated keys:

```bash
# Default owner is markcallen and default Codex auth path is ${CODEX_HOME:-$HOME/.codex}/auth.json.
scripts/update-agent-auth.sh --region us-east-1

# Equivalent explicit form for the markcallen agent secret:
scripts/update-agent-auth.sh \
  --owner markcallen \
  --secret-id /ai-desktops/markcallen/agents \
  --codex-auth-json ~/.codex/auth.json \
  --region us-east-1
```

The script reads `CLAUDE_CODE_OAUTH_TOKEN` from the current environment when set. If it is not set, it guides you to run `claude setup-token` and paste the printed token into a hidden prompt. Use `--skip-codex` or `--skip-claude` to update only one credential.

Existing desktops do not automatically re-fetch `/ai-desktops/<owner>/agents`; recreate the desktop or restart/reload the `bridgectl` user service after updating `/home/ubuntu/.config/bridgectl/agents.env` on the instance.

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

## Region and environment mapping

| Environment | AWS Region  |
|-------------|-------------|
| `prod`      | us-east-1   |
| `dev`       | us-east-2   |
| `test`      | us-west-2   |

Each environment gets its own foundation stack, DynamoDB fleet table, and DNS zone. The `prod` and `dev` environments use separate hosted zones (`desktops.orchael.com` and `desktops.orchael.dev`); the `test` environment shares the `dev` zone.

## Configuration

Create `~/.ai-desktops/config.yaml` (or pass `--config <path>`):

```yaml
aws:
  region: us-east-2   # us-east-1 = prod, us-east-2 = dev, us-west-2 = test
  profile: myprofile

pulumi:
  backend_bucket: my-ai-desktops-pulumi-state

fleet:
  table_name: ai-desktops-fleet-dev
  environment: dev    # prod | dev | test

github:
  owner: myorg
  github_secret: /ai-desktops/myorg/github

operator:
  # Operator-only CLI secret; not needed by desktop cloud-init.
  secret: /ai-desktops/myorg

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

#### Prerequisites

- [Packer](https://www.packer.com/downloads) installed
- AWS credentials configured (same profile as above)
- A Packer variables file at `packer/variables.pkrvars.hcl` (see below)
- The `GITHUB_NPM_TOKEN` environment variable set (see below)

#### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_NPM_TOKEN` | Yes | GitHub token used to install `@markcallen/desktop-web` from GitHub Packages during the AMI build. Must have `read:packages` scope. |

Create a GitHub personal access token (classic) or fine-grained token with at minimum:

| Scope | Purpose |
|-------|---------|
| `read:packages` | Download `@markcallen/desktop-web` from `npm.pkg.github.com` |

```bash
export GITHUB_NPM_TOKEN=ghp_...
```

The build will fail with a clear error if `GITHUB_NPM_TOKEN` is not set before running `ami build`.

#### Packer variables file

The CLI looks for `packer/variables.pkrvars.hcl` by default (override with `--vars-file`). At minimum it must define:

```hcl
# packer/variables.pkrvars.hcl
aws_region              = "us-east-2"   # dev region; use us-east-1 for prod, us-west-2 for test
ai_agent_bridge_version = "v0.11.0"
tailscale_version       = "1.98.9"
go_version              = "1.24.0"
uv_version              = "0.12.3"
```

The following variables are **injected automatically** by the CLI and must not be set in the vars file:

| Variable | Source |
|----------|--------|
| `source_ami` | Resolved at build time from the latest public `novnc-desktop-ubuntu-24.04-elementary` AMI (or `--base-ami`) |
| `desktop_web_version` | Embedded in the CLI binary at release time |
| `ai_desktops_version` | Embedded in the CLI binary at release time |
| `github_npm_token` | Mapped from `GITHUB_NPM_TOKEN` environment variable |

#### Build the AMI

The foundation stack must already be applied for the configured environment and
AWS control region because `ami build` records AMI history in the foundation
DynamoDB AMI table:

```bash
ai-desktops init-foundation
```

```bash
export GITHUB_NPM_TOKEN=ghp_...
ai-desktops ami build --regions us-east-2
```

Build all three regions:

```bash
ai-desktops ami build --regions us-east-1,us-east-2,us-west-2
```

Use an explicit base AMI instead of the auto-lookup (single region only):

```bash
ai-desktops ami build --regions us-east-2 --base-ami ami-0123456789abcdef0
```

Make the built AMI publicly accessible:

```bash
ai-desktops ami build --regions us-east-2 --public
```

#### What gets installed

The AMI is built on top of the latest public `novnc-desktop-ubuntu-24.04-elementary` base and includes:

- Docker
- Go (version from `go_version` var)
- uv Python package manager (version from `uv_version` var)
- AWS CLI v2
- neovim (via snap)
- Homebrew
- `ai-agent-bridge` (version from `ai_agent_bridge_version` var)
- Tailscale (version from `tailscale_version` var)
- `@markcallen/desktop-web` npm package (version from `desktop_web_version` var)
- Android Studio (via snap)
- Android SDK with platforms `android-34` (including Google Play Store system image `x86_64`), build-tools 35.0.1 and 37.0.0, and NDK 27.0.12077973
- pyenv (installed for the `ubuntu` user; Python version management at runtime)

The built AMI is tagged with the CLI version that created it (`AiDesktopsVersion`) and the component versions for traceability.

#### Verify the AMI

```bash
ai-desktops ami list
```

Output shows the registered AMI ID per region. The CLI automatically detects pre-baked AMIs and uses them during `create`.

#### Custom Packer builds

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
aws ssm start-session --target $INSTANCE_ID --region us-east-2  # adjust region for your environment
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

### 11. Optional private network and step-ca registration

Attach a desktop to Tailscale by passing `--tailscale` and providing an auth key through the local environment or an existing integration secret. When `TAILSCALE_AUTHKEY` is set, the CLI stores it in AWS Secrets Manager and cloud-init retrieves it at boot. If `TAILSCALE_AUTHKEY` is not set, the CLI reuses the existing secret at `/ai-desktops/<owner>/tailscale/<tailnet>` when it contains `TS_AUTHKEY`.

When `TAILSCALE_API_KEY` is set during create, the CLI stores it in the operator-only secret at `/ai-desktops/<owner>` as `TAILSCALE_API_KEY`. This key is not used by desktop startup; it is used later by `terminate` to remove the desktop's Tailscale machine record.

Operator secrets are tagged with `ai-desktops-scope=operator`; the foundation instance role denies desktop instances from reading secrets with that tag.

```bash
export TAILSCALE_AUTHKEY=tskey-auth-...

ai-desktops create \
  --github-owner myorg \
  --tailscale \
  --tailscale-network my-tailnet
```

You can also set `network.tailscale_network` in `config.yaml` and use `--tailscale` without `--tailscale-network`. Setting the config value alone does not attach every new desktop to Tailscale.

Desktops join Tailscale with Tailscale SSH enabled. The tailnet policy must still allow SSH to the auth-key tag used for desktops, for example:

```json
{
  "tagOwners": {
    "tag:ai-desktop": ["autogroup:admin"]
  },
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:admin"],
      "dst": ["tag:ai-desktop"],
      "users": ["ubuntu"]
    }
  ]
}
```

Register the bridgectl agent server with a step-ca server by passing the CA DNS name. If the CA is only reachable on Tailscale, use `--tailscale` too; cloud-init waits for Tailscale to be running and for the CA DNS name to resolve before configuring step-ca. When `STEP_CA_PROVISIONER_PASSWORD` is set, the CLI stores it in AWS Secrets Manager. If it is not set, the CLI reuses the existing secret at `/ai-desktops/<owner>/step-ca/<server>` when it contains `STEP_CA_PROVISIONER_PASSWORD`. A CA fingerprint is required and can be supplied with `--step-ca-fingerprint`, `STEP_CA_FINGERPRINT`, or `pki.step_ca_fingerprint`.

When both Tailscale and step-ca are enabled, cloud-init also rewrites `~/.config/bridgectl/config.yaml` so `server.listen` binds to the desktop's Tailscale IPv4 address on the configured bridge port, and `server.san` includes the desktop's Tailscale DNS name. Tailscale-only desktops keep the safer localhost-only listener.

Remote `bridgectl` clients also need JWT trust in addition to Step CA client certificates. Add known clients in config under `pki.step_ca_clients`, or pass them at create time:

```yaml
pki:
  step_ca_clients:
    - issuer: mark-macbook
      public_key_path: /Users/mark/.ai-agent-bridge/certs/jwt-signing.pub
      required: true
```

```bash
ai-desktops create \
  --step-ca ca.my-tailnet.ts.net \
  --step-ca-client issuer=mark-macbook,public-key-path=/Users/mark/.ai-agent-bridge/certs/jwt-signing.pub,required=true
```

The CLI reads each public key locally during `create`, copies it to `/home/ubuntu/.ai-agent-bridge/certs/jwt-clients/<issuer>.pub`, and adds a matching `step_ca.clients` entry to `/home/ubuntu/.config/bridgectl/config.yaml`. Do not provide a JWT private key.

```bash
export TAILSCALE_AUTHKEY=tskey-auth-...
export STEP_CA_PROVISIONER_PASSWORD=...
export STEP_CA_FINGERPRINT=...

ai-desktops create \
  --github-owner myorg \
  --tailscale \
  --tailscale-network my-tailnet \
  --step-ca ca.my-tailnet.ts.net \
  --step-ca-provisioner admin
```

Config defaults are available as `pki.step_ca_server`, `pki.step_ca_provisioner`, `pki.step_ca_fingerprint`, and `pki.step_ca_clients`. CLI flags override config values for a single desktop, and repeated `--step-ca-client` values append to the configured client list. When `--tailscale` is passed and `pki.step_ca_server` is configured, step-ca is enabled from config as part of the private-network setup.

`doctor` adds Tailscale and step-ca checks only for desktops created with those integrations enabled.

### 12. Run diagnostics

```bash
ai-desktops doctor d-a1b2c3d4
ai-desktops doctor d-a1b2c3d4 --json
```

Checks: EC2 running, SSH reachable, noVNC HTTPS responds, Docker active, bridge active.

### 13. Debug with SSM (if diagnostics fail)

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

# Check bridgectl user service status
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) systemctl --user status bridgectl

# View bridge logs
sudo -u ubuntu env XDG_RUNTIME_DIR=/run/user/$(id -u ubuntu) journalctl --user -u bridgectl -n 50

# Restart Pantheon session if noVNC shows black screen
systemctl --user restart pantheon-session
```

Exit the session with `exit` or Ctrl+D. The CLI's `ssh` and `agent` commands use SSH directly; this SSM session is for interactive troubleshooting when SSH fails.

### 14. Stop and start

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

For desktops created with `--tailscale` or `--tailscale-network`, `terminate` also tries to remove the matching Tailscale machine before destroying the Pulumi stack. Set `TAILSCALE_API_KEY` to a Tailscale API key with device management access, or store it in the operator-only secret at `/ai-desktops/<owner>` as `TAILSCALE_API_KEY`, to enable this cleanup. If the key is not set or cleanup fails, the CLI warns and continues with infrastructure termination; remove the stale Tailscale machine manually from the admin console or API.

## Updating existing desktops

The desktop configuration—including Homebrew setup, GitHub known_hosts, and tool verification—is managed by Ansible and embedded in the CLI binary. To reconfigure an existing desktop with the latest configuration:

```bash
# SSH into the desktop
ai-desktops ssh d-a1b2c3d4

# Inside the desktop, re-run the Ansible playbook
ansible-playbook /opt/ai-desktops/desktop-setup.yml \
  -i localhost, -c local
```

The playbook is idempotent and safe to re-run. This allows you to update existing desktops without rebuilding the AMI or recreating instances.

## Smoke test checklist

Run this against `desktops.orchael.dev` before considering the MVP complete:

- [ ] `ai-desktops bootstrap` completes without error; re-run is a no-op
- [ ] `ai-desktops init-foundation` completes; outputs include subnetId, securityGroupId, instanceProfile, zoneId
- [ ] `ai-desktops ami build --regions us-east-2` completes; `ai-desktops ami list` shows registered AMI ID (optional — dev region)
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
- **Hibernation requires new desktops**: Hibernation is configured at launch time and cannot be retrofitted onto existing instances. Running `ai-desktops stop` on a desktop created before this change will fail because the instance was not launched with hibernation enabled. Recreate the desktop with `ai-desktops terminate` followed by `ai-desktops create`.
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
