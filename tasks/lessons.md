# Execution lessons

- On managed desktops, a package-provided user unit can coexist with the desktop's configured unit under a different name. Mask the generic unit during provisioning and document which unit to restart after upgrades; a package install leaves an already running process on the old executable.

- Configuration ownership should follow the thing being configured: a general desktop profile belongs under `desktop`, even if its first examples configure agents. When a profile secret is tied to a default profile, a per-desktop profile override must not inherit that default secret; track the chosen secret with the desktop and load it before the installer runs.

- A documented configuration key is not an implemented feature: verify the config struct, CLI selection, and rendered bootstrap all carry it before testing a live desktop. Resolve profile repository names against the actual private repo and the desktop's GitHub identity before provisioning.
- User-level installers must run with the desktop user's login PATH when agent binaries live outside sudo's default PATH. Run Claude onboarding before a profile installer that may create `.claude.json`, and make selected-profile failures exit the enclosing cloud-init script.
- Pulumi infrastructure completion precedes cloud-init completion. When readiness depends on bootstrap, wait for its terminal result before marking a fleet record ready; a successful rendered script alone does not prove the installer ran. Run installers from the selected profile directory so relative profile assets resolve correctly.

- Cloud-init `runcmd` entries execute as separate processes, so one entry's `EXIT` trap cannot represent the full bootstrap lifecycle. Run a separate watcher for `cloud-init status --wait` and atomically publish its terminal result.
- Validate image-baked tools in the live Packer sequence, not only in isolation: assert the release binary's actual version format, configure consumers only after installation, and establish third-party package-manager trust before loading their metadata.
- Do not model secrets with different exposure scopes as one merged value map. Keep their retrieval paths tracked transactionally, but preserve typed destinations through initial provisioning and reload; test duplicate keys to catch accidental cross-scope copying.
- Optional remote services may downgrade a confirmed service failure, never an SSH transport failure; preserve exit 255 as a hard failure so an unreachable host cannot appear merely degraded.
- Lifecycle observers must start before every phase they claim to diagnose, and managed-config markers must precede the first mutation so interrupted provisioning remains safely repairable. Shell-expanded UI status commands must use the host application's quoting modifier for untrusted paths.
- Check context cancellation before interpreting a killed remote command's exit code; timeout termination commonly reports a non-transport exit code. Keep an image-wide capability marker distinct from per-config ownership markers, and poll asynchronous plugin initialization before asserting runtime behavior.
- Ansible `stat.exists` is false for a dangling symlink even when `stat.islnk` is true; exclude links before treating a path as absent. Validate lifecycle state, timestamps, and exit code as one consistent record so contradictory terminal artifacts cannot report success.
- Before reporting a branch commit complete, compare the intended file set with both `git show --stat HEAD` and the live worktree; a clean commit message does not prove every requested file was included.
- Integration suites with provisioning in `TestMain` still perform setup under `go test -run '^$'`; use `go test -c` when the goal is compile-only validation with no cloud side effects.

- For tests targeting "this repository", verify Git origin before provisioning. Repository catalogs can retain an older organization name; bind cleanup to the exact recorded resource identities regardless of the remote name.
- Credential-source selection and remote credential validity are separate checks. Preserve provider-owned refresh state; do not replay an in-progress task under a different identity after a remote auth failure.
- A resumable E2E runner must support partial provisioning (workspace created, desktop not yet created), retain exact resource IDs, and report useful error categories without exposing provider output or credentials.
- Validate explicit auth paths exactly as consumers receive them; accepting shell expansions during validation while persisting the unexpanded string makes a successful reload unusable.
- Preserve base-before-override secret order both when initially creating a desktop and when registering the base secret on an older desktop. Cache invalidation must inspect each previous credential surface before merging away overridden paths.
- A credential-operation lock must span both remote side effects and metadata commit. Test coordinator death while its child runs, late disconnected-client commits, and explicit recovery; a local mutex or a final unconditional read/write is insufficient.
- Validate every destructive target's type, ownership, and permissions before staging replacements or stopping services. Test failure preservation, not merely a nonzero exit status.
- E2E artifacts must exclude arbitrary provider arguments, and cleanup failure must change the persisted result even when the scenario itself passed.
- Preflight every credential output and its parent chain, not only provider auth caches. A private temporary file can still land on shared storage through a symlink or nested mount; create each missing directory explicitly because `mkdir(parents=True)` applies its mode only to the leaf. Test fixture homes must set their mode explicitly rather than inherit the runner's umask.
- Atomic replacement must preserve unrelated file metadata intentionally: test successful `.bashrc` updates for mode preservation separately from private credential snapshots. In-memory record isolation must clone every slice consistently at all read/write boundaries, not only the field involved in the current bug.
- Credential preflight must distinguish an absent source from a present blank source before destructive rotation. Test empty and whitespace values alongside another nonempty secret/API key so aggregate nonempty checks cannot hide the gap; also prove absent-source fallback still works.
- A companion fix being merged or released does not change the consuming repository's package pins. Test the required release in both Packer variables and rendered cloud-init, and document the matching-AMI rollout separately from E2E branch-binary overrides.
- Credential rotation must distinguish an explicitly absent secret from a secret that could not be read. Treat only the provider's stable not-found code as authorization to create; network, access, empty/non-string, and parse failures must stop before any write.
- If runtime security checks require a private tool home, create it with explicit ownership and mode in both image and fallback provisioning before the tool inherits a permissive login umask. Keep reload validation fail-closed and surface the invariant through health checks.
- Do not choose between actionable errors and secret-safe output. Have the credential child return fixed exit categories, map those through the coordinator, and keep arbitrary stdout/stderr suppressed.
- Permission checks built on `stat` follow symlinks by default; reject the link itself before validating a security-sensitive directory's target ownership and mode. Match provider error codes only in their structured error position, and pair mutable package-manager installs with an expected-version gate when images are built independently across regions.
- Do not derive allowlisted failure categories from exception text that contains caller-controlled identifiers. Carry the category as typed internal state, keep the public message generic, and make security preconditions exit the parent provisioning script before later steps can mask or build on the failure.
- Before adding an AMI assertion for a third-party package's installed files, inspect the exact pinned artifact with `dpkg-deb -c` or equivalent. A static test that repeats an assumed path can pass while the live bake fails; verify the package-owned path, mode, and launcher command before encoding them in the playbook.
- A browser smoke running during image provisioning can inherit desktop display or D-Bus session variables that make a headless launch hang after the process starts. Isolate those variables for the bake check, bound its timeout, and retain a fresh-desktop browser test to prove the shipped environment works.
- An exact apt version can be present in an upstream package index yet unavailable as an installation candidate on a builder. For essential image packages, pin the vendor artifact hash and verify the downloaded package before apt installs it; keep post-install version and launch checks to catch mismatches.

# Editor defaults belong in the AMI

- When correcting a desktop's editor configuration, check both the live user's shell and the Packer source. A live fix does not carry into newly created desktops. Keep Git's `core.editor` and session editor variables aligned, and verify them in the AMI and post-boot checks.
- Before placing a verification task in a post-boot playbook, trace every invocation of that playbook. If cloud-init invokes it only for optional features, use AMI bake checks and a live desktop integration test for behavior required on every desktop.
# 2026-10-04 Repository moves must cover runtime assumptions

- Incident/bug: After changing the Git remote and import/package references, the E2E runner still accepted only `owner/ai-desktops` and its remote helper used `/workspace/ai-desktops`.
- Root cause pattern: Repository identity was embedded in validation and runtime paths as well as imports and documentation.
- Early signal missed: A search for full GitHub URLs and package names did not catch unqualified repository names in regular expressions and workspace paths.
- Preventative rule: For repository moves, search the old repository basename and workspace paths as well as complete URLs, module paths, package names, and release metadata.
- Validation added: The E2E origin and legacy-state tests, full Go suite, and workspace-path review.
- Next trigger to detect sooner: Any repository rename or change to the checkout's `origin`.
# 2026-10-04 Package renames must include installers and existing service units

- Incident/bug: Copilot found that the AMI npm configuration still mapped the old scope, and `update-web` on an existing desktop restarted a unit that still launched the old package.
- Root cause pattern: Package coordinates were stored separately in registry configuration, installation commands, and baked systemd units.
- Early signal missed: Tests covered the fresh package path but not the old AMI upgrade path.
- Preventative rule: For package renames, verify registry scope and both fresh-install and in-place upgrade paths before declaring the migration complete.
- Validation added: Targeted update-web tests check registry mapping, local package identity, shell syntax, and service migration before restart.
- Next trigger to detect sooner: A package scope or runtime package name change.
# 2026-10-10 Prove bootstrap credentials and cleanup identity at the boundary

- Incident: SaaS create omitted the operator GitHub secret while bootstrap still used SSH clone URLs; a failed nested launch cleanup could retain a fleet record without the instance ID needed by destroy.
- Preventative rule: Match clone transport to the credentials actually installed on the guest, and persist externally created resource IDs before any waiter or cleanup step can fail.
- Trigger: Any provisioning path that changes credential sources or creates resources before infrastructure-as-code import.
