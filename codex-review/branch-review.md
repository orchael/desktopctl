Approve with fixes

**Diff Summary**

Base/ref reviewed: `origin/main...HEAD`
Head: `HEAD`
Profile: `standard`

Major subsystems touched:
- `create` CLI flags and create-time integration secret handling
- Cloud-init provisioning for Tailscale and step-ca
- Pulumi desktop stack user-data handling
- Fleet/status/doctor metadata and health checks
- Config schema, README/docs, Packer/Ansible AMI build path

**Areas Checked**

- Go CLI behavior for create preview/create, integration resolution, Secrets Manager paths, and compressed user-data flow
- Cloud-init rendering, YAML validity coverage, secret retrieval, Tailscale bootstrap, step-ca bootstrap, and bridgectl environment wiring
- Pulumi stack config changes from raw `userData` to `userDataBase64`
- Fleet record persistence and status/doctor display of new integration metadata
- Packer/Ansible syntax around new `tailscale_version` variable and package install tasks
- Tests/build behavior for changed Go packages

**Areas Not Checked / Limits**

- Did not run real AWS provisioning, Pulumi `up`, EC2 boot, Secrets Manager writes, Tailscale registration, or step-ca certificate issuance.
- `packer validate` could not complete because the local Packer source plugin failed to load in this environment; Ansible syntax check was run separately.
- Did not verify that the pinned external Tailscale apt package version is available from the live upstream repository.
- Did not run web app `pnpm` commands because the diff does not touch `apps/desktop-web`.

**Findings**

P2 - README IAM permissions are stale for the new create-time integration secret path. The new Tailscale/step-ca flow documents that `create` stores or reuses integration secrets in AWS Secrets Manager, and the implementation performs `GetSecretValue` when reusing an existing secret plus `DescribeSecret`, `PutSecretValue`, or `CreateSecret` when local env vars are supplied: [cmd/ai-desktops/cmd/create.go](/home/marka/src/orchael/ai-desktops/cmd/ai-desktops/cmd/create.go:643), [cmd/ai-desktops/cmd/setup.go](/home/marka/src/orchael/ai-desktops/cmd/ai-desktops/cmd/setup.go:699). However, the README’s `Desktop lifecycle (create, stop, start, terminate)` operator permissions list still only includes EC2, Route53, and DynamoDB actions: [README.md](/home/marka/src/orchael/ai-desktops/README.md:35). A user following the documented permissions and then running the new documented path at [README.md](/home/marka/src/orchael/ai-desktops/README.md:389) will hit AccessDenied before provisioning. Update the create/lifecycle permissions to include the required `secretsmanager:GetSecretValue`, `secretsmanager:DescribeSecret`, `secretsmanager:PutSecretValue`, and `secretsmanager:CreateSecret` scope for the `/ai-desktops/<owner>/tailscale/*` and `/ai-desktops/<owner>/step-ca/*` integration secrets.

**Tests To Run**

- `go test ./...`
- `git diff --check origin/main...HEAD`
- `ANSIBLE_LOCAL_TEMP=/tmp/ansible-local ANSIBLE_REMOTE_TEMP=/tmp/ansible-remote ansible-playbook --syntax-check packer/playbook.yml --extra-vars 'go_version=1.24.0 uv_version=0.4.0 flutter_version=3.32.0 android_cmdline_tools_version=11076708 ai_agent_bridge_version=v0.8.0 tailscale_version=1.98.9 desktop_web_version=0.1.0 novnc_desktop_version=v1.0.0'`
- `packer init packer/ubuntu-desktop.pkr.hcl && packer validate -var-file=packer/variables.pkrvars.hcl ... packer/ubuntu-desktop.pkr.hcl` with the repo’s normal required Packer vars in an environment where plugins can load.
