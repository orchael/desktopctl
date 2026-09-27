# Agent profiles

Agent profiles are optional, user-owned configuration bundles applied to a desktop at provisioning time.

They are intentionally **not stored in ai-desktops**. The ai-desktops repository owns the profile contract and loader; a user keeps their profile in a separate repository such as `markcallen/ai-desktop-profile`.

## Selection

Configure a default profile on the operator machine:

```yaml
# ~/.ai-desktops/config.yaml
agent:
  profile: markcallen/ai-desktop-profile
```

A future `--agent-profile` create flag may override this per desktop. An empty profile means no custom agent configuration is applied.

## Profile repository contract

A profile repository should contain:

```
profile.yaml
install.sh
codex/
  config.toml
claude/
  settings.json
```

`install.sh` is the profile's installation boundary. It must be idempotent and must not contain credentials. ai-desktops supplies credentials separately through its existing Secrets Manager integration.

## Provisioning model

Profiles are applied to the desktop user's home, not to individual workspace repositories:

- Codex user configuration -> `~/.codex/`
- Claude Code user configuration -> `~/.claude/`
- repository-specific `.codex/`, `.claude/`, `AGENTS.md`, and `CLAUDE.md` remain owned by each workspace repository.

This keeps personal policy out of the public ai-desktops image while allowing the same configuration to follow a user across every repository on a desktop.

## Security

Profile repositories are configuration, not secret stores. Never commit provider tokens, GitHub tokens, SSH keys, or other credentials.

Profiles may grant commands permission to execute without prompting. Keep those rules narrow and treat profile changes as security-sensitive code changes.
