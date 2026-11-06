# Parking an EFS desktop

Parking is for an EFS-backed desktop that will be idle long enough to justify
deleting its EC2 instance and root gp3 EBS volume. `stop` hibernates the
instance and retains the volume, RAM image, home directory, and installed
packages. `park` retains only the named EFS `/workspace` and a logical fleet
record. Restoring takes a new-instance boot and readiness check rather than a
hibernation resume. The EC2 instance ID changes; the desktop ID and name stay
the same. EFS storage remains billable while parked.

```sh
desktopctl park d-0123abcd --dry-run
desktopctl park d-0123abcd
desktopctl unpark d-0123abcd
```

The dry run reads the fleet, EFS, and EC2 records without writing. It shows the
exact root volume ID and size, EFS file system and access point, and the local
state that will be lost. Parking requires typing the desktop ID, or `--force`
for automation. Local workspaces, nested virtualization guests, and AVDs are
ineligible because their machine-local state cannot be restored safely.

The fleet record retains a version 1 launch specification containing the AMI,
root size, network and workspace settings, and the non-secret bootstrap inputs.
Unpark regenerates cloud-init using the current fleet secret paths, so paths
removed after creation are not restored and paths added later are included.
The new instance fetches current secret values at boot. Older desktops without
a saved bootstrap specification cannot be parked safely; recreate them with
this CLI before using park.
Keep the recorded AMI available to the account. Configure `desktop.ssh_key_path`
so unpark can verify Docker, bridgectl, repository checkouts, and the EFS mount
before reporting `ready`.

The workspace remains attached to the logical desktop while parked. Another
desktop cannot claim it, and `workspace delete` remains blocked. `start` on a
parked desktop directs you to `unpark`. `terminate` on a parked desktop releases
the workspace claim while retaining the EFS data.

## Recovery

If destroy fails, the fleet record is `failed` with phase `park`; rerun
`desktopctl park <id> --force` after inspecting the failed Pulumi stack. The retry reconciles the
same root volume ID and confirms its deletion before reporting `parked`. If a
process was interrupted while the record says `parking`, first confirm that
no other lifecycle process is running, then use `park --force`. A failed
restore leaves the workspace attached and the record in `failed` with phase
`unpark`; run `unpark` again. If the process was interrupted while the record
says `restoring`, confirm that it is no longer running and use `unpark --retry`.
Pulumi up will reconcile a partially created stack.

An interrupted `start` or `stop` remains in `starting` or `stopping` until
reconciled. After 30 minutes and after confirming the prior CLI process has
exited, use `desktopctl start <id> --retry` or `desktopctl stop <id> --retry`.
The command checks the live EC2 state before continuing. An interrupted
`terminate` can be resumed with `desktopctl terminate <id> --force` after two
hours and after confirming that the prior Pulumi operation has ended. The
longer wait avoids cancelling a still-active destroy. The same force path can
clean up an abandoned `starting`, `stopping`, or `resizing` desktop.

## Live validation

Run in a disposable environment with a retained EFS workspace:

1. Create a desktop with `--workspace-mode efs --workspace-name <name>` and
   write a unique marker under `/workspace`. Record its desktop ID and EC2
   instance ID from `desktopctl status <id> --json`.
2. Run `desktopctl park <id> --dry-run`. Record the printed `vol-...` ID; confirm
   the command has not changed the fleet state or instance.
3. Run `desktopctl park <id>` and confirm `desktopctl status <id> --json` reports
   `parked` with an empty `instance_id`. Check the old volume with
   `aws ec2 describe-volumes --volume-ids <old-volume-id> --region <region>`;
   `InvalidVolume.NotFound` is expected. Check the old instance is terminated.
4. Run `desktopctl workspace status <name>` and confirm it remains attached to
   the same desktop. Run `desktopctl unpark <id>`; confirm a **different** EC2
   instance ID and `ready` state. Run `desktopctl doctor <id> --wait` and check
   the marker file under `/workspace` through SSH.
5. Terminate the desktop, then confirm the EFS workspace remains and its claim
   is released. Delete the workspace separately when no longer needed.

The procedure provisions chargeable AWS resources. Unit tests cannot establish
that a real root volume was deleted or that an EFS mount worked in AWS.
