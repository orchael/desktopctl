#!/usr/bin/env python3
"""Preview or remove one desktopctl foundation, DNS zone, and state bucket."""

import argparse
from itertools import islice
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

import yaml


def run(*args, env=None, output=True):
    result = subprocess.run(args, env=env, text=True, capture_output=output, check=False)
    if result.returncode:
        raise RuntimeError(f"{' '.join(args[:3])} failed: {result.stderr.strip() if output else result.returncode}")
    return result.stdout if output else ""


def aws(profile, region, *args):
    output = run("aws", *args, "--profile", profile, "--region", region, "--output", "json")
    return json.loads(output) if output.strip() else {}


def versions(profile, region, bucket, account):
    key_marker = version_marker = None
    while True:
        args = ["s3api", "list-object-versions", "--bucket", bucket,
                "--expected-bucket-owner", account, "--max-keys", "1000", "--no-paginate"]
        if key_marker:
            args += ["--key-marker", key_marker]
        if version_marker:
            args += ["--version-id-marker", version_marker]
        page = aws(profile, region, *args)
        for group in ("Versions", "DeleteMarkers"):
            for obj in page.get(group, []):
                yield {"Key": obj["Key"], "VersionId": obj["VersionId"]}
        if not page.get("IsTruncated"):
            return
        key_marker = page.get("NextKeyMarker")
        version_marker = page.get("NextVersionIdMarker")
        if not key_marker:
            raise RuntimeError("S3 returned a truncated version listing without a continuation marker")


def desktop_stack_from_key(key):
    patterns = (
        r"\.pulumi/stacks/desktop/(desktop-d-[0-9a-f]{8})\.json(?:\.bak)?",
        r"\.pulumi/(?:backups|history)/desktop/(desktop-d-[0-9a-f]{8})/.+",
        r"\.pulumi/locks/organization/desktop/(desktop-d-[0-9a-f]{8})/.+",
    )
    for pattern in patterns:
        match = re.fullmatch(pattern, key)
        if match:
            return match.group(1)
    return None


def validate_state_keys(objects, stack, desktop_stacks=()):
    allowed = (
        f".pulumi/stacks/foundation/{stack}.json",
        f".pulumi/stacks/foundation/{stack}.json.bak",
        ".pulumi/meta.yaml",
    )
    prefixes = tuple(f".pulumi/{part}/foundation/{stack}/" for part in ("backups", "history")) + (
        f".pulumi/locks/organization/foundation/{stack}/",
    )
    for obj in objects:
        key = obj["Key"]
        if key not in allowed and not key.startswith(prefixes) and desktop_stack_from_key(key) not in desktop_stacks:
            raise RuntimeError(f"bucket contains an unrelated object; refusing cleanup: {key}")


def terminated_desktops(profile, region, table, environment):
    ids = set()
    marker = None
    while True:
        args = ["dynamodb", "scan", "--table-name", table, "--limit", "100", "--no-paginate",
                "--projection-expression", "desktop_id,lifecycle_state,#e,#r",
                "--expression-attribute-names", '{"#e":"environment","#r":"region"}']
        if marker:
            args += ["--exclusive-start-key", json.dumps(marker)]
        page = aws(profile, region, *args)
        for item in page.get("Items", []):
            if (item.get("lifecycle_state", {}).get("S") == "terminated"
                    and item.get("environment", {}).get("S") == environment
                    and item.get("region", {}).get("S") == region):
                ids.add(item["desktop_id"]["S"])
        marker = page.get("LastEvaluatedKey")
        if not marker:
            return ids


def inspect_desktop_stacks(profile, region, environment, table, objects, workdir, pulumi_env):
    candidates = {name for obj in objects if (name := desktop_stack_from_key(obj["Key"]))}
    if not candidates:
        return set(), set()
    known_ids = terminated_desktops(profile, region, table, environment)
    for name in candidates:
        if name.removeprefix("desktop-") not in known_ids:
            raise RuntimeError(f"desktop stack {name} has no terminated fleet record in {table}; refusing cleanup")
    listed = json.loads(run("pulumi", "--cwd", str(workdir), "stack", "ls", "--json", env=pulumi_env))
    active = {item["name"].split("/")[-1] for item in listed}
    for name in sorted(candidates & active):
        state = json.loads(run("pulumi", "--cwd", str(workdir), "stack", "export", "--stack", name, env=pulumi_env))
        deployment = state.get("deployment")
        if not isinstance(deployment, dict) or not isinstance(deployment.get("resources"), list):
            raise RuntimeError(f"desktop stack {name} has an unreadable deployment; refusing cleanup")
        resources = [resource for resource in deployment.get("resources", [])
                     if resource.get("type") != "pulumi:pulumi:Stack"]
        if resources or deployment.get("pending_operations") or deployment.get("pendingOperations"):
            raise RuntimeError(f"desktop stack {name} still owns resources or has pending operations; terminate it first")
    return candidates, candidates & active


def delete_versions(profile, region, bucket, account, predicate=None):
    while True:
        matching = (obj for obj in versions(profile, region, bucket, account)
                    if predicate is None or predicate(obj))
        batch = list(islice(matching, 1000))
        if not batch:
            return
        with tempfile.NamedTemporaryFile(mode="w", prefix="desktopctl-s3-delete-", suffix=".json") as request:
            json.dump({"Objects": batch, "Quiet": True}, request)
            request.flush()
            result = aws(profile, region, "s3api", "delete-objects", "--bucket", bucket,
                         "--expected-bucket-owner", account, "--delete", "file://" + request.name)
        if result.get("Errors"):
            raise RuntimeError(f"S3 failed to delete {len(result['Errors'])} object versions")
        print(f"Deleted {len(batch)} S3 object versions or delete markers.")


def verify_setup_cleanup(profile, region, key, secrets):
    if key:
        found = aws(profile, region, "ec2", "describe-key-pairs", "--filters", f"Name=key-name,Values={key}")
        if found.get("KeyPairs"):
            raise RuntimeError(f"EC2 key pair remains: {key}")
    found = aws(profile, region, "secretsmanager", "list-secrets", "--include-planned-deletion")
    current = {item["Name"]: item for item in found.get("SecretList", [])}
    for name in secrets:
        if name in current and not current[name].get("DeletedDate"):
            raise RuntimeError(f"setup secret remains active: {name}")


def validate_dns_config(conf):
    dns = conf.get("dns") or {}
    name = (dns.get("zone") or "").rstrip(".").lower()
    zone_id = dns.get("hosted_zone_id") or ""
    if not name and not zone_id:
        return None
    if not name or not re.fullmatch(r"(?:/hostedzone/)?Z[A-Z0-9]+", zone_id):
        raise RuntimeError("DNS cleanup requires both dns.zone and its exact dns.hosted_zone_id")
    return name, zone_id


def inspect_dns_zone(profile, region, dns):
    if dns is None:
        return False
    name, zone_id = dns
    try:
        zone = aws(profile, region, "route53", "get-hosted-zone", "--id", zone_id)["HostedZone"]
    except RuntimeError as exc:
        if "NoSuchHostedZone" in str(exc):
            return False  # A previous cleanup run may already have deleted it.
        raise
    if (zone.get("Id", "").split("/")[-1] != zone_id.split("/")[-1]
            or zone.get("Name", "").rstrip(".").lower() != name):
        raise RuntimeError(f"hosted zone {zone_id} does not match configured DNS name {name}")
    if zone.get("Config", {}).get("PrivateZone"):
        raise RuntimeError(f"hosted zone {zone_id} is private; refusing DNS cleanup")
    records = aws(profile, region, "route53", "list-resource-record-sets", "--hosted-zone-id", zone_id)
    extras = [f"{record.get('Name')} {record.get('Type')}" for record in records.get("ResourceRecordSets", [])
              if (record.get("Name", "").rstrip(".").lower() != name
                  or record.get("Type") not in ("NS", "SOA"))]
    if extras:
        raise RuntimeError(f"hosted zone {zone_id} still has DNS records: {', '.join(extras[:5])}; remove desktops and records first")
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--account-id", required=True, help="expected 12-digit AWS account ID")
    parser.add_argument("--cli", type=Path, default=Path(__file__).resolve().parents[1] / "bin/desktopctl")
    parser.add_argument("--apply", action="store_true", help="destroy resources and remove all state bucket versions")
    args = parser.parse_args()
    if not (args.account_id.isdigit() and len(args.account_id) == 12):
        parser.error("--account-id must be 12 digits")
    conf = yaml.safe_load(args.config.read_text())
    profile = conf["aws"]["profile"]
    region = conf["aws"]["region"]
    env_name = conf["fleet"]["environment"]
    bucket = conf["pulumi"]["backend_bucket"]
    owner = conf["github"]["owner"]
    if not all((profile, region, env_name, bucket, owner)) or "desktopctl" not in bucket:
        raise RuntimeError("config must specify profile, region, environment, owner, and a dedicated desktopctl bucket")
    stack = f"foundation-{env_name}-{region}"
    expected_key = f"desktopctl-{env_name}-{region}-{owner}"
    key = conf.get("desktop", {}).get("ssh_key_name", "")
    skipped_setup = []
    if key and key != expected_key:
        skipped_setup.append("custom EC2 key pair requires separate cleanup")
        key = ""
    expected_secrets = [f"/desktopctl/{env_name}/{region}/{owner}/{suffix}" for suffix in ("github", "agents")]
    secrets = []
    for field, expected in zip(("github_secret", "agent_secret"), expected_secrets):
        actual = conf["github"].get(field, "")
        if actual and actual != expected:
            skipped_setup.append("custom setup secret requires separate cleanup")
        elif actual:
            secrets.append(actual)
    dns = validate_dns_config(conf)
    account = aws(profile, region, "sts", "get-caller-identity")["Account"]
    if account != args.account_id:
        raise RuntimeError(f"AWS account {account} does not match expected {args.account_id}")
    location = aws(profile, region, "s3api", "get-bucket-location", "--bucket", bucket,
                   "--expected-bucket-owner", account).get("LocationConstraint") or "us-east-1"
    if location != region:
        raise RuntimeError(f"bucket is in {location}, expected {region}")
    objects = list(versions(profile, region, bucket, account))
    pulumi_env = dict(os.environ, AWS_PROFILE=profile, AWS_REGION=region, AWS_DEFAULT_REGION=region,
                      PULUMI_BACKEND_URL="s3://" + bucket, PULUMI_CONFIG_PASSPHRASE="")
    infra_dir = Path(conf["pulumi"].get("infra_dir") or Path(__file__).resolve().parents[1])
    desktop_dir = infra_dir / "infra/pulumi/desktop"
    fleet_table = conf["fleet"].get("table_name") or f"desktopctl-fleet-{env_name}-{region}"
    desktop_stacks = {name for obj in objects if (name := desktop_stack_from_key(obj["Key"]))}
    validate_state_keys(objects, stack, desktop_stacks)
    desktop_stacks, active_desktop_stacks = inspect_desktop_stacks(
        profile, region, env_name, fleet_table, objects, desktop_dir, pulumi_env)
    dns_exists = inspect_dns_zone(profile, region, dns)
    print(f"Account {account}, region {region}, stack {stack}, bucket {bucket}")
    print(f"State bucket: {len(objects)} object versions and delete markers; EC2 key pair: {key or 'none'}")
    if desktop_stacks:
        print(f"Terminated desktop stacks: {', '.join(sorted(desktop_stacks))}")
    print(f"Setup secrets selected for cleanup: {len(secrets)}")
    for item in skipped_setup:
        print(f"Skipping setup resource: {item}")
    if dns:
        print(f"Route 53 hosted zone: {dns[0]} ({dns[1]}) — {'delete after foundation cleanup' if dns_exists else 'already absent'}")
    if not args.apply:
        print("Preview only. Pass --apply to destroy the foundation and delete the dedicated state bucket.")
        return
    if not args.cli.is_file():
        raise RuntimeError(f"desktopctl binary missing: {args.cli}; run make build")
    for name in sorted(active_desktop_stacks):
        run("pulumi", "--cwd", str(desktop_dir), "stack", "rm", name,
            "--yes", "--non-interactive", env=pulumi_env, output=False)
    # Remove validated empty desktop state before the foundation destroys the
    # fleet table used to prove ownership. A retry can then revalidate any
    # remaining desktop state while that table still exists.
    objects = list(versions(profile, region, bucket, account))
    validate_state_keys(objects, stack, desktop_stacks)
    delete_versions(profile, region, bucket, account,
                    lambda obj: desktop_stack_from_key(obj["Key"]) in desktop_stacks)
    if any(desktop_stack_from_key(obj["Key"]) for obj in versions(profile, region, bucket, account)):
        raise RuntimeError("desktop state remains in the bucket; foundation was not destroyed")
    run(str(args.cli), "--config", str(args.config.resolve()), "destroy-foundation", "--yes", output=False)
    verify_setup_cleanup(profile, region, key, [s for s in secrets if s])
    workdir = infra_dir / "infra/pulumi/foundation"
    stack_list = json.loads(run("pulumi", "--cwd", str(workdir), "stack", "ls", "--json", env=pulumi_env))
    if any(item["name"].split("/")[-1] == stack for item in stack_list):
        run("pulumi", "--cwd", str(workdir), "stack", "rm", stack, "--yes", "--non-interactive", env=pulumi_env, output=False)
    if inspect_dns_zone(profile, region, dns):
        aws(profile, region, "route53", "delete-hosted-zone", "--id", dns[1])
        print(f"Deleted Route 53 hosted zone {dns[0]} ({dns[1]}). Remove its parent NS delegation at your DNS provider.")
    # Recheck after Pulumi has written its final metadata and backup objects.
    objects = list(versions(profile, region, bucket, account))
    validate_state_keys(objects, stack)
    delete_versions(profile, region, bucket, account)
    if next(versions(profile, region, bucket, account), None):
        raise RuntimeError("state bucket still has object versions")
    aws(profile, region, "s3api", "delete-bucket", "--bucket", bucket, "--expected-bucket-owner", account)
    print("Foundation cleanup complete; dedicated state bucket deleted.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, KeyError, ValueError, RuntimeError, yaml.YAMLError) as exc:
        print(f"cleanup-installation: {exc}", file=sys.stderr)
        sys.exit(1)
