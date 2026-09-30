# Desktop profiles

Desktop profiles are optional configuration bundles applied at provisioning time. They can configure any desktop tool or user environment. **ai-desktops owns the profile contract and loader, not the profile policy.**

## Ownership model

Profiles live with the thing that owns their policy:

| Profile | Location | Purpose |
| --- | --- | --- |
| Personal interactive profile | dedicated user repo, e.g. `markcallen/ai-desktop-profile` | Personal desktop setup across repositories |
| Repository developer profile | repository `profiles/developer`, e.g. `orchael/bridge:profiles/developer` | Development tools and policy appropriate for that product |
| Crew role profile | `orchael/crew:profiles/<role>` | Autonomous worker setup such as reviewer, developer, or tester |

Do not copy personal policy into ai-desktops and do not put Crew execution policy into a product repository.

## Profile references

Desktop profile references use:

```
owner/repository[:path]
```

Examples:

```
markcallen/ai-desktop-profile
orchael/bridge:profiles/developer
orchael/crew:profiles/reviewer
orchael/crew:profiles/developer
orchael/crew:profiles/tester
```

A repository-only reference means the profile is at the repository root.

## Selection

Configure a personal default on the operator machine:

```yaml
# ~/.ai-desktops/config.yaml
desktop:
  profile: markcallen/ai-desktop-profile
  profile_secret: /ai-desktops/markcallen/profiles/ai-desktop-profile
```

A per-desktop `--desktop-profile` selection overrides that default. `--profile-secret` selects the secret for that desktop. Crew should pass both its role profile and role secret explicitly when dispatching a worker instead of inheriting a human operator's personal profile.

An explicit `--desktop-profile ''` disables the configured default for one desktop. An empty profile means no custom setup is applied. If a different profile is selected, the configured `desktop.profile_secret` is not injected; pass its secret with `--profile-secret`. An explicit `--profile-secret ''` disables the configured secret for one desktop.

The selected repository must be accessible to the desktop's provisioned GitHub SSH key. The CLI rejects malformed references before creating infrastructure. Create waits up to 15 minutes for cloud-init to finish when a profile is selected. A clone or installer failure makes create fail and records a failed fleet state; the instance remains available for diagnosis. A profile is applied only when a desktop is created, so changing the operator config does not update an existing desktop.

## Profile secret

Create one AWS Secrets Manager secret for each profile whose environment variables need separate ownership or rotation. For example, create an empty secret and then add its JSON keys through your normal secret-management workflow:

```sh
aws secretsmanager create-secret \
  --name /ai-desktops/markcallen/profiles/ai-desktop-profile \
  --secret-string '{}' \
  --region <aws-region> --profile <aws-profile>
```

Populate the placeholder before creating a desktop. The secret value is a JSON object whose keys are environment variable names, for example `{"SERVICE_TOKEN":"..."}`. Put the path, never the value, in `desktop.profile_secret` or pass it with `--profile-secret`. Create verifies that the selected secret exists before provisioning. Its keys are written with private permissions to the desktop's user environment, available to the profile installer, login shells, and user services. They are not written to bridgectl's bridge-only `agents.env`. As with `create --secret`, these variables are available across the desktop user environment; they are not isolated to the profile process.

The profile secret path is tracked on the desktop. After changing its value, run `ai-desktops secrets reload <desktop-id>`. An existing desktop can attach a new profile secret with `ai-desktops secrets add <desktop-id> <secret-path>`. Adding a secret does not rerun the profile installer.

## Profile contract

A profile directory must contain:

```
profile.yaml
install.sh
```

A profile may also contain tool-specific files, such as `codex/config.toml` or `claude/settings.json`, and may install any desktop tools or user configuration it owns.

`install.sh` is the installation boundary. It must be idempotent and must not contain credentials. ai-desktops supplies credentials separately through Secrets Manager.

The installer runs as the desktop user from the selected profile directory, after GitHub authentication and Claude onboarding and before workspace repositories are cloned or bridgectl starts. It should fail with a nonzero exit status when required setup fails.

## Provisioning model

Profiles are applied to the desktop user's home, not copied into workspace repositories:

- Codex user configuration, if used -> `~/.codex/`
- Claude Code user configuration, if used -> `~/.claude/`
- other user tools and configuration chosen by the profile
- repository-specific `.codex/`, `.claude/`, `AGENTS.md`, and `CLAUDE.md` remain owned by each workspace repository

For example, a Bridge worker can be created with `orchael/bridge:profiles/developer`. A Crew review worker can use `orchael/crew:profiles/reviewer` against that same Bridge checkout. The selected worker role therefore does not require changing Bridge itself.

## Precedence and responsibilities

1. **ai-desktops** installs tools, injects credentials, fetches the selected profile, applies it, and checks readiness.
2. **desktop profile** defines user-level tools, permissions, hooks, and defaults.
3. **target repository** defines project instructions and project-scoped configuration.
4. **Crew** selects an explicit role profile for autonomous work.

A profile should not silently rewrite project configuration in a checkout.

## Security

Profile repositories are configuration, not secret stores. Never commit provider tokens, GitHub tokens, SSH keys, OTP secrets, or other credentials.

Profiles may grant commands permission to execute without prompting. Keep those rules narrow. Avoid blanket shell approval. Destructive operations such as force pushes, repository deletion, volume deletion, and broad cleanup commands should remain outside normal developer/reviewer allow lists.

Treat profile changes as security-sensitive code changes and review them accordingly.
