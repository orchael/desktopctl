# Fresh ca-central-1 setup

The migration uses `desktopctl` names throughout. A setup is identified by its
AWS account, environment, and region. The fresh production config for this
checkout is `~/.config/desktopctl/config-prod-ca-central-1-dns.yaml` on Linux; an
example is at the repository root. `XDG_CONFIG_HOME` changes the base directory. Pass
`--config` to every command when using the named file.

Start with the [README quick start](../README.md#quick-start),
which gives the interactive path for a new config. Use
`--allow-existing` on the first run if reusing AWS secret names from the prior
installation. Setup automatically restores the GitHub and agent secrets if
they are scheduled for deletion. Later setup runs with a matching saved
owner, environment, and region can reuse them without that flag. The wizard
detects its current public IPv4 address and confirms the `/32` security group
rule. It also lists the GitHub classic token scopes and accepts a replacement
token with hidden input. The AMI build reads the token from that regional
secret unless `GITHUB_NPM_TOKEN` is set. Verify the AWS profile resolves to
the intended account and reuse the existing bucket with `bootstrap --allow-existing`.
The example uses account 819363892004,
profile `marka`, environment `prod`, and region `ca-central-1`.

The control-plane operator secret belongs to the foundation, separately from
the two setup secrets. When a previous foundation secret is still scheduled
for deletion, a fresh `init-foundation` picks the next available versioned
name and stores it in the new Pulumi stack. Review the printed name in the
preview; the stale secret needs no manual deletion or restoration.

```sh
aws sts get-caller-identity --profile marka
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1-dns.yaml bootstrap --allow-existing
# Setup prints NS records for desktops.markcallen.dev; add them at the markcallen.dev DNS provider.
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1-dns.yaml dns check
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1-dns.yaml init-foundation --preview
desktopctl --config ~/.config/desktopctl/config-prod-ca-central-1-dns.yaml init-foundation
```

`bootstrap` refuses an existing state bucket without `--allow-existing`. `init-foundation` refuses an
existing fleet table in the selected region. Updating an intentionally existing
setup requires the command's `--allow-existing` flag. The new foundation stack
is `foundation-prod-ca-central-1`; fleet and AMI tables include both dimensions.
Do not point this config at another live installation's backend bucket or a
legacy secret path.

## AMI and first desktop

The new config has no pinned AMI. Setup writes publisher account `819363892004`
to `desktop.public_ami_owner`, so `create` discovers the newest available public
`desktopctl-v<CLI major>-*` image in ca-central-1. Review the chosen AMI ID
with `create --preview` before creating a desktop. Pass `--ami <id>` to pin a
specific image for one create, or set `desktop.active_ami.ca-central-1` to pin
it for subsequent creates. A maintainer who needs a new image can follow the
[public AMI build guide](PUBLIC_AMIS.md); this setup does not require a build.

The selected zone is `desktops.markcallen.dev`; the first desktop hostname is
`<desktop-id>.desktops.markcallen.dev`. A new public Route 53 zone must be
delegated at the provider for `markcallen.dev` using every printed NS value.
`dns check` reports when public resolution matches the Route 53 delegation
set. Foundation apply and desktop creation also verify delegation. Once setup
has stored GitHub credentials and registered an EC2 SSH key, create a desktop
with `desktopctl --config <path> create --repo orchael/desktopctl`.

If you intentionally choose `--no-dns`, use it for both foundation and desktop
creation. The hostname is only `<desktop-id>`, Certbot is skipped, and no
public noVNC URL is returned. `desktopctl ssh <desktop-id>` automatically uses
an SSM tunnel for these desktops, and the foundation opens no inbound desktop
ports. This mode does not provide a trusted browser URL.
The opt-in SSH login runner currently connects to the desktop hostname
directly, so it cannot test no-DNS desktops. Set
`desktop.active_ami.ca-central-1` in the new config before running it.
See [AWS desktop E2E scenarios](../tests/e2e/README.md) for its lifecycle and
cleanup behavior.

## Remove an installation

Terminate desktops and delete any workspaces first. Build the current CLI, then
preview the foundation and state cleanup using the same config and AWS account:

```sh
make build
python3 scripts/cleanup-installation.py \
  --config ~/.config/desktopctl/config-prod-ca-central-1-dns.yaml \
  --account-id 819363892004
```

Add `--apply` to run it. The wrapper requires Python 3 with PyYAML, AWS CLI,
Pulumi, and the built CLI. It checks the AWS account, bucket region, and every
object key in the dedicated state bucket before changing anything. It refuses
state belonging to another Pulumi stack. For terminated desktops, it checks
the fleet record's environment and region and confirms that the desktop stack
owns no resources. It removes those empty stacks and their versioned state
before destroying the foundation, so a retry can still use the fleet table to
verify ownership. It also verifies the exact configured
Route 53 hosted zone ID and name, and stops if the zone still has desktop or
other records beyond its own NS and SOA records. It then runs
`destroy-foundation`, removes the empty Pulumi stack, deletes the hosted zone,
deletes every version and delete marker in the state bucket, and deletes the
bucket. If any step fails, it stops; correct the cause and rerun it. If the
config has a DNS name without a hosted zone ID, run `dns setup` first to pin
the zone. Remove the `desktops.markcallen.dev` parent NS record at your DNS
provider after cleanup. The wrapper does not delete AMIs, the private base
AMI, or AWS Backup recovery points.

`destroy-foundation --yes` also deletes the setup-created EC2 key pair and
schedules deletion of the configured GitHub and agent secrets with a seven-day
recovery window. It accepts only setup's exact environment-, region-, and
owner-scoped names; it reports and skips custom names for separate review while
still destroying the foundation. The wrapper verifies
that the setup-named key is absent and both setup-named secrets are absent or scheduled for deletion
before removing Pulumi state. The AWS Backup EFS automatic backup vault can
retain a recovery point after its file system is deleted. Check its retention
date and let it expire if a short recovery window is useful; it is managed
separately from the Pulumi and S3 cleanup.
