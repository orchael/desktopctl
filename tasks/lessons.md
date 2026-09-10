# Execution lessons

- For tests targeting "this repository", verify Git origin before provisioning. Repository catalogs can retain an older organization name; bind cleanup to the exact recorded resource identities regardless of the remote name.
- Credential-source selection and remote credential validity are separate checks. Preserve provider-owned refresh state; do not replay an in-progress task under a different identity after a remote auth failure.
- A resumable E2E runner must support partial provisioning (workspace created, desktop not yet created), retain exact resource IDs, and report useful error categories without exposing provider output or credentials.
- Validate explicit auth paths exactly as consumers receive them; accepting shell expansions during validation while persisting the unexpanded string makes a successful reload unusable.
- Preserve base-before-override secret order both when initially creating a desktop and when registering the base secret on an older desktop. Cache invalidation must inspect each previous credential surface before merging away overridden paths.
