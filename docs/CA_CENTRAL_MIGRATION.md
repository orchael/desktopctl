# Fresh ca-central-1 setup

The migration uses `desktopctl` names throughout. A setup is identified by its
AWS account, environment, and region. The production config for this checkout
is `~/.config/desktopctl/config-ca-central-1.yaml` on Linux; an example is at
the repository root. `XDG_CONFIG_HOME` changes the base directory. Pass
`--config` to every command when using the named file.

Run `desktopctl --config ~/.config/desktopctl/config-ca-central-1.yaml setup`
on the machine that will access desktop SSH. The wizard detects its current
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

`desktop.public_ami_owner` lets a new user launch the latest public
`desktopctl-v<CLI major>-*` AMI from a trusted AWS account in the configured region. The
image must be published in that region first. Read-only checks found no
`novnc-desktop-ubuntu-24.04-elementary-*` base or `desktopctl-*` image in
ca-central-1. Account 819363892004 owns a public noVNC base in us-east-1,
`ami-04b234a7966d2ab99`. The approved copy in ca-central-1 is
`ami-0db78418f8538fb8f` (private, available). A maintainer can build the desktopctl
image with `scripts/public-amis.sh build`; this requires
the pinned build inputs and a GitHub Packages token. A user can instead set
`desktop.active_ami.ca-central-1` to a launchable image ID or pass `create --ami`.
Image discovery checks that the selected image is public.

```sh
aws ec2 copy-image --profile marka --region ca-central-1 \
  --source-region us-east-1 --source-image-id ami-04b234a7966d2ab99 \
  --name novnc-desktop-ubuntu-24.04-elementary-20260710-140357
scripts/public-amis.sh build \
  --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --profile marka --regions ca-central-1 --enable-public-sharing
```

The copy returns a new regional AMI ID. Wait for `image-available` before the
build. The source image is public; the regional copy can stay private because
Packer runs in the publishing account. The resulting desktopctl image is public.

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
