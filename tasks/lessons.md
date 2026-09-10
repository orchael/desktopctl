# Execution lessons

- For tests targeting "this repository", verify Git origin before provisioning. Repository catalogs can retain an older organization name; bind cleanup to the exact recorded resource identities regardless of the remote name.
- Credential-source selection and remote credential validity are separate checks. Preserve provider-owned refresh state; do not replay an in-progress task under a different identity after a remote auth failure.
- A resumable E2E runner must support partial provisioning (workspace created, desktop not yet created), retain exact resource IDs, and report useful error categories without exposing provider output or credentials.
