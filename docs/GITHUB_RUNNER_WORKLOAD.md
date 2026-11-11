# GitHub runner workload requirements (Orchael Runners)

Status: proposed, not implemented. Consumer: [orchael/runners](https://github.com/orchael/runners).

## Objective
Extend desktopctl to provision and manage **headless ephemeral GitHub Actions runner servers** without installing Elementary, VNC, developer desktop profiles, or long-lived agent credentials. Preserve existing desktop behavior.

## Provisioning API
Provide a versioned, authenticated service API or SDK for `createRunnerHost`, `leaseRunnerHost`, `releaseRunnerHost`, `terminateRunnerHost`, `describeRunnerHost`, `reconcileWarmPool`, and `buildRunnerImage`. Each request includes organization, region, architecture, image version, instance class, idempotency key, TTL, and tags. Return lifecycle status and stable IDs. Add contract tests with Runners; never execute CLI shell commands from user-facing web requests.

## AMI profile
Create Packer profile `orchael-runner-ubuntu-24.04-x64`, based on pinned Ubuntu 24.04 image and upstream GitHub `actions/runner-images` image definitions. Install a matching tested toolchain and publish an exact software manifest, image provenance, SBOM, vulnerability results, compatibility-test results, and AMI ID per region. Image releases must be immutable and canary-tested; avoid claiming perfect hosted image parity. No GitHub registration tokens or tenant secrets in AMI.

## Warm pool
Configurable min/target/max idle EC2 hosts per region/architecture. Warm hosts are provisioned and healthy but **unregistered** with GitHub. On lease, inject a short-lived runner registration token via secure channel, register one ephemeral runner, process at most one job, export telemetry, terminate host, and asynchronously replenish pool. Implement queue-aware scaling, backpressure, idempotent leases, fencing tokens, timeouts, dead-letter reconciliation, cleanup of stale registrations, and idle-budget limits. Never reassign untrusted job disks to a new tenant. Prefer On-Demand initially, evaluate Spot later.

## Security
Tenant isolation; no inbound SSH; IMDSv2; encrypted EBS; restricted outbound network; least-privilege IAM; no persistent secrets; job cancellation must terminate host; untrusted fork jobs must never access privileged credentials. Distinguish infrastructure operator from customer GitHub App installation identity.

## Telemetry
Emit OpenTelemetry spans and metrics for allocation, AMI boot, cloud-init, lease, runner registration, job start/end, CPU/memory/disk/network, teardown, cost, and failures. Correlation IDs must link to GitHub run/job IDs and Runners organization. Charge users only for defined job execution minutes, not warm idle costs.

## Acceptance tests
1. Warm host allocated and leased in bounded time.
2. Runner appears online under expected GitHub scale-set label.
3. One sample Actions job completes and host terminates.
4. Failed registration, cancellation, timeout and provider errors leave no leaked credentials or orphan hosts.
5. Cross-tenant lease is rejected.
6. AMI runs checkout, cache, artifacts, Docker services, and sample Go/Node/Python builds.
7. Existing desktopctl desktop workflows remain unchanged.

## Suggested implementation slices
1. Headless runner workload type and SDK contract.
2. Packer AMI profile and compatibility harness.
3. EC2 warm-pool reconciler with durable leases.
4. GitHub ephemeral runner registration integration and lifecycle tests.
5. Metrics, security hardening, cost and load testing.
