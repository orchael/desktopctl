# AWS desktop E2E scenarios

This opt-in runner tests a desktop against the existing **dev** foundation and
active AMI. It creates an isolated retained EFS workspace and desktop, each named
`e2e-ai-desktops-<random ID>`, for this checkout's GitHub origin (or an explicit
`--repo owner/desktopctl`). It does not use `eos-dev`, build AMIs,
or create/destroy shared foundation infrastructure. These tests incur AWS costs.

## Run Codex authentication

Build the two branches under test, then run:

```bash
go build -o /tmp/ai-desktops-e2e ./cmd/ai-desktops
# In the bridgectl checkout containing the auth fix:
GOOS=linux GOARCH=amd64 go build -o /tmp/bridgectl-e2e ./cmd/bridgectl
# Back in desktopctl:
bash scripts/e2e.sh --cli /tmp/ai-desktops-e2e \
  --bridgectl-binary /tmp/bridgectl-e2e --scenario codex-auth
```

Prerequisites are the normal configured CLI/AWS/Pulumi access, access to
this repository with the configured GitHub secret, an SSH key authorized on
new desktops, and a configured agent secret containing usable `CODEX_AUTH`
account credentials. The runner uses the operator's normal CLI config by default;
`--config`, `--profile`, and `--region` allow explicit choices. `--ssh-key` must
identify the same private key file as `desktop.ssh_key_path` in the config:
desktop creation and `secrets reload` both use that configured key. Mismatched
overrides are rejected before cloud commands, including on reuse; choose the
intended key in the CLI config before starting a run.
Desktop defaults are `--env dev --instance-type m7i.large --workspace-mode efs`.
`--timeout 30m` bounds provisioning and scenarios. Cleanup has its own 15-minute
deadline.

The state-file path is printed **before provisioning**. JSON state and scenario
metadata contain resource IDs, paths, booleans, and process IDs, never tokens,
provider command arguments, or raw provider output. Provisioning and scenario
failures retain resources and report the desktop ID and workspace name. A cleanup
failure marks the overall run failed and preserves remaining resource IDs.
Creation failures can leave partial resources;
inspect the exact printed workspace name if creation failed before its IDs were
returned. Raw CLI output is withheld because errors can contain secrets.

## Keep, reuse, and clean up

```bash
# Keep even after all assertions pass:
bash scripts/e2e.sh --cli /tmp/ai-desktops-e2e \
  --bridgectl-binary /tmp/bridgectl-e2e --keep

# Reuse only resources in a previously generated manifest:
bash scripts/e2e.sh --cli /tmp/ai-desktops-e2e \
  --reuse /tmp/ai-desktops-e2e-EXAMPLE/state.json \
  --bridgectl-binary /tmp/bridgectl-e2e --keep

# Explicitly terminate the recorded desktop, then delete its workspace:
bash scripts/e2e.sh --cli /tmp/ai-desktops-e2e \
  --cleanup /tmp/ai-desktops-e2e-EXAMPLE/state.json
```

If initial creation stopped after creating the workspace, `--reuse` validates
that workspace and creates its missing desktop without making another workspace.
Supply `--bridgectl-binary` for this resumed creation. A failed workspace-create
response retains its creation intent; the runner attempts to recover the exact
new workspace's IDs immediately, and `--reuse` or `--cleanup` can retry that
discovery if the initial status lookup also failed. Discovery requires matching
name, owner, repo, EFS mode, and a creation time at or after the run started.
An interrupted desktop create that
already saved a desktop fleet record can be recovered only when its exact name,
creation time, owner, repository, and workspace identity match this manifest.
Conflicting names or attached/unavailable workspaces stop the runner.

Default successful runs terminate the desktop before deleting the workspace.
`--keep` retains successful resources; failures always retain them. Reuse without
`--keep` cleans up after success too. Cleanup/reuse validate the live resource
name, environment, owner, repository, workspace ID, EFS access point ID, and
attachment against the manifest before acting. An attached workspace is never
deleted. The original config/profile/region are persisted and cannot be overridden
on reuse or cleanup. Do not hand-edit manifests to adopt unrelated resources.

Workspace deletion uses the normal `workspace delete` command: it deletes the
retained workspace record/access point according to that command's semantics.
It does not recursively erase the EFS filesystem or shared filesystem directories.

## Assertions and debugging

The scenario waits for cloud-init's completion marker, the cloned repository,
and an active bridge service. It installs the supplied bridge binary and
temporarily configures the Codex provider to run the installed Codex CLI in
`exec --json --sandbox read-only` mode through:

```bash
bridgectl run --provider codex --no-tty /workspace/desktopctl
```

The first session must return the expected JSON assistant message. The prompt
contains two fragments to concatenate, so merely echoing input cannot pass.
The provider must use account auth with both API-key environment variables absent
and persist a mode-0600 auth file in a private desktop-local directory beneath
the user's home. Auth files outside that home, symlink escapes, and NFS/EFS mounts
are rejected before credentials are read or a refresh marker is written.
A harmless metadata field added to that file
simulates Codex refreshing credentials; a second authenticated session must
preserve it. Two wrapper processes hold the real bridge-owned provider process
groups alive after the answers. `ai-desktops secrets reload` must kill both
groups, replace the auth cache, and give a third session working account auth.
This deterministic lifetime fixture avoids timing reload against model latency.
The original provider config is restored on success and failure.

If the original uploaded refresh token is already unusable, the real request
fails and resources are retained. The test never refreshes or replaces the shared
AWS secret. A successful test demonstrates local refresh-state preservation;
it cannot prove that duplicating one account refresh token across many desktops
produces independently refreshable upstream credentials.

Desktop helper files are under `~/.local/share/ai-desktops-e2e/`. JSON files record
only safe metadata. The original bridge configuration is kept separately in a
mode-0600 restoration copy; provider arguments are read from that protected copy
at execution and the copy is removed after restoration. If an interrupted SSH session leaves temporary provider
configuration installed, restore it before rerunning:

```bash
XDG_RUNTIME_DIR=/run/user/$(id -u) python3 \
  ~/.local/share/ai-desktops-e2e/codex-test.py restore
```

## Add scenarios and test the runner offline

Scenarios belong beside `remote_codex.py`; register them in `run`'s scenario
validation and `exercise`. Reuse the provisioning, readiness, ownership checks,
state persistence, and finish/cleanup lifecycle. Keep credentials and raw output
out of artifacts. A scenario returns an error to retain the fixture for diagnosis.

```bash
go test ./tests/e2e -cover
# Desktop controller regressions (requires Python 3 and PyYAML):
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s tests/e2e -p 'test_*.py'
```

These offline Go tests inject a fake resource client and verify success cleanup
order, failure/keep retention, reuse validation, changed-resource refusal, and
termination-failure safety. They never access AWS. Existing
`tests/integration` remains the separate full-foundation integration suite.
The Python tests run a local fake bridge subprocess to verify that a transient
401 followed by a successful answer does not prematurely fail authentication,
and that terminal errors produce only safe diagnostic categories.
