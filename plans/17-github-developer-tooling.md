# Plan 17 — GitHub Developer Tooling on the Desktop

## Objective

Equip every managed desktop with a fully authenticated `gh` CLI, SSH-based git credentials,
a configured git commit identity, and `python3` so that a developer can commit and push code,
create and merge PRs, inspect and debug GitHub Actions, manage Dependabot, and run the
`github-health-check` skill without any additional setup.

This plan also introduces a new `ai-desktops setup` command that walks the operator through
first-time configuration: writing `config.yaml`, generating a per-owner Ed25519 SSH key pair,
registering the public key with GitHub, and storing the key and GitHub token together in a
single AWS Secrets Manager secret at `/ai-desktops/<owner>/github`.

## Depends On

- Plan 08 (cloud-init bootstrap — provides the secret injection path)
- Plan 14 (pre-baked AMI — toolchain installation target)
- FR-5.1 (git already required on every desktop)

---

## Background

The `github-health-check` Ballast skill covers 15 checks using `gh api`, `git`, and `python3`.
Each check maps to a developer workflow the desktop must support:

| Skill check | Developer workflow |
|---|---|
| Actions status, workflow health | Debug CI failures |
| Branch freshness vs release | Assess release readiness |
| Open PRs, PR status rollup | Review and land PRs |
| Dependabot PR auto-merge | Manage dependency upgrades |
| Code coverage (Codecov) | Check test quality |
| GitHub Code Quality | Review static analysis findings |
| Security feature enablement | Audit repo security posture |
| Dependabot / code scanning / secret scanning alerts | Triage security findings |
| Snyk integration | Inspect third-party vulnerability scanning |
| Branch protection rules | Verify merge gate configuration |
| Stale branches | Clean up old work |
| Repository housekeeping | Verify repo health files |
| Release and tag health | Manage versioned releases |
| Actions permissions and secrets | Audit workflow credentials |
| Public/private best practices | Full repo hygiene review |

All 15 checks require `gh` authenticated. Checks 7 and 10 also use `python3` inline for JSON
parsing and date arithmetic. `git push` requires non-interactive credentials — SSH with a
pre-installed key eliminates the `.netrc` workaround used today.

---

## Secret Design

### Current state

```
/ai-desktops/github/pat    ← SSM Parameter Store, plain-text PAT, shared by all owners
```

### New state

```
/ai-desktops/<owner>/github  ← AWS Secrets Manager, JSON, one secret per GitHub owner
```

**Secret JSON schema:**

```json
{
  "github_token":    "ghp_...",
  "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----",
  "ssh_public_key":  "ssh-ed25519 AAAA... ai-desktops/<owner>"
}
```

`github_token` is used by `gh auth login` on the desktop and by the `setup` command to
register the SSH key via the GitHub API.  
`ssh_private_key` is written to `/home/ubuntu/.ssh/github_ed25519` at boot; git clones use it.  
`ssh_public_key` is informational — stored for reference and for re-registration if needed.

**Why Secrets Manager (not SSM Parameter Store)?**  
A multi-field JSON document maps naturally to a single Secrets Manager secret. SSM could hold
three separate SecureString parameters, but one atomic unit is easier to rotate, reference from
IAM, and retrieve in a single API call.

**Why per-owner?**  
Different GitHub owners (orgs/users) require different credentials. Per-owner secrets make it
safe to operate multiple owner scopes from the same AWS account without mixing credentials.

---

## GitHub Token Scope Requirements

The token stored in the secret must have all of the following scopes:

| Scope | Required for |
|---|---|
| `admin:public_key` | Register SSH key during `setup` |
| `repo` | Clone repos, push, PR create/merge |
| `workflow` | `gh run list`, `gh run view`, workflow dispatch |
| `security_events` | Code scanning and secret scanning APIs |
| `read:user` | `gh auth status` identity check |

The operator creates this token once per GitHub owner during `ai-desktops setup`.

---

## New Command: `ai-desktops setup`

A new interactive wizard command that replaces the current manual `config.yaml` editing step.
It is the recommended first command for a new installation.

### Wizard flow

```
$ ai-desktops setup

Welcome to ai-desktops setup.
This wizard will create ~/.ai-desktops/config.yaml and provision your GitHub credentials.

── AWS ──────────────────────────────────────────────
AWS region [us-east-1]: us-east-2
AWS profile [default]: myprofile
Pulumi state S3 bucket: myorg-ai-desktops-pulumi-state
Fleet environment (dev/prod) [dev]: dev

── GitHub ───────────────────────────────────────────
GitHub owner (org or user): orchael

Before continuing, create a GitHub personal access token with these scopes:
  • admin:public_key  (register SSH keys)
  • repo              (clone, push, PRs)
  • workflow          (GitHub Actions)
  • security_events   (code scanning, secret scanning)
  • read:user         (identity)

Create the token at: https://github.com/settings/tokens/new
Paste the token here (input hidden): ****

── Generating SSH key ───────────────────────────────
Generating Ed25519 SSH key pair for owner: orchael
  Private key: (stored in AWS Secrets Manager only)
  Public key:  ssh-ed25519 AAAA... ai-desktops/orchael

Registering SSH public key with GitHub account 'orchael'...  ✓

── Storing secret ───────────────────────────────────
Storing secret at /ai-desktops/orchael/github in us-east-2...  ✓

── Writing config ───────────────────────────────────
Writing ~/.ai-desktops/config.yaml...  ✓

Setup complete. Next steps:
  ai-desktops bootstrap       # create S3 Pulumi state bucket
  ai-desktops init-foundation # deploy shared AWS infrastructure
  ai-desktops create --github-owner orchael --repo myorg/myrepo
```

### Implementation: `cmd/ai-desktops/cmd/setup.go`

```go
// Wizard steps:
// 1. Collect AWS config fields interactively (with defaults from env / existing config)
// 2. Collect GitHub owner
// 3. Print token scope requirements and URL; prompt for token (masked input)
// 4. Validate token: call GET https://api.github.com/user with the token; fail if invalid
// 5. Generate crypto/ed25519 key pair using crypto/rand
// 6. Marshal private key as OpenSSH PEM (golang.org/x/crypto/ssh)
// 7. Format public key as authorized_keys line
// 8. Register public key via POST https://api.github.com/user/keys
//    {"title": "ai-desktops/<owner>", "key": "<public key line>"}
// 9. Store JSON secret in AWS Secrets Manager at /ai-desktops/<owner>/github
// 10. Write config.yaml with GitHubSecret: /ai-desktops/<owner>/github
// 11. Print next-steps summary
```

The token validation (step 4) happens before key generation so the operator gets a clear error
if the token is wrong or missing a required scope.

### Re-run behaviour

If `~/.ai-desktops/config.yaml` already exists, `setup` prompts:
```
config.yaml already exists. Overwrite? [y/N]:
```
If a secret already exists at `/ai-desktops/<owner>/github`, `setup` prompts:
```
Secret /ai-desktops/orchael/github already exists. Rotate keys? [y/N]:
```
Rotation generates a new key pair and updates the secret atomically; it does not delete the old
key from GitHub (the operator should remove the old key manually from GitHub settings).

---

## Config Changes

### `internal/config/config.go`

Rename `PATSecret` to `GitHubSecret` in `GitHubConfig`; add backward-compatibility alias so
old configs with `pat_secret` still load without error:

```go
type GitHubConfig struct {
    Owner         string `yaml:"owner"`
    // GitHubSecret is the AWS Secrets Manager secret path holding the JSON blob
    // with github_token, ssh_private_key, and ssh_public_key.
    // Default: /ai-desktops/<owner>/github (populated by Defaults() using Owner).
    GitHubSecret  string `yaml:"github_secret"`
    // PATSecret is the legacy field name. Loaded if github_secret is absent.
    // Deprecated: use github_secret set by `ai-desktops setup`.
    PATSecret     string `yaml:"pat_secret,omitempty"`
}
```

In `Defaults()`, replace the hardcoded `/ai-desktops/github/pat` default:

```go
if c.GitHub.GitHubSecret == "" {
    if c.GitHub.PATSecret != "" {
        // Migrate legacy config: treat pat_secret as the secret path
        c.GitHub.GitHubSecret = c.GitHub.PATSecret
    } else if c.GitHub.Owner != "" {
        c.GitHub.GitHubSecret = "/ai-desktops/" + c.GitHub.Owner + "/github"
    } else {
        c.GitHub.GitHubSecret = "/ai-desktops/github/pat" // fallback for zero-config
    }
}
```

### `config.example.yaml`

Replace:

```yaml
  pat_secret: /ai-desktops/github/pat
```

with:

```yaml
  # AWS Secrets Manager secret path for this GitHub owner's credentials.
  # Created automatically by `ai-desktops setup`. Contains github_token,
  # ssh_private_key, and ssh_public_key as a JSON document.
  github_secret: /ai-desktops/myorg/github
```

---

## Cloud-Init Changes

### `internal/provision/cloudinit.go`

**`BootstrapConfig`**: rename `PATSecretPath` → `GitHubSecretPath`.

**Replace** the current `.netrc`-based HTTPS clone block with an SSH-key-based block:

```bash
# --- retrieve GitHub credentials and configure SSH ---
- |
  set -e
  REGION="{{ .AWSRegion }}"
  SECRET="{{ .GitHubSecretPath }}"
  WORKSPACE="{{ .WorkspacePath }}"
  OWNER="{{ .GitHubOwner }}"

  # Retrieve JSON secret from Secrets Manager
  SECRET_JSON=$(aws secretsmanager get-secret-value \
    --region "$REGION" \
    --secret-id "$SECRET" \
    --query SecretString \
    --output text)

  if [ -z "$SECRET_JSON" ]; then
    echo "ERROR: could not retrieve secret from $SECRET" >&2
    exit 1
  fi

  GITHUB_TOKEN=$(echo "$SECRET_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['github_token'])")
  SSH_KEY=$(echo "$SECRET_JSON" | python3 -c "import json,sys; print(json.load(sys.stdin)['ssh_private_key'])")
  unset SECRET_JSON

  # Install SSH private key for github.com
  install -d -m 700 /home/ubuntu/.ssh
  printf '%s\n' "$SSH_KEY" > /home/ubuntu/.ssh/github_ed25519
  chmod 600 /home/ubuntu/.ssh/github_ed25519
  chown ubuntu:ubuntu /home/ubuntu/.ssh/github_ed25519
  unset SSH_KEY

  # Configure SSH to use the key for github.com
  cat > /home/ubuntu/.ssh/config <<'SSHCONF'
Host github.com
  IdentityFile ~/.ssh/github_ed25519
  StrictHostKeyChecking accept-new
  User git
SSHCONF
  chmod 600 /home/ubuntu/.ssh/config
  chown ubuntu:ubuntu /home/ubuntu/.ssh/config

  # Authenticate gh CLI as ubuntu user
  sudo -u ubuntu bash -c "echo \"${GITHUB_TOKEN}\" | gh auth login --with-token"

  # Configure git commit identity
  sudo -u ubuntu git config --global user.name  "AI Desktop ({{ .DesktopID }})"
  sudo -u ubuntu git config --global user.email "desktop-{{ .DesktopID }}@noreply.github.com"

  unset GITHUB_TOKEN

# --- clone repositories ---
{{ range .Repos }}
- |
  set -e
  OWNER="{{ $.GitHubOwner }}"
  WORKSPACE="{{ $.WorkspacePath }}"
  REPO_URL="{{ . }}"
  REPO_NAME=$(basename "$REPO_URL" .git)
  REPO_OWNER=$(echo "$REPO_URL" | sed 's|.*github.com[/:]||' | cut -d/ -f1)

  if [ "$REPO_OWNER" != "$OWNER" ]; then
    echo "ERROR: repo $REPO_URL owner $REPO_OWNER does not match desktop owner $OWNER" >&2
    exit 1
  fi

  DEST="$WORKSPACE/$REPO_NAME"
  if [ ! -d "$DEST/.git" ]; then
    sudo -u ubuntu git clone "git@github.com:${OWNER}/${REPO_NAME}.git" "$DEST"
  fi
{{ end }}
```

Key changes from current implementation:
- Secret source: Secrets Manager JSON → no more plain-text SSM Parameter
- Credentials: SSH key → no more `.netrc`
- Clone URL: `git@github.com:owner/repo.git` → no more `https://github.com/...`
- `gh auth login` now runs at boot → desktop is ready for all `gh` commands immediately
- `StrictHostKeyChecking accept-new` accepts github.com's host key on first connect without prompting

---

## AMI Changes

### `packer/scripts/toolchain.sh`

Add to the toolchain install block:

```bash
# GitHub CLI
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
  | sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg 2>/dev/null
echo "deb [arch=$(dpkg --print-architecture) \
  signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] \
  https://cli.github.com/packages stable main" \
  | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null
sudo apt-get update -qq
sudo apt-get install -y gh

# python3 is present on Ubuntu 24.04 Noble; pin explicitly for AMI self-documentation
sudo apt-get install -y python3
```

---

## Readiness Check Changes

### `internal/health/health.go`

Add two new checks to the `readinessChecks` slice:

```go
{name: "gh-auth",  fn: checkGHAuth},
{name: "python3",  fn: checkPython3},
```

```go
func checkGHAuth(ctx context.Context, c *ssh.Client) CheckResult {
    out, err := runSSH(ctx, c, "sudo -u ubuntu gh auth status 2>&1")
    if err != nil {
        return CheckResult{Name: "gh-auth", OK: false, Detail: out}
    }
    return CheckResult{Name: "gh-auth", OK: true}
}

func checkPython3(ctx context.Context, c *ssh.Client) CheckResult {
    out, err := runSSH(ctx, c, "python3 --version 2>&1")
    if err != nil {
        return CheckResult{Name: "python3", OK: false, Detail: out}
    }
    return CheckResult{Name: "python3", OK: true, Detail: out}
}
```

---

## Integration Tests

New file `integration/github_tooling_test.go` (gated by `AI_DESKTOPS_RUN_INTEGRATION=true`):

| Test | SSH command | Pass condition |
|---|---|---|
| `TestFR11_GHInstalled` | `which gh && gh --version` | exits 0 |
| `TestFR11_GHAuthStatus` | `sudo -u ubuntu gh auth status` | exits 0 |
| `TestFR11_GitIdentityName` | `sudo -u ubuntu git config --global user.name` | non-empty |
| `TestFR11_GitIdentityEmail` | `sudo -u ubuntu git config --global user.email` | non-empty |
| `TestFR11_Python3Installed` | `python3 --version` | exits 0 |
| `TestFR11_SSHKeyPresent` | `test -f /home/ubuntu/.ssh/github_ed25519` | exits 0 |
| `TestFR11_SSHConfigPresent` | `grep -q github.com /home/ubuntu/.ssh/config` | exits 0 |
| `TestFR11_GHPRList` | `sudo -u ubuntu gh pr list --help` | exits 0 |
| `TestFR11_GHRunList` | `sudo -u ubuntu gh run list --limit 1 --json status,conclusion` | exits 0 |
| `TestFR11_DependabotAPIReachable` | `sudo -u ubuntu gh api /repos/OWNER/REPO/dependabot/alerts?state=open&per_page=1` | 0 or HTTP 403, not 404 |
| `TestFR11_CodeScanningAPIReachable` | `sudo -u ubuntu gh api /repos/OWNER/REPO/code-scanning/alerts?state=open&per_page=1` | 0 or 403 |
| `TestFR11_SecretScanningAPIReachable` | `sudo -u ubuntu gh api /repos/OWNER/REPO/secret-scanning/alerts?state=open&per_page=1` | 0 or 403 |
| `TestFR11_BranchProtectionAPIReachable` | `sudo -u ubuntu gh api /repos/OWNER/REPO/branches/main/protection` | 0 or 403 |
| `TestFR11_GitPushCredentials` | `sudo -u ubuntu git -C /workspace/REPO push --dry-run` | exits 0, no credential prompt |

---

## Files Affected

| File | Change |
|---|---|
| `cmd/ai-desktops/cmd/setup.go` | **New** — interactive setup wizard |
| `cmd/ai-desktops/cmd/root.go` | Register `setup` subcommand |
| `internal/config/config.go` | `PATSecret` → `GitHubSecret`; backward-compat migration in `Defaults()` |
| `internal/config/config_test.go` | Tests for migration, new default path |
| `config.example.yaml` | Replace `pat_secret` with `github_secret` |
| `internal/provision/cloudinit.go` | `PATSecretPath` → `GitHubSecretPath`; replace `.netrc` block with SSH key block |
| `internal/provision/cloudinit_test.go` | Update rendered template assertions |
| `internal/health/health.go` | Add `checkGHAuth`, `checkPython3` readiness checks |
| `internal/health/health_test.go` | Unit tests for new check functions |
| `packer/scripts/toolchain.sh` | Add `gh` and `python3` install steps |
| `integration/github_tooling_test.go` | **New** — `TestFR11_*` integration tests |
| `PRD.md` | FR-11 requirements and acceptance criteria (done in this branch) |

---

## Acceptance Criteria

A desktop passes FR-11 when all of the following hold after `create` completes:

1. `gh` is on PATH and `gh auth status` exits 0 reporting the GitHub owner's account.
2. `/home/ubuntu/.ssh/github_ed25519` exists with permissions 600.
3. `/home/ubuntu/.ssh/config` contains a `Host github.com` stanza pointing to the key.
4. `git config --global user.name` and `user.email` are non-empty.
5. `python3` is on PATH.
6. `git push --dry-run` in a cloned workspace repo exits 0 without credential prompts.
7. All `gh api` calls to Dependabot, code-scanning, secret-scanning, and branch-protection endpoints return 0 or HTTP 403 — never HTTP 404.

Setup is complete when:

8. `ai-desktops setup` writes a valid `~/.ai-desktops/config.yaml` with `github_secret: /ai-desktops/<owner>/github`.
9. The SSH public key appears in the GitHub account's SSH key list after `setup` completes.
10. The secret at `/ai-desktops/<owner>/github` in AWS Secrets Manager contains `github_token`, `ssh_private_key`, and `ssh_public_key` fields.

---

## Open Questions for Review

1. **SSH key scope**: The SSH key is registered as a **user SSH key** on the GitHub account, giving it access to all repos owned by that account. This is the simplest path. An alternative is **per-repo deploy keys**, which are more narrowly scoped but require one registration per repository. Which do you prefer?

2. **Token rotation**: The plan stores the initial registration token permanently as the ongoing API token. If the operator wants to use a narrower token for day-to-day operations (dropping `admin:public_key` after setup), the secret JSON could be updated manually. Should `setup` offer to generate a second, narrower token for runtime use?

3. **Old `pat_secret` configs**: Existing `~/.ai-desktops/config.yaml` files using `pat_secret` will silently migrate via `Defaults()`. The old SSM parameter at `/ai-desktops/github/pat` is not deleted automatically. Should the `setup` command warn when it detects a legacy config and offer to clean up the old parameter?

4. **`StrictHostKeyChecking accept-new`**: This trusts github.com's host key on first connect. The known alternative is pre-seeding `known_hosts` with GitHub's published host keys in the AMI. Pre-seeding is more secure (eliminates a TOFU window) but adds an AMI maintenance step. Preference?

5. **`python3` availability**: Ubuntu 24.04 Noble ships `python3` in the base system. The explicit install in the AMI script is redundant but self-documenting. Remove it and rely on the base image, or keep it?

---

## Verification

```bash
# Unit tests
go test ./internal/config/... ./internal/health/... ./internal/provision/...

# Integration tests (requires a live desktop fixture)
AI_DESKTOPS_RUN_INTEGRATION=true go test ./integration/... -run TestFR11 -v

# Manual setup wizard test
go run ./cmd/ai-desktops setup

# Manual spot-check on a running desktop
ssh ubuntu@<hostname> '
  gh auth status
  python3 --version
  git config --global user.name
  ls -la ~/.ssh/github_ed25519
  sudo -u ubuntu gh api /repos/orchael/ai-desktops/dependabot/alerts?state=open&per_page=1
'
```
