# GitHub Issue Draft: Pre-Baked AMI Approach for PRD Update

## Title
RFC: Update PRD to include pre-baked AMI approach with pinned versions

## Body

## Summary

Update the PRD to include a pre-baked AMI approach for desktop provisioning, replacing the current cloud-init-only strategy. This approach pins specific versions of all required software at AMI build time, improving boot speed, reliability, and consistency.

## Current State

The MVP uses cloud-init to install all software (novnc-desktop, ai-agent-bridge, dev tools) at first boot:

- **Pros**: Secrets not baked into images; simple bootstrap logic
- **Cons**: Slow boot time; Elementary/Pantheon compatibility risk during install; version drift across desktops; cloud-init failures hard to diagnose

## Proposed Approach

Pre-build AMIs with specific, pinned versions of:

### Required Components
- `novnc-desktop` — pinned at release version (e.g., v0.1.5)
- `ai-agent-bridge` — pinned at release version
- `ai-agent-browser` — pinned at release version
- `android-emulator-webapp` — pinned at release version

### Base Development Toolchain
- `docker` — pinned version with systemd service enabled
- `uv` — pinned version for Python package management
- `go` — pinned version for Go development
- `git`, `nvim`, `tmux` — from Ubuntu repos or pinned
- `brew` (Linuxbrew) — pinned for additional package installs

### Build and Distribution
- Packer or similar tool to build AMIs per region
- Version the AMI builds (e.g., `ai-desktops-v1.2.3-elementary`)
- Publish to a known AWS account or as public AMIs
- Store AMI IDs in config or hardcode per region (like today)

### Secrets Handling
- Secrets (GitHub PAT, provider credentials) remain injected at runtime via cloud-init or user data script
- Keep the runtime secret injection secure (no baking into image)
- Smaller cloud-init script (only secrets + workspace setup)

## Benefits

1. **Faster boot**: Software already installed; no network fetches during first boot
2. **Reliability**: No cloud-init package install failures; Elementary/Pantheon stability guaranteed
3. **Consistency**: All desktops use identical software versions unless intentionally bumped
4. **Debuggability**: Pre-boot state is reproducible; easier to diagnose AMI vs. runtime issues

## Trade-Offs

- Requires a separate AMI build pipeline (Packer, CI/CD step)
- Must update and republish AMIs when software versions change
- Slightly larger AMIs (but manageable)
- Requires documenting the build process for contributors

## Next Steps

1. Update `PRD.md` with the new AMI approach and version pinning strategy
2. Add an AMI build task to `plans/`
3. Create a Packer config or equivalent build definition
4. Document the AMI versioning and release process
5. Update README with the new workflow (bootstrap → init-foundation → create)

## Related Issues

- Potential Elementary/Pantheon compatibility risk with cloud-init (PRD line 315)
- Current cloud-init bootstrap timing and reliability

---

**Acceptance**: PRD is updated to reflect pre-baked AMI approach with clear version pinning strategy and build pipeline requirements.
