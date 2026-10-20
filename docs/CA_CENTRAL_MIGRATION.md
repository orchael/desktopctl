# Fresh ca-central-1 setup

The migration uses `desktopctl` names throughout. A setup is identified by its
AWS account, environment, and region. The production config for this checkout
is `~/.config/desktopctl/config-ca-central-1.yaml` on Linux; an example is at
the repository root. `XDG_CONFIG_HOME` changes the base directory. Pass
`--config` to every command when using the named file.

Run `desktopctl --config ~/.config/desktopctl/config-ca-central-1.yaml setup`
on the machine that will access desktop SSH. If reusing the two secret names
after the October 2026 teardown, restore those scheduled secrets first and
pass `setup --allow-existing` as shown in the [quick start](../README.md#quick-start-production-in-ca-central-1).
The wizard detects its current
public IPv4 address and confirms the `/32` security group rule. It also lists
the exact GitHub classic token scopes, accepts the token with hidden input,
and explains the `read:packages` scope needed to build the public AMI. The AMI
build reads the token from that regional secret unless `GITHUB_NPM_TOKEN` is set.
Verify the
AWS profile resolves to the intended account and check that the bucket name
is globally available. The example uses account 819363892004,
profile `marka`, environment `prod`, and region `ca-central-1`.

```sh
aws sts get-caller-identity --profile marka
desktopctl --config ~/.config/desktopctl/config-ca-central-1.yaml bootstrap
desktopctl --config ~/.config/desktopctl/config-ca-central-1.yaml init-foundation --no-dns --preview
desktopctl --config ~/.config/desktopctl/config-ca-central-1.yaml init-foundation --no-dns
```

`bootstrap` refuses an existing state bucket. `init-foundation` refuses an
existing fleet table in the selected region. Updating an intentionally existing
setup requires the command's `--allow-existing` flag. The new foundation stack
is `foundation-prod-ca-central-1`; fleet and AMI tables include both dimensions.
Do not point this config at an old backend bucket or legacy secret path.

## AMI and first desktop

The existing config pins public AMI `ami-0d0b8cccb4d7077fd`, which was
verified available in ca-central-1 on 2026-10-20. Check that it is still
available before creating a desktop. `desktop.public_ami_owner` can instead
discover the newest public `desktopctl-v<CLI major>-*` image from a trusted
account. A maintainer who needs a new image can follow the
[public AMI build guide](PUBLIC_AMIS.md); this setup does not require a build.

The account currently has no `desktops.orchael.com` hosted zone, so this
configuration uses `--no-dns` for the foundation and desktops. It still creates
EFS workspace resources and supports SSH access. After
`desktopctl --config <path> setup` has stored GitHub credentials and registered
an EC2 SSH key, create a desktop with
`desktopctl --config <path> create --no-dns --repo orchael/desktopctl`.
The opt-in runner then covers workspace and desktop creation, cloud-init,
SSH login, and teardown:

```sh
make build
scripts/e2e.sh --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --scenario ssh-login --no-dns --cli ./bin/desktopctl
```

The runner saves a private state file, and retains resources on failure for
diagnosis. Pass `--cleanup <state-file>` after fixing the failure. A successful
run terminates its desktop and deletes its EFS workspace. The runner assumes
the foundation and AMI already exist; it does not destroy the shared foundation
or backend bucket.

## Remove an installation

Terminate desktops and delete any workspaces first. Build the current CLI, then
preview the foundation and state cleanup using the same config and AWS account:

```sh
make build
python3 scripts/cleanup-installation.py \
  --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --account-id 819363892004
```

Add `--apply` to run it. The wrapper requires Python 3 with PyYAML, AWS CLI,
Pulumi, and the built CLI. It checks the AWS account, bucket region, and every
object key in the dedicated state bucket before changing anything. It refuses
state belonging to another Pulumi stack. It then runs `destroy-foundation`,
removes the empty Pulumi stack, deletes every version and delete marker in the
state bucket, and deletes the bucket. If any step fails, it stops; correct the
cause and rerun it. The wrapper does not delete AMIs, the private base AMI, or
AWS Backup recovery points.

`destroy-foundation --yes` also deletes the setup-created EC2 key pair and
schedules deletion of the configured GitHub and agent secrets with a seven-day
recovery window. It accepts only setup's exact environment-, region-, and
owner-scoped names; custom names need separate review. The wrapper verifies
that the key is absent and both secrets are absent or scheduled for deletion
before removing Pulumi state. The AWS Backup EFS automatic backup vault can
retain a recovery point after its file system is deleted. Check its retention
date and let it expire if a short recovery window is useful; it is managed
separately from the Pulumi and S3 cleanup.
