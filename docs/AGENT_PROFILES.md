# Agent profiles

Agent profiles are optional configuration bundles applied to a desktop at provisioning time. **ai-desktops owns the profile contract and loader, not the profile policy.**

## Ownership model

Profiles live with the thing that owns their policy:

| Profile | Location | Purpose |
| --- | --- | --- |
| Personal interactive profile | dedicated user repo, e.g. `markcallen/ai-desktop-profile` | How one user wants Codex and Claude Code to behave across repositories |
| Repository developer profile | repository `profiles/developer`, e.g. `orchael/bridge:profiles/developer` | Development policy and tools appropriate for that product |
| Crew role profile | `orchael/crew:profiles/<role>` | Autonomous worker policy such as reviewer, developer, or tester |

Do not copy personal policy into ai-desktops and do not put Crew execution policy into a product repository.

## Profile references

ai-desktops profile references use:

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
agent:
  profile: markcallen/ai-desktop-profile
```

A per-desktop `--agent-profile` selection overrides that default. Crew should always pass the role profile explicitly when dispatching a worker rather than inheriting a human operator's personal profile.

An empty profile means no custom agent configuration is applied.

## Profile contract

A profile directory contains:

```
profile.yaml
install.sh
codex/
  config.toml
claude/
  settings.json
```

Additional provider-specific rules and hooks may live below `codex/` and `claude/`.

`install.sh` is the installation boundary. It must be idempotent and must not contain credentials. ai-desktops supplies credentials separately through its existing Secrets Manager integration.

## Provisioning model

Profiles are applied to the desktop user's home, not copied into workspace repositories:

- Codex user configuration -> `~/.codex/`
- Claude Code user configuration -> `~/.claude/`
- user-scoped MCP registrations are installed by the profile
- repository-specific `.codex/`, `.claude/`, `AGENTS.md`, and `CLAUDE.md` remain owned by each workspace repository

For example, a Bridge worker can be created with `orchael/bridge:profiles/developer`. A Crew review worker can use `orchael/crew:profiles/reviewer` against that same Bridge checkout. The selected worker role therefore does not require changing Bridge itself.

## Precedence and responsibilities

1. **ai-desktops** installs tools, injects credentials, fetches the selected profile, applies it, and checks readiness.
2. **agent profile** defines user-level MCP availability, permissions, hooks, and agent defaults.
3. **target repository** defines project instructions and project-scoped configuration.
4. **Crew** selects an explicit role profile for autonomous work.

A profile should not silently rewrite project configuration in a checkout.

## Security

Profile repositories are configuration, not secret stores. Never commit provider tokens, GitHub tokens, SSH keys, OTP secrets, or other credentials.

Profiles may grant commands permission to execute without prompting. Keep those rules narrow. Avoid blanket shell approval. Destructive operations such as force pushes, repository deletion, volume deletion, and broad cleanup commands should remain outside normal developer/reviewer allow lists.

Treat profile changes as security-sensitive code changes and review them accordingly.
