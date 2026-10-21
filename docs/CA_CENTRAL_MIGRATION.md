# Fresh ca-central-1 setup

The migration uses `desktopctl` names throughout. A setup is identified by its
AWS account, environment, and region. The fresh production config for this
checkout is `~/.config/desktopctl/config-prod-ca-central-1.yaml` on Linux; an
example is at the repository root. `XDG_CONFIG_HOME` changes the base directory. Pass
`--config` to every command when using the named file.

Start with the [README quick start](../README.md#quick-start-production-in-ca-central-1),
which gives both scripted and interactive commands for a new config. Use
`--allow-existing` on the first run if reusing AWS secret names from the prior
installation. Setup automatically restores the GitHub and agent secrets if
they are scheduled for deletion. Later setup runs with a matching saved
owner, environment, and region can reuse them without that flag. The wizard
detects its current public IPv4 address and confirms the `/32` security group
rule. It also lists the GitHub classic token scopes and accepts a replacement
token with hidden input. The AMI build reads the token from that regional
secret unless `GITHUB_NPM_TOKEN` is set. Verify the AWS profile resolves to
the intended account and check that the bucket name
is globally available. The example uses account 819363892004,
profile `marka`, environment `prod`, and region `ca-central-1`.

```sh
aws sts get-caller-identity --profile marka
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1.yaml bootstrap
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1.yaml init-foundation --no-dns --preview
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1.yaml init-foundation --no-dns
```

`bootstrap` refuses an existing state bucket. `init-foundation` refuses an
existing fleet table in the selected region. Updating an intentionally existing
setup requires the command's `--allow-existing` flag. The new foundation stack
is `foundation-prod-ca-central-1`; fleet and AMI tables include both dimensions.
Do not point this config at another live installation's backend bucket or a
legacy secret path. The deleted dedicated bucket name from the prior setup can
be reused after confirming it is still available.

## AMI and first desktop

The new config has no pinned AMI. Public AMI `ami-0d0b8cccb4d7077fd` was
verified available in ca-central-1 on 2026-10-20. Check that it is still
available and pass it with `create --ami ami-0d0b8cccb4d7077fd`.
`desktop.public_ami_owner` can instead discover the newest public
`desktopctl-v<CLI major>-*` image from a trusted
account. A maintainer who needs a new image can follow the
[public AMI build guide](PUBLIC_AMIS.md); this setup does not require a build.

The account currently has no `desktops.orchael.com` hosted zone, so this
configuration uses `--no-dns` for the foundation and desktops. This records
`<desktop-id>.invalid` as a placeholder hostname, creates no Route 53 record,
and skips Certbot. The printed noVNC URL and direct SSH target will not
resolve. For browser access with a trusted certificate, configure and delegate
the Route 53 zone and omit `--no-dns` for both foundation and desktops. EFS
workspace resources remain available in no-DNS mode. Use
`desktopctl --config <path> ssh --tunnel ssm <desktop-id>` for SSH after
creation, provided Session Manager is available. Once setup has stored GitHub
credentials and registered an EC2 SSH key, create a desktop with
`desktopctl --config <path> create --no-dns --ami ami-0d0b8cccb4d7077fd --repo orchael/desktopctl`.
The opt-in SSH login runner currently connects to the desktop hostname
directly, so it cannot test no-DNS desktops. Use it only after configuring a
resolving zone and setting `desktop.active_ami.ca-central-1` in the new config.
See [AWS desktop E2E scenarios](../tests/e2e/README.md) for its lifecycle and
cleanup behavior.

## Remove an installation

Terminate desktops and delete any workspaces first. Build the current CLI, then
preview the foundation and state cleanup using the same config and AWS account:

```sh
make build
python3 scripts/cleanup-installation.py \
  --config ~/.config/desktopctl/config-prod-ca-central-1.yaml \
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
