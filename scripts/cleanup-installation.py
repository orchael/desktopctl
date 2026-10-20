#!/usr/bin/env python3
"""Preview or remove one desktopctl foundation and its dedicated Pulumi state bucket."""

import argparse
from itertools import islice
import json
import os
from pathlib import Path
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


def validate_state_keys(objects, stack):
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
        if key not in allowed and not key.startswith(prefixes):
            raise RuntimeError(f"bucket contains an unrelated object; refusing cleanup: {key}")


def delete_versions(profile, region, bucket, account):
    while True:
        batch = list(islice(versions(profile, region, bucket, account), 1000))
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
    if key and key != expected_key:
        raise RuntimeError(f"custom EC2 key pair {key} requires separate cleanup")
    expected_secrets = [f"/desktopctl/{env_name}/{region}/{owner}/{suffix}" for suffix in ("github", "agents")]
    secrets = [conf["github"].get(field, "") for field in ("github_secret", "agent_secret")]
    if any(actual and actual != expected for actual, expected in zip(secrets, expected_secrets)):
        raise RuntimeError("config has a custom setup secret path; clean it up separately")
    account = aws(profile, region, "sts", "get-caller-identity")["Account"]
    if account != args.account_id:
        raise RuntimeError(f"AWS account {account} does not match expected {args.account_id}")
    location = aws(profile, region, "s3api", "get-bucket-location", "--bucket", bucket,
                   "--expected-bucket-owner", account).get("LocationConstraint") or "us-east-1"
    if location != region:
        raise RuntimeError(f"bucket is in {location}, expected {region}")
    objects = list(versions(profile, region, bucket, account))
    validate_state_keys(objects, stack)
    print(f"Account {account}, region {region}, stack {stack}, bucket {bucket}")
    print(f"State bucket: {len(objects)} object versions and delete markers; EC2 key pair: {key or 'none'}")
    print("Setup secrets: " + ", ".join(s for s in secrets if s))
    if not args.apply:
        print("Preview only. Pass --apply to destroy the foundation and delete the dedicated state bucket.")
        return
    if not args.cli.is_file():
        raise RuntimeError(f"desktopctl binary missing: {args.cli}; run make build")
    run(str(args.cli), "--config", str(args.config.resolve()), "destroy-foundation", "--yes", output=False)
    verify_setup_cleanup(profile, region, key, [s for s in secrets if s])
    pulumi_env = dict(os.environ, AWS_PROFILE=profile, AWS_REGION=region, AWS_DEFAULT_REGION=region,
                      PULUMI_BACKEND_URL="s3://" + bucket, PULUMI_CONFIG_PASSPHRASE="")
    workdir = Path(conf["pulumi"]["infra_dir"]) / "infra/pulumi/foundation"
    stack_list = json.loads(run("pulumi", "--cwd", str(workdir), "stack", "ls", "--json", env=pulumi_env))
    if any(item["name"].split("/")[-1] == stack for item in stack_list):
        run("pulumi", "--cwd", str(workdir), "stack", "rm", stack, "--yes", "--non-interactive", env=pulumi_env, output=False)
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
