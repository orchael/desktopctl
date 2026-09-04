# TODO

## Warm Pool CLI (issues #212, #213, #214)

- [ ] Add `ai-desktops pool` subcommands: `status`, `members`, `replenish`, `drain` — https://github.com/markcallen/ai-desktops/issues/212
- [ ] Integrate pool into `create` (acquire with Pulumi fallback), `stop` (release), and `terminate` (release + recycle) — https://github.com/markcallen/ai-desktops/issues/213
- [ ] Implement pool member provisioner (Pulumi-launched stopped instance) and EBS scrub on recycle (SSM send-command) — https://github.com/markcallen/ai-desktops/issues/214

## Live Acceptance (issue #159)

These require a real AWS environment and cannot be automated.

- [ ] Run the Plan 14 acceptance path: build an AMI, create a desktop from it, verify the toolchain, and record the provisioning-time improvement — https://github.com/markcallen/ai-desktops/issues/159
- [ ] Run the Plan 16 browser, API, service, and noVNC checks against a rebuilt desktop — https://github.com/markcallen/ai-desktops/issues/159
- [ ] Run `ai-desktops setup` against a test owner, provision a desktop, and run the FR-11 live integration checks — https://github.com/markcallen/ai-desktops/issues/159
