# Public AMI naming and lifecycle

New published images use `desktopctl-v<major>-<YYYYMMDD>-<timestamp>`. The major
version comes from the CLI release that starts the build. The date is UTC and
the final Packer timestamp makes repeat builds on the same day distinct. For
example, CLI `1.4.2` produces a name like
`desktopctl-v1-20261018-1792310400`. The image also carries
`DesktopctlVersion=1.4.2`, `DesktopctlMajor=1`, `BuildDate=20261018`,
`DesktopWebVersion`, `BridgeVersion`, `NovncDesktopVersion`, and `BaseAMI`
tags. The name is a compatibility lane; the tags retain the exact build inputs.
The current CLI release is `0.7.0`, so its automatic lane is `v0`.
That lane includes all `0.x` releases; pin an exact `active_ami` if a minor
release is incompatible while the project remains below version 1.

`desktopctl create` selects images in this order:

1. Explicit `--ami` on the command line.
2. `desktop.active_ami.<region>` in the selected config file.
3. The newest available **public** `desktopctl-v<major>-*` image owned by
   `desktop.public_ami_owner` in the selected region.

The default major is the running CLI's major version. Set
`desktop.public_ami_major: "1"` to discover another major, or set
`desktop.active_ami.<region>` to pin an exact image. A publisher build writes
its new image to `active_ami` in the publisher's config; consumer configs can
leave that map empty to follow the newest image in their chosen major lane.
Old images named after the bridge version do not participate in automatic
discovery.

## Build and inspect

The script requires an explicit AWS profile and region list. It discovers the
account ID through STS using that profile. Pass `--account-id` when you want an
additional check against using the wrong account. The script resolves and checks
the account before any other AWS operation.

Build, delete, and prune also require a config file and desktopctl binary;
list reads EC2 directly and needs neither. AWS CLI is required for all actions,
and `jq` for list, delete, and prune. Build invokes `desktopctl ami build --public`
once per region and saves each successful
region's active image in the selected config. The GitHub Packages token can
come from the regional secret created by `desktopctl setup`.
Before starting Packer, a public build checks the region's account-level AMI
Block Public Access state. In ca-central-1, us-east-1, us-east-2, us-west-1,
and us-west-2, pass `--enable-public-sharing` to let the script disable the
block when it is managed by the account. It leaves public sharing enabled.
The AWS profile must resolve to the intended
account. A declarative organization policy or a blocked region outside that
list stops the build. Direct `desktopctl ami build --public` checks the state
and stops before provisioning when public sharing is blocked.

```bash
make build
scripts/public-amis.sh build \
  --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --profile marka \
  --regions ca-central-1 --enable-public-sharing

scripts/public-amis.sh list \
  --profile marka \
  --regions ca-central-1,us-east-1
```

Each build region needs a launchable noVNC base AMI and a usable Packer subnet
or default VPC. The CLI records AMI history in the control region's DynamoDB
table. `list` shows only available, public, account-owned AMIs with the exact
new name pattern and `ManagedBy=desktopctl-packer` tag.

## Delete old images

`prune` keeps the newest two images **per major and per region** by default.
It prints candidates without deleting anything. `delete` previews one exact
AMI ID. Pass `--apply` to either command to request deletion; the script then
checks for pending, running, stopping, or stopped instances using each image,
and the CLI asks for `yes` before deregistering the AMI and deleting its
snapshots. The CLI refuses to delete an image pinned as `active_ami` in this
config. Images used by other AWS accounts cannot be detected through the
publisher's EC2 instance query; retain an appropriate rollback window before
removing published versions.

```bash
scripts/public-amis.sh prune \
  --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --profile marka \
  --regions ca-central-1,us-east-1 --keep 2

# After reviewing the printed IDs:
scripts/public-amis.sh prune \
  --config ~/.config/desktopctl/config-ca-central-1.yaml \
  --profile marka \
  --regions ca-central-1,us-east-1 --keep 2 --apply
```
