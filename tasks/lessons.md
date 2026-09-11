# Execution lessons

- For tests targeting "this repository", verify Git origin before provisioning. Repository catalogs can retain an older organization name; bind cleanup to the exact recorded resource identities regardless of the remote name.
- Credential-source selection and remote credential validity are separate checks. Preserve provider-owned refresh state; do not replay an in-progress task under a different identity after a remote auth failure.
- A resumable E2E runner must support partial provisioning (workspace created, desktop not yet created), retain exact resource IDs, and report useful error categories without exposing provider output or credentials.
- Validate explicit auth paths exactly as consumers receive them; accepting shell expansions during validation while persisting the unexpanded string makes a successful reload unusable.
- Preserve base-before-override secret order both when initially creating a desktop and when registering the base secret on an older desktop. Cache invalidation must inspect each previous credential surface before merging away overridden paths.
- A credential-operation lock must span both remote side effects and metadata commit. Test coordinator death while its child runs, late disconnected-client commits, and explicit recovery; a local mutex or a final unconditional read/write is insufficient.
- Validate every destructive target's type, ownership, and permissions before staging replacements or stopping services. Test failure preservation, not merely a nonzero exit status.
- E2E artifacts must exclude arbitrary provider arguments, and cleanup failure must change the persisted result even when the scenario itself passed.
- Preflight every credential output and its parent chain, not only provider auth caches. A private temporary file can still land on shared storage through a symlink or nested mount; create each missing directory explicitly because `mkdir(parents=True)` applies its mode only to the leaf. Test fixture homes must set their mode explicitly rather than inherit the runner's umask.
