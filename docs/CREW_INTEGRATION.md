# Crew workspace leasing contract

Tracked by Linear MAR-44.

Crew should use ai-desktops as its first isolated execution provider. Existing desktop and workspace lifecycle remains authoritative here; Crew only needs a safe lease abstraction around it.

## Goal

A Crew run can acquire exactly one ready workspace, prepare a clean per-run Git worktree, use bridgectl inside that environment, and release the lease without destroying persistent credentials or unrelated workspace state.

## Proposed API

### Acquire

`POST /api/crew/leases`

Request:

```json
{
  "crew_run_id": "crew_123",
  "organization_id": "...",
  "repository": "orchael/factory",
  "base_ref": "main",
  "ttl_seconds": 7200
}
```

Response should include only non-secret connection metadata:

```json
{
  "lease_id": "lease_123",
  "crew_run_id": "crew_123",
  "desktop_id": "d-...",
  "workspace_id": "workspace:...",
  "repo_path": "/workspace/runs/crew_123/repo",
  "bridge_target": "...",
  "expires_at": "..."
}
```

### Release

`DELETE /api/crew/leases/{lease_id}`

Release removes the run-specific worktree and lease ownership. It must not delete persistent desktop credentials.

## Concurrency

Lease acquisition must be atomic. A desktop/workspace already leased to another active Crew run cannot be returned. Expired leases need a safe reclamation path that verifies no active run still owns the worktree.

## Workspace preparation

For each run:

1. ensure the requested repository is available
2. fetch the base ref
3. create a dedicated worktree under a run-specific path
4. create the Crew branch from the requested base SHA/ref
5. return the worktree path to Crew

Crew passes that path as `repo_path` to bridgectl. Provider credentials remain managed by ai-desktops/bridgectl and are never returned to Crew.

## Release cleanup

- stop or detach the run's bridgectl session first
- preserve branch/commits already pushed to GitHub
- remove the local run worktree
- clear lease ownership atomically
- keep caches, installed tools, and credentials intact

## Implementation plan

1. Add lease fields/store abstraction rather than overloading `WorkspaceStateAttached` with Crew semantics.
2. Implement atomic acquire/release for in-memory and DynamoDB stores.
3. Add worktree preparation/cleanup service methods.
4. Add authenticated HTTP endpoints.
5. Test concurrent acquire, idempotent release, expiry, and cleanup failure.
6. Return bridge connection metadata without secret material.
7. Wire Crew's `EnvironmentProvider` adapter.

Kubernetes ephemeral workers can implement the same Crew environment contract later.