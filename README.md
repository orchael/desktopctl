# desktopctl

[![CI](https://github.com/orchael/desktopctl/actions/workflows/ci.yml/badge.svg)](https://github.com/orchael/desktopctl/actions/workflows/ci.yml)
[![Release](https://github.com/orchael/desktopctl/actions/workflows/publish-cli.yml/badge.svg)](https://github.com/orchael/desktopctl/actions/workflows/publish-cli.yml)
[![License](https://img.shields.io/github/license/orchael/desktopctl)](LICENSE)
[![GitHub Release](https://img.shields.io/github/v/release/orchael/desktopctl)](https://github.com/orchael/desktopctl/releases)

A Go CLI-driven fleet manager for persistent remote AI coding desktops on AWS.

## Quick start

This walkthrough creates one AWS desktop from a fresh config. Use a region
where you want the desktop, an AWS profile for the target account, and a
subdomain you can delegate to Route 53. AWS resources created here incur charges.

### Prepare access and credentials

- Install [Go 1.26+](#tools), AWS CLI v2, Pulumi, Packer, and SSH. Configure an
  AWS CLI profile with the [required permissions](#aws-permissions). Have an
  SSH private key and matching `.pub` file on this computer; `ssh-keygen -t
  ed25519 -f "$HOME/.ssh/id_ed25519"` creates a new pair if you do not have one.
- Create a [GitHub classic personal access token](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens#creating-a-personal-access-token-classic)
  with `repo`, `admin:public_key`, and `read:user` scopes. Add `workflow` if you
  will manage GitHub Actions, `read:packages` if you will build an AMI or use
  GitHub Packages, and `read:org` if you need organization features. Setup
  stores this token and a generated GitHub SSH key in AWS Secrets Manager.
- For **Claude Code**, install the Claude CLI and run `claude setup-token` on
  your computer. Complete the browser sign-in and copy the token printed at
  the end; the command does not save it. Enter it in setup's hidden Claude
  token prompt. This requires a Claude Pro, Max, Team, or Enterprise plan.
  See [Claude Code authentication](https://code.claude.com/docs/en/authentication#generate-a-long-lived-token).
- For **Codex with ChatGPT sign-in**, install the Codex CLI and run `codex
  login`; complete the browser flow, then check `codex login status`. The
  helper in step 2 reads `${CODEX_HOME:-$HOME/.codex}/auth.json`. If Codex
  saved credentials in your OS keyring instead, configure file storage with
  `cli_auth_credentials_store = "file"` in `${CODEX_HOME:-$HOME/.codex}/config.toml`
  and sign in again. Treat `auth.json` as a password. See
  [OpenAI's Codex authentication guide](https://learn.chatgpt.com/docs/auth#credential-storage).
  Alternatively, create an [OpenAI API key](https://platform.openai.com/api-keys)
  and enter it in setup's hidden OpenAI key prompt; API usage is billed to
  your OpenAI Platform account. No Codex auth file is needed in that case.
- For **Gemini CLI**, optionally create a [Google AI Studio API key](https://ai.google.dev/gemini-api/docs/api-key)
  and enter it in setup's hidden Gemini key prompt. Agent credentials are
  optional; prepare only the providers you will use. Keep every token out of
  the config file, Git, and shell command arguments.

Public `desktopctl-v0` AMIs from the default publisher account `819363892004`
were verified in **`ca-central-1`, `us-east-1`, and `us-east-2`** on 2026-10-25.
AMIs are regional and availability can change. In a region without a public
image, build a compatible AMI there or supply one with `create --ami`; see
step 5 and the [AMI guide](docs/PUBLIC_AMIS.md). Setup accepts any supported
AWS region; its default publisher account does not guarantee an image there.

### Create your first desktop

1. **Choose the target.** Clone this repository if needed, then set your
   values and verify the AWS identity from its root directory. The S3 bucket
   name you enter during setup must be globally unique and dedicated to this
   installation.

   ```sh
   PROFILE=my-aws-profile
   REGION=us-east-1
   ENVIRONMENT=prod
   OWNER=my-github-user
   REPO="$OWNER/my-repo"
   CONFIG="$HOME/.config/desktopctl/config-${REGION}.yaml"
   aws sts get-caller-identity --profile "$PROFILE" --region "$REGION"
   make build
   ```

2. **Run interactive setup.** Enter `ENVIRONMENT`, `OWNER`, the GitHub token,
   a unique state bucket, your SSH private key path, and the detected public
   IPv4 `/32` for SSH. Enter a desktop
   DNS subdomain such as `desktops.example.com` if you want browser access.
   Setup registers the matching `.pub` key as an EC2 key pair, creates or reuses
   the Route 53 hosted zone, and writes `CONFIG`. Keep the default public AMI
   publisher unless you trust another publisher.

   ```sh
   ./bin/desktopctl --config "$CONFIG" --profile "$PROFILE" --region "$REGION" setup
   ```

   If you chose Codex ChatGPT sign-in, copy the local Codex auth file into the
   regional agent secret, then let setup record that secret path in the config.
   The helper preserves any Claude or other provider keys entered in setup:

   ```sh
   scripts/update-agent-auth.sh --profile "$PROFILE" --region "$REGION" \
     --environment "$ENVIRONMENT" --owner "$OWNER" --skip-claude
   ./bin/desktopctl --config "$CONFIG" setup --non-interactive
   ```

   Skip these two commands if you use an OpenAI API key or do not use Codex.

3. **Check and bootstrap.** Confirm the `aws`, `packer`, `pulumi`, `ssh`, and
   `aws-credentials` checks pass and that the account is the one you intended.
   Bootstrap creates the S3 state bucket for a new installation. If you are
   adopting existing resources, follow [the migration guide](docs/CA_CENTRAL_MIGRATION.md)
   for `--allow-existing` instead.

   ```sh
   ./bin/desktopctl --config "$CONFIG" check
   ./bin/desktopctl --config "$CONFIG" bootstrap
   ```

4. **Delegate DNS and create the foundation.** Add the NS records printed by
   setup at the provider for your desktop zone's parent domain. Continue when
   `dns check` reports `DNS delegation ready`; DNS updates can take time. Then
   review the Pulumi preview and create networking, IAM, DynamoDB, and EFS.

   ```sh
   ./bin/desktopctl --config "$CONFIG" dns check
   ./bin/desktopctl --config "$CONFIG" init-foundation --preview
   ./bin/desktopctl --config "$CONFIG" init-foundation
   ```

   If you left the DNS zone blank for SSH-only access, skip `dns check` and
   add `--no-dns` to both `init-foundation` commands and the `create` commands
   in step 5.

5. **Preview and create a desktop.** Set `REPO` to a repository your GitHub
   token can access. The preview reports the selected AMI; confirm
   it is available in your region before creating the EC2 instance. If no
   compatible public AMI exists there, build a private image after foundation
   setup with `./bin/desktopctl --config "$CONFIG" ami build --regions "$REGION"`
   (requires `read:packages`), or pass a compatible regional AMI ID with
   `--ami`. AMI builds need a usable base AMI and Packer subnet in that region.

   ```sh
   ./bin/desktopctl --config "$CONFIG" create --repo "$REPO" --preview
   ./bin/desktopctl --config "$CONFIG" create --repo "$REPO"
   ./bin/desktopctl --config "$CONFIG" list
   ```

   Use the desktop ID printed by `create` with `./bin/desktopctl --config
   "$CONFIG" ssh DESKTOP_ID`. With delegated DNS, its browser URL
   and SSH hostname use `<desktop-id>.<your-desktop-zone>`.

For an SSH-only installation without DNS, use `--no-dns` on both
`init-foundation` and `create`. The hostname is then just `<desktop-id>`, no
public noVNC URL is returned, and Certbot is skipped. `desktopctl ssh` uses an
SSM tunnel automatically for these desktops; AWS Session Manager must be
available. The no-DNS foundation has no inbound desktop ports. This mode does
not provide a trusted browser URL.

### Optional browser companion

On a server with this checkout, Docker Compose, your desktopctl config, and the
same AWS profile as the CLI, start the standalone companion:

```sh
desktopctl app start --source /path/to/desktopctl
```

The default `app.bind: 127.0.0.1` in the config keeps the browser app on the
server's loopback interface. Forward it from your workstation with
`ssh -L 3000:127.0.0.1:3000 user@your-server`, then open
`http://127.0.0.1:3000/`. To bind a private server IP, set `app.bind` in your
desktopctl config or pass `desktopctl app start --bind 192.168.1.20 --source
/path/to/desktopctl`. `--bind` overrides the config value. Selecting
`0.0.0.0` is allowed but prints a warning because it listens on every IPv4
interface; restrict access to your private network. Use `desktopctl app stop
--source /path/to/desktopctl` to shut down the companion.

Click a fleet row to see a short summary and lifecycle actions in the panel
beside the list. Use its details icon for the full desktop record. **Open desktop**
shows the desktop's fresh, one-session noVNC URL with its access token, linked
directly to the desktop. Use **Refresh desktop URL** for another session. A
configured desktop SSH key is required. If a Create response is interrupted,
use **Retry create request** to recover the same operation. See the
[companion guide](docs/control-plane.md) for private HTTPS proxy setup and
server requirements.

This repository remains usable on its own. The separate [Orchael Desktops](https://github.com/orchael/desktops) service will own SaaS users, organizations, customer AWS connections, and provisioning jobs. The optional [standalone browser companion](docs/control-plane.md) uses the same fleet as the CLI and does not require a SaaS account. See the [architecture and responsibility boundary](docs/ARCHITECTURE.md).

Each desktop is an EC2 instance running a full Elementary (Pantheon) desktop environment accessible via noVNC, with `bridgectl` for programmatic AI agent access and a pre-cloned developer workspace. Fleet state is tracked in DynamoDB; infrastructure is managed with Pulumi using S3 as the state backend.

## Prerequisites

### Tools

- Go 1.26+
- [Pulumi CLI](https://www.pulumi.com/docs/install/) — `curl -fsSL https://get.pulumi.com | sh`
- AWS CLI v2 — configured with a profile that has the permissions listed below
- `pnpm` — for the desktop webapp (`apps/desktop-web`) and optional companion app (`apps/control-plane-web`)

### AWS permissions

The operator profile needs the following permissions:

The foundation Pulumi stack can create a dedicated environment-specific IAM
user, assumable role, access key, and Secrets Manager credential bundle for
these permissions. See [`docs/AWS_OPERATOR_IAM.md`](docs/AWS_OPERATOR_IAM.md).

**Bootstrap (`bootstrap` command):**
- `s3:CreateBucket`, `s3:ListBucket` (for `HeadBucket` checks), `s3:GetBucketTagging`, `s3:PutBucketTagging`, `s3:PutBucketVersioning`, `s3:PutEncryptionConfiguration`, `s3:PutBucketPublicAccessBlock`
- `dynamodb:CreateTable`, `dynamodb:DescribeTable`

**Desktop DNS (setup with a domain, `dns setup`, `dns check`):**
- `route53:ListHostedZones`, `route53:GetHostedZone`; setup and `dns setup` also need `route53:CreateHostedZone` when no public zone exists.

**Foundation (`init-foundation` command):**
- `ec2:CreateVpc`, `ec2:CreateSubnet`, `ec2:CreateInternetGateway`, `ec2:CreateRouteTable`, `ec2:CreateSecurityGroup`, and associated `Describe*`/`Delete*` variants
- `iam:CreateUser`, `iam:CreateAccessKey`, `iam:CreateRole`, `iam:PutUserPolicy`, `iam:PutRolePolicy`, `iam:AttachRolePolicy`, `iam:CreateInstanceProfile`, `iam:AddRoleToInstanceProfile`, and associated `Get*`/`List*`/`Delete*`/`Tag*` variants
- `route53:GetHostedZone`, `route53:ListHostedZones`
- `secretsmanager:DescribeSecret`, `secretsmanager:CreateSecret`, `secretsmanager:PutSecretValue`, `secretsmanager:TagResource` for `/desktopctl/<env>-<region>/control-plane/aws-operator*`
- Full Pulumi S3 state backend access on the bootstrap bucket

**Desktop lifecycle (`create`, `stop`, `start`, `terminate`):**
- `ec2:RunInstances`, `ec2:StopInstances`, `ec2:StartInstances`, `ec2:TerminateInstances`, `ec2:DescribeInstances`, `ec2:ModifyInstanceAttribute`
- `ec2:CreateTags`
- `ec2:ImportKeyPair` when registering an EC2 key pair through `setup`
- `route53:ChangeResourceRecordSets`, `route53:ListResourceRecordSets`
- `dynamodb:PutItem`, `dynamodb:GetItem`, `dynamodb:UpdateItem`, `dynamodb:Scan`, `dynamodb:DeleteItem`
- `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`, `secretsmanager:PutSecretValue`, `secretsmanager:CreateSecret` on `/desktopctl/<env>/<region>/<owner>/tailscale/*` and `/desktopctl/<env>/<region>/<owner>/step-ca/*` (required when using `--tailscale` or `--step-ca`)
- `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`, `secretsmanager:PutSecretValue`, `secretsmanager:CreateSecret`, `secretsmanager:TagResource` on `/desktopctl/<env>/<region>/<owner>` (operator-only CLI secrets such as `TAILSCALE_API_KEY`)
- `TAILSCALE_API_KEY` in the local environment, or in the `/desktopctl/<env>/<region>/<owner>` operator secret, lets `terminate` remove the matching Tailscale machine when the desktop record has `tailscale_network` set. If it is absent, termination continues and the Tailscale machine must be removed manually.

**AMI build/list/delete (`ami` commands):**
- `ec2:DescribeImages`, `ec2:DescribeSubnets`, `ec2:CreateImage`, `ec2:CopyImage`, `ec2:ModifyImageAttribute`, `ec2:DeregisterImage`, `ec2:DeleteSnapshot`
- Packer also needs EC2 launch, tagging, stop, and terminate permissions for its build instance.
- `dynamodb:PutItem`, `dynamodb:GetItem`, `dynamodb:Scan`, `dynamodb:DeleteItem` on the AMI history table.

**Tunnel access (`agent` command):**
- `ssm:StartSession` with document `AWS-StartPortForwardingSession`
- `ssm:DescribeSessions`, `ssm:TerminateSession`

### Route53 prerequisites

Pass `--dns-zone desktops.example.com` to `desktopctl setup`, or answer its
interactive DNS prompt, to create or reuse a public hosted zone. The standalone
`desktopctl dns setup --zone desktops.example.com` can do this later. It saves `dns.zone` and
`dns.hosted_zone_id` in the config and prints the Route 53 name servers. Add
those values as an NS record for the subdomain at the parent domain's DNS
provider, then run `desktopctl dns check`. The check compares publicly resolved
NS records with the exact Route 53 delegation set. Foundation apply, desktop
creation, and desktop start repeat this check when DNS is enabled; they stop
before launching compute if delegation is missing or points elsewhere.
New installations also use the saved hosted zone ID for desktop records and
DNS refreshes, so duplicate hosted zone names cannot redirect writes.
Existing configs without `dns.zone` retain their legacy environment domain
until updated with `dns setup`.

### GitHub credentials

Repositories are cloned during desktop boot via SSH using a key retrieved from AWS Secrets Manager. The GitHub personal access token is also used to authenticate `gh` CLI. By default, npm scopes continue to resolve from npmjs; configure explicit GitHub Packages scopes only for desktops that need them.

Recommended token scopes (required scopes are marked):

| Scope | Required | Purpose |
|-------|----------|---------|
| `admin:public_key` | Yes | Register SSH keys |
| `repo` | Yes | Clone, push, PRs |
| `read:user` | Yes | Identity |
| `read:packages` | Optional | Install packages from GitHub Packages when `github.npm_github_scopes` or `--npm-github-scope` is used |
| `workflow` | Recommended | GitHub Actions |
| `security_events` | Recommended | Code scanning, secret scanning |
| `read:org` | Optional | `gh` CLI org features; missing scope produces a warning but auth continues |

Run `desktopctl setup` to configure credentials. The wizard:

1. Collects your AWS region/profile, GitHub owner, and git identity.
2. Generates and registers a GitHub Ed25519 SSH key pair when one is missing or rotation is requested.
3. Stores a JSON secret in AWS Secrets Manager at `/desktopctl/<env>/<region>/<owner>/github`:
   ```json
   {
     "github_token": "ghp_...",
     "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
     "ssh_public_key": "ssh-ed25519 AAAA..."
   }
   ```
4. Optionally collects AI provider API keys (Anthropic, OpenAI, Gemini) and stores them at `/desktopctl/<env>/<region>/<owner>/agents`.
5. Writes the selected config path, or `~/.config/desktopctl/config.yaml` by default on Linux.

Run `desktopctl setup` before running `create`. For the same configured region,
environment, and GitHub owner, setup reuses matching secrets and the EC2 key
pair. A secret scheduled for deletion is restored before it is read or updated.
New token and provider key values replace the corresponding stored values;
unspecified values remain. A different identity with existing names still
requires `setup --allow-existing`.

### Noninteractive setup

`setup --non-interactive` takes values from command flags, environment
variables, then the existing config, in that order. It never prompts. The
region and profile use the root `--region` and `--profile` flags. A new AWS
installation requires a GitHub token. Every scripted setup needs an SSH private
key path with a matching `.pub` file, public IPv4 `/32`, bucket, environment,
owner, region, and profile.
An existing GitHub secret can supply the token and SSH key when no replacement
is provided; use `--allow-existing` when starting from a new config and reusing
those AWS names. Set `DESKTOPCTL_GITHUB_TOKEN` to replace only the token; pass
`--rotate-github-ssh-key` to generate and register a new GitHub SSH key too.

```sh
CONFIG="$HOME/.config/desktopctl/config-prod-ca-central-1-dns.yaml"
# Supply DESKTOPCTL_GITHUB_TOKEN through your secret source when rotating it.
./bin/desktopctl --config "$CONFIG" --profile marka --region ca-central-1 \
  setup --non-interactive --allow-existing --environment prod \
  --dns-zone desktops.markcallen.dev \
  --backend-bucket marka-desktopctl-prod-ca-central-1-819363892004 \
  --github-owner orchael --operator-cidr 203.0.113.42/32 \
  --ssh-key-path "$HOME/.ssh/id_ed25519"
```

Replace the example CIDR with the public IPv4 of the machine that will use
desktop SSH. The CLI validates that it is a public `/32` and fails if any
required input is missing. It imports the local public key into EC2 under the
configured or derived key name. It also restores matching scheduled secrets
and merges supplied agent keys without clearing other stored keys.

| Input | Flag | Environment variable |
| --- | --- | --- |
| AWS region and profile | `--region`, `--profile` | `DESKTOPCTL_REGION`, `DESKTOPCTL_PROFILE` |
| Environment and state bucket | `--environment`, `--backend-bucket` | `DESKTOPCTL_ENVIRONMENT`, `DESKTOPCTL_BACKEND_BUCKET` |
| Desktop DNS zone | `--dns-zone` | `DESKTOPCTL_DNS_ZONE` |
| Trusted public AMI publisher (defaults to `819363892004`) | `--public-ami-owner` | `DESKTOPCTL_PUBLIC_AMI_OWNER` |
| GitHub owner and token | `--github-owner`, `--github-token` | `DESKTOPCTL_GITHUB_OWNER`, `DESKTOPCTL_GITHUB_TOKEN` |
| Git author | `--git-user-name`, `--git-user-email` | `DESKTOPCTL_GIT_USER_NAME`, `DESKTOPCTL_GIT_USER_EMAIL` |
| Desktop SSH | `--operator-cidr`, `--ssh-key-path`, `--ssh-key-name` | `DESKTOPCTL_OPERATOR_CIDR`, `DESKTOPCTL_SSH_KEY_PATH`, `DESKTOPCTL_SSH_KEY_NAME` |
| Agent credentials | `--claude-oauth-token`, `--openai-api-key`, `--gemini-api-key` | `CLAUDE_CODE_OAUTH_TOKEN`, `OPENAI_API_KEY`, `GEMINI_API_KEY` |

Use environment variables for credentials to keep them out of shell history
and process arguments. Setup does not print credential values. CLI input flags
work for automation systems that provide protected command arguments.

At desktop boot, cloud-init retrieves the JSON secret, installs the SSH private key at `/home/ubuntu/.ssh/github_ed25519`, and configures SSH to use it for `github.com`. Repos are then cloned via `git@github.com:<owner>/<repo>.git`.

### Codex and Claude Code Agent Auth

`desktopctl setup` stores provider API keys in AWS Secrets Manager, but Codex ChatGPT auth and Claude Code long-lived OAuth tokens have their own local credential flows:

- With file-based credential storage, Codex CLI writes auth state to `auth.json` under `CODEX_HOME`, or `~/.codex/auth.json` when `CODEX_HOME` is unset. Its OS keyring setting does not provide the file this helper needs.
- Claude Code's `claude setup-token` command prints a long-lived `CLAUDE_CODE_OAUTH_TOKEN`; Anthropic's Claude Code docs state that command does not save the token, so copy it when it is printed.

Use the helper script to merge these credentials into `/desktopctl/<env>/<region>/<owner>/agents` without overwriting unrelated keys:

```bash
# Default owner is markcallen and default Codex auth path is ${CODEX_HOME:-$HOME/.codex}/auth.json.
scripts/update-agent-auth.sh --environment prod --region ca-central-1 --owner orchael

# Equivalent explicit form for the markcallen agent secret:
scripts/update-agent-auth.sh \
  --owner markcallen \
  --secret-id /desktopctl/markcallen/agents \
  --codex-auth-json ~/.codex/auth.json \
  --region us-east-1
```

The script reads `CLAUDE_CODE_OAUTH_TOKEN` from the current environment when set. If it is not set, it guides you to run `claude setup-token` and paste the printed token into a hidden prompt. Use `--skip-codex` or `--skip-claude` to update only one credential.

New desktops track `github.agent_secret` together with additional `--secret` paths. After updating the AWS secret, run `desktopctl secrets reload <desktop-id>` to fetch and replace the complete credential snapshot. Reload stops active bridge/provider processes, clears the desktop-local Codex auth caches, and starts the bridge with the replacement credentials. Start or resume sessions afterward. An already inactive bridge stays stopped, and the command reports that you need to start it. A failed secret fetch leaves the existing credentials and service untouched. Older desktops whose fleet record does not include the agent secret must first register it with `desktopctl secrets add <desktop-id> /desktopctl/<env>/<region>/<owner>/agents` to rotate it. Until registered, application-secret operations preserve the existing `agents.env` rather than clearing or replacing it.

Custom `github.agent_secret` and `github.github_secret` paths are preserved when `--github-owner` changes the repository owner. Conventional `/desktopctl/<env>/<region>/<owner>/agents` and `/github` paths follow the selected owner. This allows the same operator credentials to bootstrap desktops for different repository owners while each desktop keeps its own refreshed auth file.

With the companion `bridgectl` auth lifecycle fix installed, Codex prefers an existing account auth file, then the `CODEX_AUTH` bootstrap seed, then API-key credentials. Each desktop owns its refreshed file under its private home directory; ordinary sessions and bridge restarts preserve it. Source selection checks credential structure; a revoked account still requires renewed login or explicit credential rotation, and tasks are not replayed automatically with a different identity. Explicit `CODEX_HOME` values used by desktop secret rotation must be private absolute directories under the desktop user's home, not an EFS workspace.

`secrets add`, `remove`, and `reload` coordinate across operator machines using a desktop-held lock. Overlapping commands fail immediately with a busy error; they do not queue another rotation. The lock covers the fresh fleet snapshot, remote changes, and a fenced update of only the secret-path metadata. If a command or connection is interrupted, run `desktopctl secrets reload <desktop-id>` to reconcile against the authoritative fleet list before trying add/remove again. Recovery can clear a partially injected snapshot even when that list is empty. If reload is still busy, the earlier remote operation is still running; wait for it to finish. Do not remove `~/.desktopctl-secret-operation/lock`: its inode coordinates running processes and records unfinished rotations without storing credentials. Upgrade all CLI and control-plane processes that write fleet records; older versions bypass coordination and metadata guards. An ordinary fleet update whose snapshot became stale during rotation fails safely and can be retried after refreshing the record.

Before rotation changes files or stops sessions, it validates the replacement `CODEX_AUTH` structure, existing auth-directory/file ownership and private permissions, and that the bridge systemd unit is loaded. Invalid credentials or unsafe cache paths leave the previous snapshot untouched. A failure after mutation has begun may leave a partial snapshot; fix the reported preconditions and use `secrets reload` to reconcile. Credential values and child-process diagnostics are not printed.

The auth update script fails closed if an existing Secrets Manager value cannot be read; only an explicit AWS `ResourceNotFoundException` permits creating a new secret. To update Codex auth and immediately reload one desktop while preserving every other provider key, run `scripts/update-agent-auth.sh --skip-claude --reload-desktop <desktop-id> --yes`. Before reload, the CLI verifies that the exact updated secret path is tracked for the desktop and that its resolved AWS region matches the desktop. Older desktops must register the path with `desktopctl secrets add` first. A reload failure is returned to the caller and must be reconciled before sessions resume.

Known reload failures are reported through allowlisted non-secret categories, while raw child output remains suppressed. New AMIs and fallback cloud-init create `/home/ubuntu/.codex` as an ubuntu-owned `0700` directory; `desktopctl doctor` checks that invariant. Existing desktops with a more permissive directory must be corrected explicitly before credential rotation.

A present empty or whitespace-only `CODEX_AUTH` string is invalid, even when an API key is also configured. To intentionally switch to API-key-only credentials, remove `CODEX_AUTH` from the configured secret sources instead of setting it to a blank string, then reload. An absent seed remains supported.

Reload also preflights all credential-output paths (`agents.env`, `desktop-secrets.conf`, `.desktop-secrets`) and `.bashrc` before reading or staging them. From the desktop home downward, existing parent directories must be real, owned by the desktop user, and not writable by group/others. Safe `0755` directories are allowed and left unchanged; new directories are created with `0700`. Existing credential files must be private, user-owned regular files. Symlinks, shared NFS/EFS directories or file-level mounts, and unsafe ownership/permissions are rejected without automatically chmodding or moving anything. Correct the unsafe path explicitly, then retry reload.

When adding the shell-source hook, reload preserves the existing safe permissions of `.bashrc` (for example, `0644`); a newly created `.bashrc` and all credential snapshots remain `0600`.

### Desktop Secret Management

The configured `github.agent_secret` and secrets passed with `desktopctl create --secret <path>` are tracked together in the fleet record but rendered to separate scopes. The agent secret is written only to `/home/ubuntu/.config/bridgectl/agents.env`. Additional secret paths are written only to `/home/ubuntu/.desktop-secrets` and `/home/ubuntu/.config/environment.d/desktop-secrets.conf`; they remain available to login shells and user services, including bridgectl. When both scopes define the same key, the additional desktop secret takes precedence for bridgectl because its systemd environment file is loaded after `agents.env`. Creation and `secrets reload` both preserve this boundary. Human-readable `desktopctl status` reports the tracked agent path separately as `Agent secret`; its `Secrets` line contains only the additional desktop-wide paths.

Manage those per-desktop secret references after creation with:

```bash
desktopctl secrets add d-a1b2c3d4 /desktopctl/myorg/app
desktopctl secrets reload d-a1b2c3d4
desktopctl secrets remove d-a1b2c3d4 /desktopctl/myorg/app
```

`secrets add` verifies each new AWS Secrets Manager path exists before injection. `secrets remove` rewrites the desktop environment files without the removed paths and clears them when no configured secrets remain. The desktop must be running and reachable over SSH for these commands.

The opt-in [AWS E2E runner](tests/e2e/README.md) tests Codex auth using this checkout's CLI and a locally built Linux bridgectl binary. It creates a dedicated workspace and desktop for this repository, cleans up after success, and retains failed runs. Use `--keep` to retain a successful run and `--reuse <state.json>` to test the same resources again.

## Installation

```bash
git clone https://github.com/orchael/desktopctl
cd desktopctl
make build
./bin/desktopctl --help
```

Or build directly:

```bash
mkdir -p bin
go build -o bin/desktopctl ./cmd/desktopctl
```

`go install ./cmd/desktopctl` installs the same `desktopctl` command in your Go bin directory.

## Releases

The existing `Publish` GitHub Actions workflow accepts a patch, minor, or major release type. It updates the version files, creates a `v<version>` tag, and calls the CLI publish workflow. GoReleaser builds `desktopctl` archives for Linux, macOS, and Windows, publishes them to GitHub Releases, and writes `desktopctl-checksums.txt`. Verify that the release archives contain `desktopctl` before announcing a renamed release. Existing `desktopctl` installations need to be removed from user `PATH` separately; the new release does not install an alias.

The public `orchael/homebrew-desktopctl` tap receives a `desktopctl` formula from GoReleaser during the same release. The `HOMEBREW_TAP_GITHUB_TOKEN` secret on `orchael/desktopctl` must be a fine-grained token with Contents read/write access to this tap; the release workflow checks the token and tap branch before creating a version tag. After the first formula is published, install it with `brew install orchael/desktopctl/desktopctl` on macOS or Linux. GitHub Releases contain the same versioned binary archives and checksums.

The optional companion app runs from a source checkout on the operator's server. See [standalone app setup](docs/control-plane.md).

## Region and environment mapping

| Environment | AWS Region  |
|-------------|-------------|
| `prod`      | us-east-1   |
| `dev`       | us-east-2   |
| `test`      | us-west-2   |

Each environment gets its own foundation stack and DynamoDB fleet table. Set
`dns.zone` per installation with `dns setup`; existing configs without it use
the legacy Orchael zone for their environment.

## Optional browser companion

The standalone companion lets operators create and manage the same fleet through a private browser UI. It is separate from the Orchael Desktops SaaS. Install Docker Compose and keep a desktopctl source checkout on the server, then run `desktopctl app start --source /path/to/desktopctl`. Use `desktopctl app stop` to stop it. The app listens on `127.0.0.1:3000` and requires SSH forwarding or another private network proxy; it does not use Google sign-in. See [the setup and security guide](docs/control-plane.md).

## Configuration

Create `~/.config/desktopctl/config.yaml` (or pass `--config <path>`):

```yaml
aws:
  region: us-east-2   # us-east-1 = prod, us-east-2 = dev, us-west-2 = test
  profile: myprofile

pulumi:
  backend_bucket: my-desktopctl-pulumi-state

fleet:
  table_name: desktopctl-fleet-dev-us-east-2
  environment: dev    # prod | dev | test

github:
  owner: myorg
  github_secret: /desktopctl/dev/us-east-2/myorg/github
  # Optional: only scopes listed here resolve from GitHub Packages.
  # Leave unset so packages such as @myorg/package resolve from npmjs.
  # npm_github_scopes:
  #   - private-tools

operator:
  # Operator-only CLI secret; not needed by desktop cloud-init.
  secret: /desktopctl/myorg

desktop:
  instance_type: t3.xlarge
  operator_cidr: 203.0.113.42/32   # your public IP
  # profile: myorg/desktop-profile
  # profile_secret: /desktopctl/myorg/profiles/desktop-profile

agent:
  bridge_port: 9445
```

See `config.example.yaml` at the repo root for a fully commented example.
For optional desktop setup bundles and their dedicated environment-variable secrets, see [desktop profiles](docs/DESKTOP_PROFILES.md).

## Full lifecycle workflow

### 1. Configure the Pulumi state backend

Set the S3 bucket name in your config file before running any infrastructure commands:

```yaml
pulumi:
  backend_bucket: <globally-unique-bucket-name>   # e.g. myorg-desktopctl-pulumi-state
```

The bucket name must be globally unique across all AWS accounts. Pick something like
`<your-org>-desktopctl-pulumi-state`.

> **Enforced by the CLI**: `init-foundation`, `create`, and `terminate` check that this
> bucket exists in AWS before doing any work. If the bucket is missing they will print
> the exact command needed to create it.

### 2. Bootstrap Pulumi state backend

```bash
desktopctl bootstrap
```

Creates a fresh S3 bucket using AWS API calls (not Pulumi). Configures versioning, encryption (SSE), and public-access block. Use `bootstrap --allow-existing` only for an intentional existing setup. This step must complete before Pulumi can initialize.

For complete teardown of a dedicated installation, including the setup EC2 key
pair, GitHub and agent secrets, foundation stack, configured Route 53 hosted
zone, and versioned state bucket,
use the preview-first [`cleanup-installation.py`](scripts/cleanup-installation.py)
wrapper. See the [ca-central-1 cleanup guide](docs/CA_CENTRAL_MIGRATION.md#remove-an-installation)
for prerequisites, safety checks, and the apply command. The wrapper verifies
terminated desktop records and empty Pulumi stacks before clearing their
versioned state. It reports custom EC2 key pair and secret names for separate
cleanup while continuing with the foundation.

### 3. Initialize shared foundation infrastructure

```bash
desktopctl init-foundation
```

Creates: VPC, public subnet, internet gateway, security group (SSH from `operator_cidr`, HTTPS from anywhere), IAM role with SSM + Secrets Manager permissions, instance profile, environment-specific control-plane IAM credentials in Secrets Manager, and looks up the Route53 zone.

For a DNS-backed foundation, the security group allows TCP 443 and 8443 by default. The desktop noVNC URL uses 8443; opening 443 does not configure a service or certificate on that port. To allow additional public TCP ports, repeat `--allow-port` or pass a comma-separated list:

```bash
desktopctl init-foundation --allow-port 3000 --allow-port 9000
desktopctl init-foundation --allow-port 3000,9000
```

Pass the same ports on later `init-foundation` updates; omitting the flag removes previously configured additional ports. `--allow-port` is unavailable with `--saas` or `--no-dns`. SSH port 22 remains controlled by `desktop.operator_cidr`.

Preview without applying:

```bash
desktopctl init-foundation --preview
```

### 4. Build a pre-baked AMI

Pre-baked AMIs reduce desktop boot time by pre-installing the toolchain and configuring
the `ubuntu` user's pinned NvChad/Catppuccin and tmux/TPM workflow. Desktop creation
requires an active AMI for its AWS region; cloud-init handles runtime-only work such as
TLS setup, secret injection, workspace creation, and repository cloning.

#### Prerequisites

- [Packer](https://www.packer.com/downloads) installed
- AWS credentials configured (same profile as above)
- A Packer variables file at `packer/variables.pkrvars.hcl` (see below)
- A GitHub token with `read:packages`, stored by `desktopctl setup` in the configured regional secret or supplied as `GITHUB_NPM_TOKEN`

#### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_NPM_TOKEN` | No | Overrides the GitHub token stored by `desktopctl setup`. The token used for the build must have `read:packages` scope. |

Create a GitHub personal access token (classic) or fine-grained token with at minimum:

| Scope | Purpose |
|-------|---------|
| `read:packages` | Download `@orchael/desktopctl` from `npm.pkg.github.com` |

```bash
export GITHUB_NPM_TOKEN=ghp_...
```

The build uses `GITHUB_NPM_TOKEN` when set; otherwise it reads `github_token` from the region and environment scoped secret created by `desktopctl setup`.

#### Packer variables file

The CLI looks for `packer/variables.pkrvars.hcl` by default (override with `--vars-file`). At minimum it must define:

```hcl
# packer/variables.pkrvars.hcl
aws_region              = "us-east-2"   # dev region; use us-east-1 for prod, us-west-2 for test
bridgectl_version       = "v1.4.8"
tailscale_version       = "1.98.9"
helm_version            = "v4.3.1"
helm_sha256             = "0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b"
playwright_version      = "1.63.0"
go_version              = "1.26.0"
uv_version              = "0.12.3"
```

Keep `bridgectl_version` synchronized with `BridgectlVersion` in `internal/provision/cloudinit.go` (currently `v1.4.8`). Rebuild the CLI and the AMI together when changing this pin: pre-baked desktops report a provisioning error if the installed package differs from the CLI's expectation. Changing the pin does not upgrade existing desktops or replace their standalone bridgectl configuration. The E2E runner's explicit `--bridgectl-binary` still overrides the installed binary for branch testing. The `helm_sha256` pin must match the official `helm_version` linux-amd64 release archive.

The AMI also bakes Playwright `1.63.0` and its matching Chromium and headless browser into `/opt/desktopctl/playwright-browsers`. `PLAYWRIGHT_BROWSERS_PATH` is set for desktop logins and systemd services; project tests still use their own `@playwright/test` dependency. To reuse the baked browser without downloads, pin the project to the same Playwright version and run its normal test command. A different Playwright version may require a different browser revision; install that revision into a project-specific path or update the AMI pin and rebuild. The Packer bake launches headless Chromium as `ubuntu`, and a fresh desktop should be checked with a minimal project-local test using `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` and network access disabled. Existing desktops need replacement with the new AMI. GitHub-hosted CI continues installing Chromium separately.

The following variables are **injected automatically** by the CLI and must not be set in the vars file:

| Variable | Source |
|----------|--------|
| `source_ami` | Resolved in the first build region from the latest `novnc-desktop-ubuntu-24.04-elementary` AMI (or `--base-ami`) |
| `ami_regions` | All requested regions after the first; Packer copies the built AMI to them |
| `aws_profile` | Selected CLI AWS profile; Packer uses the same account as the CLI lookup |
| `desktop_web_version` | Embedded in the CLI binary at release time |
| `desktopctl_version` | Embedded in the CLI binary at release time |
| `github_npm_token` | Mapped from `GITHUB_NPM_TOKEN` or the setup GitHub secret |

#### Build the AMI

Private `ami build` records AMI history and active pins in the desktopctl
foundation and config, so initialize the foundation before a private build:

```bash
desktopctl init-foundation
```

```bash
desktopctl ami build --regions us-east-2
```

Build once in the first region and copy the completed AMI to the other regions:

```bash
desktopctl ami build --regions us-east-1,us-east-2,us-west-2
```

Use an explicit base AMI in the first build region instead of the auto-lookup:

```bash
desktopctl ami build --regions us-east-2 --base-ami ami-0123456789abcdef0
```

Make the built AMI publicly accessible without a desktopctl foundation or
config. Supply `GITHUB_NPM_TOKEN` with `read:packages` when no setup secret is
configured; pass `--subnet-id` if the first region has no default VPC:

```bash
desktopctl ami build --regions us-east-2 --public
```

Public builds print each regional AMI ID and do not use AMI history. They save
active pins only when `--config` is explicitly supplied.
Public images are named `desktopctl-v<CLI major>-<UTC YYYYMMDD>-<build timestamp>`.
Automatic discovery follows the running CLI's major version; `desktop.public_ami_major`
selects another major and `desktop.active_ami.<region>` pins an exact image.
Use [the public AMI lifecycle guide](docs/PUBLIC_AMIS.md) for the regional build,
listing, and preview-first deletion script.

#### What gets installed

The AMI is built on top of the latest `novnc-desktop-ubuntu-24.04-elementary` base in the first region, then copied to the remaining regions. Each region receives its own AMI ID. The image includes:

- Docker
- Go (version from `go_version` var)
- uv Python package manager (version from `uv_version` var)
- AWS CLI v2
- neovim (via snap)
- Homebrew
- Helm (verified release archive; version and SHA-256 from `helm_version` and `helm_sha256` vars)
- tfenv (via Homebrew) with Terraform 1.16.5 installed and selected (version from `terraform_version` var)
- Visual Studio Code stable (Microsoft's signed apt repository; version from `vscode_version` var)
- `bridgectl` (version from `bridgectl_version` var)
- Tailscale (version from `tailscale_version` var)
- `@orchael/desktopctl` npm package (version from `desktop_web_version` var)
- Android Studio (via snap)
- Android SDK with platforms `android-34` (including Google Play Store system image `x86_64`), build-tools 35.0.1 and 37.0.0, and NDK 27.0.12077973
- pyenv (installed for the `ubuntu` user; Python version management at runtime)

Publish the new `@orchael/desktopctl` package before baking an AMI that installs
it. Existing AMIs keep their installed desktop web package until rebuilt.
On an existing desktop, `desktopctl update-web --version <version>` or
`desktopctl update-web --local` installs the new package and updates an older
service unit before restarting it.

The built AMI is tagged with the CLI version that created it (`DesktopctlVersion`) and the component versions for traceability.

#### Verify the AMI

```bash
desktopctl ami list
```

Output shows the registered AMI ID per region. The CLI automatically detects pre-baked AMIs and uses them during `create`.

#### Custom Packer builds

Edit `packer/ubuntu-desktop.pkr.hcl` to change versions or add packages. Re-run `ami build` to create a new AMI.

### 5. Create a desktop

```bash
desktopctl create --repo myorg/my-app --repo myorg/shared-lib
```

To open additional public TCP ports for this desktop only, use `--allow-port`.
Repeat the flag or use a comma-separated list; for example,
`desktopctl create --repo myorg/my-app --allow-port 3000 --allow-port 9000`.
The desktop stack owns a separate security group for these ports and removes it
when the desktop is terminated. Opening a port does not start a service or set
up TLS. SSH port 22 stays restricted by `desktop.operator_cidr`.

For a durable background job, pass a stable `--desktop-id d-0123abcd`. A repeated ID is rejected before another stack can be created. The [worker contract](docs/PROVISIONING_CONTRACT.md) covers temporary STS credentials, retries, and the required account setup. Check the effective AWS identity with `desktopctl aws validate --account-id <12-digit-account-id>` before provisioning.

Or use full GitHub URLs:

```bash
desktopctl create --github-owner myorg --repo https://github.com/myorg/my-app --repo https://github.com/myorg/shared-lib
```

Override the root volume size (default 100 GiB):

```bash
desktopctl create --repo myorg/my-app --volume-size 200
```

Name a desktop for easier operator workflows:

```bash
desktopctl create --name orchael-factory-dev --repo myorg/my-app
```

By default `/workspace` lives on the desktop root EBS volume and is deleted when
the desktop is terminated. To retain `/workspace` across desktop replacement,
create an EFS-backed workspace first, then attach it during desktop creation:

```bash
desktopctl workspace create \
  --name orchael-factory-dev \
  --github-owner myorg \
  --repo myorg/my-app

desktopctl create \
  --name orchael-factory-dev \
  --github-owner myorg \
  --workspace-mode efs \
  --workspace-name orchael-factory-dev \
  --repo myorg/my-app
```

Each environment has one shared encrypted EFS file system with AWS Backup
enabled by default. Named workspaces are isolated with EFS access points. A
workspace can be attached to only one non-terminated desktop at a time; stopped
desktops keep the attachment lock. `terminate` releases the lock but always
retains the EFS workspace until `desktopctl workspace delete <name>` is run.
To change the repo set for a retained workspace, first detach or terminate the
desktop, then update the workspace metadata:

```bash
desktopctl workspace add-repo orchael-factory-dev --repo myorg/new-service
desktopctl workspace remove-repo orchael-factory-dev --repo myorg/old-service
```

Removing a repo from workspace metadata does not delete any existing directory
or files from EFS. The updated repo set is enforced on the next
`desktopctl create --workspace-mode efs` call.

npm scoped packages resolve from npmjs unless a scope is explicitly configured
for GitHub Packages. Use `github.npm_github_scopes` in config for a default, or
pass a scope for a single desktop:

```bash
desktopctl create --repo myorg/my-app --npm-github-scope @private-tools
```

To ignore configured GitHub Packages scopes for one desktop:

```bash
desktopctl create --repo myorg/my-app --no-npm-github-scopes
```

- Validates repo owner boundary (all repos must belong to the same GitHub owner)
- Creates a DynamoDB record in state `creating`
- Prints the Pulumi stack name to run next: `cd infra/pulumi/desktop && pulumi stack select <stack> && pulumi up`
- Cloud-init installs tools, retrieves GitHub PAT from AWS Secrets Manager/SSM, and clones repos under `/workspace`

### 6. Inspect fleet

```bash
desktopctl list
desktopctl list --all
desktopctl status d-a1b2c3d4
desktopctl workspace list
desktopctl workspace status orchael-factory-dev
```

`list` hides terminated desktop records by default. Use `list --all` to include them.

Delete accumulated terminated records after reviewing them:

```bash
desktopctl purge --dry-run
desktopctl purge
```

### 7. Open noVNC

```bash
desktopctl url d-a1b2c3d4
# https://d-a1b2c3d4.desktops.orchael.dev
```

### 8. SSH into desktop

```bash
desktopctl ssh d-a1b2c3d4
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
INSTANCE_ID=$(desktopctl status d-a1b2c3d4 | grep "Instance ID" | awk -F: '{print $2}' | xargs)
aws ssm start-session --target $INSTANCE_ID --region us-east-2  # adjust region for your environment
# Then inside the session:
tail -100 /var/log/cloud-init-output.log
```

### 10. Use AI agent bridge

```bash
# Status and available providers
desktopctl agent d-a1b2c3d4 status
desktopctl agent d-a1b2c3d4 providers

# Start an agent session
desktopctl agent d-a1b2c3d4 start --provider claude --repo myorg/my-app

# List active sessions
desktopctl agent d-a1b2c3d4 sessions

# Stop a session
desktopctl agent d-a1b2c3d4 stop <session-id>
```

The CLI connects directly to the desktop via SSH. The bridge is accessed over `localhost:9445` on the desktop itself.

### 11. Optional private network and step-ca registration

Attach a desktop to Tailscale by passing `--tailscale` and providing an auth key through the local environment or an existing integration secret. When `TAILSCALE_AUTHKEY` is set, the CLI stores it in AWS Secrets Manager and cloud-init retrieves it at boot. If `TAILSCALE_AUTHKEY` is not set, the CLI reuses the existing secret at `/desktopctl/<env>/<region>/<owner>/tailscale/<tailnet>` when it contains `TS_AUTHKEY`.

When `TAILSCALE_API_KEY` is set during create, the CLI stores it in the operator-only secret at `/desktopctl/<env>/<region>/<owner>` as `TAILSCALE_API_KEY`. This key is not used by desktop startup; it is used later by `terminate` to remove the desktop's Tailscale machine record.

Operator secrets are tagged with `desktopctl-scope=operator`; the foundation instance role denies desktop instances from reading secrets with that tag.

```bash
export TAILSCALE_AUTHKEY=tskey-auth-...

desktopctl create \
  --github-owner myorg \
  --tailscale \
  --tailscale-network my-tailnet
```

You can also set `network.tailscale_network` in `config.yaml` and use `--tailscale` without `--tailscale-network`. Setting the config value alone does not attach every new desktop to Tailscale.

Desktops join Tailscale with Tailscale SSH enabled. The tailnet policy must still allow SSH to the auth-key tag used for desktops, for example:

```json
{
  "tagOwners": {
    "tag:desktopctl": ["autogroup:admin"]
  },
  "ssh": [
    {
      "action": "accept",
      "src": ["autogroup:admin"],
      "dst": ["tag:desktopctl"],
      "users": ["ubuntu"]
    }
  ]
}
```

Register the bridgectl agent server with a step-ca server by passing the CA DNS name. If the CA is only reachable on Tailscale, use `--tailscale` too; cloud-init waits for Tailscale to be running and for the CA DNS name to resolve before configuring step-ca. When `STEP_CA_PROVISIONER_PASSWORD` is set, the CLI stores it in AWS Secrets Manager. If it is not set, the CLI reuses the existing secret at `/desktopctl/<env>/<region>/<owner>/step-ca/<server>` when it contains `STEP_CA_PROVISIONER_PASSWORD`. A CA fingerprint is required and can be supplied with `--step-ca-fingerprint`, `STEP_CA_FINGERPRINT`, or `pki.step_ca_fingerprint`.

When both Tailscale and step-ca are enabled, cloud-init also rewrites `~/.config/bridgectl/config.yaml` so `server.listen` binds to the desktop's Tailscale IPv4 address on the configured bridge port, and `server.san` includes the desktop's Tailscale DNS name. Tailscale-only desktops keep the safer localhost-only listener.

Remote `bridgectl` clients also need JWT trust in addition to Step CA client certificates. Add known clients in config under `pki.step_ca_clients`, or pass them at create time:

```yaml
pki:
  step_ca_clients:
    - issuer: mark-macbook
      public_key_path: /Users/mark/.config/bridgectl/certs/jwt-signing.pub
      required: true
```

```bash
desktopctl create \
  --step-ca ca.my-tailnet.ts.net \
  --step-ca-client issuer=mark-macbook,public-key-path=/Users/mark/.config/bridgectl/certs/jwt-signing.pub,required=true
```

The CLI reads each public key locally during `create`, copies it to `/home/ubuntu/.config/bridgectl/certs/jwt-clients/<issuer>.pub`, and adds a matching `step_ca.clients` entry to `/home/ubuntu/.config/bridgectl/config.yaml`. Do not provide a JWT private key.

```bash
export TAILSCALE_AUTHKEY=tskey-auth-...
export STEP_CA_PROVISIONER_PASSWORD=...
export STEP_CA_FINGERPRINT=...

desktopctl create \
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
desktopctl doctor d-a1b2c3d4
desktopctl doctor d-a1b2c3d4 --json
```

Checks include EC2 state, SSH and noVNC reachability, Docker and bridge health, and private native Codex-home ownership and permissions.

### 13. Debug with SSM (if diagnostics fail)

If `doctor` reports issues, use AWS Systems Manager Session Manager to open an interactive shell on the instance for debugging:

```bash
# Get the instance ID from the status output
INSTANCE_ID=$(desktopctl status d-a1b2c3d4 | grep instance_id | cut -d: -f2 | xargs)

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
desktopctl stop d-a1b2c3d4    # hibernates: RAM + disk preserved
desktopctl start d-a1b2c3d4
```

`stop` hibernates the instance — the kernel writes RAM to the encrypted root EBS volume, then the instance stops. `start` resumes it; running processes continue from where they left off. Resume typically takes 30–60 seconds.

### 14. Terminate

```bash
desktopctl terminate d-a1b2c3d4
```

`desktopctl destroy d-a1b2c3d4` is an equivalent alias.

Runs `pulumi destroy` and marks the record `terminated`. If destroy fails, the instance is left running for debugging and the record is marked `failed`.

For desktops created with `--tailscale` or `--tailscale-network`, `terminate` also tries to remove the matching Tailscale machine before destroying the Pulumi stack. Set `TAILSCALE_API_KEY` to a Tailscale API key with device management access, or store it in the operator-only secret at `/desktopctl/<env>/<region>/<owner>` as `TAILSCALE_API_KEY`, to enable this cleanup. If the key is not set or cleanup fails, the CLI warns and continues with infrastructure termination; remove the stale Tailscale machine manually from the admin console or API.

## Updating existing desktops

The desktop configuration—including Homebrew setup, GitHub known_hosts, and tool verification—is managed by Ansible and embedded in the CLI binary. To reconfigure an existing desktop with the latest configuration:

```bash
# SSH into the desktop
desktopctl ssh d-a1b2c3d4

# Inside the desktop, re-run the Ansible playbook
ansible-playbook /opt/desktopctl/desktop-setup.yml \
  -i localhost, -c local
```

The playbook is idempotent and safe to re-run. This allows you to update existing desktops without rebuilding the AMI or recreating instances.

## Smoke test checklist

Run this against `desktops.orchael.dev` before considering the MVP complete:

- [ ] `desktopctl bootstrap` completes without error; re-run is a no-op
- [ ] `desktopctl init-foundation` completes; outputs include subnetId, securityGroupId, instanceProfile, zoneId
- [ ] `desktopctl ami build --regions us-east-2` completes; `desktopctl ami list` shows registered AMI ID (optional — dev region)
- [ ] `desktopctl create --repos myorg/test-repo` returns a desktop ID
- [ ] `desktopctl list` shows the new desktop in state `ready`
- [ ] `desktopctl status <id>` shows hostname, novnc_url, ssh_target
- [ ] noVNC URL loads in browser; desktop appears
- [ ] `desktopctl ssh <id>` drops into shell
- [ ] SSH to desktop: `docker ps` succeeds; `nvim --version` succeeds; `tmux -V` succeeds
- [ ] SSH to desktop: `ls /workspace/` shows cloned repos
- [ ] SSH to desktop: `cd /workspace/test-repo && git log --oneline -5` shows commit history
- [ ] `desktopctl agent <id> status` returns 200 through tunnel
- [ ] `desktopctl stop <id>` transitions to `stopped`; EC2 hibernated in console (state = stopped, RAM dump written)
- [ ] `desktopctl start <id>` transitions back to `ready`; noVNC accessible again; running processes resumed
- [ ] SSH to desktop: `/workspace/test-repo` still present after stop/start cycle
- [ ] `desktopctl terminate <id>` succeeds; record transitions to `terminated`; EC2 terminated in console; Route53 record removed

## Known limitations

- **Elementary/Pantheon reliability**: The `novnc-desktop` elementary AMI runs Pantheon on Ubuntu 24.04. If noVNC shows a black screen, SSH in and run `systemctl --user restart pantheon-session`.
- **Root EBS persistence**: By default, workspace data lives on the root EBS volume (100 GiB gp3, encrypted). EBS is preserved through stop/start but is destroyed on terminate. Commit and push work before terminating local-workspace desktops.
- **EFS workspace retention**: EFS workspaces are opt-in with `--workspace-mode efs`. They survive desktop termination and must be deleted explicitly with `desktopctl workspace delete <name>`.
- **Hibernation requires new desktops**: Hibernation is configured at launch time and cannot be retrofitted onto existing instances. Running `desktopctl stop` on a desktop created before this change will fail because the instance was not launched with hibernation enabled. Recreate the desktop with `desktopctl terminate` followed by `desktopctl create`.
- **Failed desktops left running**: If `terminate` fails mid-way, the EC2 instance is intentionally left running so you can SSH in to diagnose. Clean up manually with `aws ec2 terminate-instances` and `pulumi destroy` from `infra/pulumi/desktop/`.
- **Single availability zone**: Desktops land in the first public subnet from the foundation stack. Multi-AZ placement is not yet supported.
- **No desktop autostop**: There is no idle-timeout or schedule-based stop. Remember to stop or terminate desktops when not in use.

## Repository layout

```
cmd/desktopctl/          CLI entrypoint and Cobra commands
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
  agent/                  Typed HTTP client for bridgectl
  version/                Version string (overridable via ldflags)
infra/
  pulumi/foundation/      Shared VPC/IAM/DNS/SG Pulumi program (separate Go module)
  pulumi/desktop/         Per-desktop EC2/Route53 Pulumi program (separate Go module)
apps/desktop-web/         On-desktop status dashboard (Vite + React + TypeScript)
apps/control-plane-web/   Standalone browser companion (Next.js)
plans/                    Implementation plan files
tasks/                    Branch-local TODO tracking
```

## License

MIT License - see [LICENSE](LICENSE) file for details.
