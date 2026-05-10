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

Repositories are cloned during desktop boot using a GitHub personal access token retrieved from AWS at runtime. Store the token in AWS SSM Parameter Store or Secrets Manager before running `create`:

```bash
# SSM Parameter Store (recommended)
aws ssm put-parameter \
  --name /ai-desktops/github-pat \
  --value ghp_... \
  --type SecureString \
  --profile <your-profile>

# Or Secrets Manager
aws secretsmanager create-secret \
  --name ai-desktops/github-pat \
  --secret-string ghp_... \
  --profile <your-profile>
```

Reference the parameter name in your config file:

```yaml
github:
  owner: myorg
  pat_secret: /ai-desktops/github-pat   # SSM path or Secrets Manager name
```

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

### 1. Bootstrap Pulumi state backend

```bash
ai-desktops bootstrap
```

Creates the S3 bucket (versioning + SSE + public-access block) and the DynamoDB fleet table. Idempotent — safe to re-run.

### 2. Initialize shared foundation infrastructure

```bash
ai-desktops init-foundation
```

Creates: VPC, public subnet, internet gateway, security group (SSH from `operator_cidr`, HTTPS from anywhere), IAM role with SSM + Secrets Manager permissions, instance profile, and looks up the Route53 zone.

Preview without applying:

```bash
ai-desktops init-foundation --preview
```

### 3. Create a desktop

```bash
ai-desktops create --repos myorg/my-app,myorg/shared-lib
```

- Validates repo owner boundary (all repos must belong to `github.owner`)
- Creates a DynamoDB record in state `creating`
- Runs `pulumi up` to provision EC2, EBS, Route53 A record, and cloud-init
- Cloud-init installs tools, retrieves GitHub PAT, clones repos under `/workspace`
- Polls readiness checks (SSH, noVNC HTTPS, bridge TCP) until desktop is `ready`

### 4. Inspect fleet

```bash
ai-desktops list
ai-desktops status d-a1b2c3d4
```

### 5. Open noVNC

```bash
ai-desktops url d-a1b2c3d4
# https://d-a1b2c3d4.desktops.orchael.dev
```

### 6. SSH into desktop

```bash
ai-desktops ssh d-a1b2c3d4
```

### 7. Use AI agent bridge

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

The CLI opens an SSM port-forward tunnel (falling back to SSH) to reach the bridge at `127.0.0.1:9445` on the desktop. The bridge is never exposed publicly.

### 8. Run diagnostics

```bash
ai-desktops doctor d-a1b2c3d4
ai-desktops doctor d-a1b2c3d4 --json
```

Checks: EC2 running, SSH reachable, noVNC HTTPS responds, Docker active, bridge active.

### 9. Stop and start

```bash
ai-desktops stop d-a1b2c3d4    # EBS data preserved
ai-desktops start d-a1b2c3d4
```

### 10. Terminate

```bash
ai-desktops terminate d-a1b2c3d4
```

Runs `pulumi destroy` and marks the record `terminated`. If destroy fails, the instance is left running for debugging and the record is marked `failed`.

## Smoke test checklist

Run this against `desktops.orchael.dev` before considering the MVP complete:

- [ ] `ai-desktops bootstrap` completes without error; re-run is a no-op
- [ ] `ai-desktops init-foundation` completes; outputs include subnetId, securityGroupId, instanceProfile, zoneId
- [ ] `ai-desktops create --repos myorg/test-repo` returns a desktop ID
- [ ] `ai-desktops list` shows the new desktop in state `ready`
- [ ] `ai-desktops status <id>` shows hostname, novnc_url, ssh_target
- [ ] noVNC URL loads in browser; desktop appears
- [ ] `ai-desktops ssh <id>` drops into shell
- [ ] SSH to desktop: `docker ps` succeeds; `nvim --version` succeeds; `tmux -V` succeeds
- [ ] SSH to desktop: `ls /workspace/test-repo` shows cloned repo
- [ ] `ai-desktops agent <id> status` returns 200 through tunnel
- [ ] `ai-desktops stop <id>` transitions to `stopped`; EC2 stopped in console
- [ ] `ai-desktops start <id>` transitions back to `ready`; noVNC accessible again
- [ ] SSH to desktop: `/workspace/test-repo` still present after stop/start cycle
- [ ] `ai-desktops terminate <id>` succeeds; record transitions to `terminated`; EC2 terminated in console; Route53 record removed

## Known limitations

- **Elementary/Pantheon reliability**: The `novnc-desktop` package targets Elementary OS; on Ubuntu 22.04 some Pantheon services may be slow to start or require a session restart. If noVNC shows a black screen, SSH in and run `systemctl --user restart pantheon-session`.
- **Root EBS persistence**: Workspace data lives on the root EBS volume. EBS is preserved through stop/start but is destroyed on terminate. Commit and push work before terminating.
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
  provision/              Cloud-init template renderer
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
