"""Offline safety checks for the installation cleanup wrapper."""

import importlib.util
import json
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


script_path = Path(__file__).with_name("cleanup-installation.py")
spec = importlib.util.spec_from_file_location("cleanup_installation", script_path)
cleanup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cleanup)


class CleanupInstallationTest(unittest.TestCase):
    def test_rejects_unrelated_state_before_deletion(self):
        stack = "foundation-prod-ca-central-1"
        cleanup.validate_state_keys([{"Key": f".pulumi/stacks/foundation/{stack}.json"}], stack)
        cleanup.validate_state_keys([{"Key": f".pulumi/locks/organization/foundation/{stack}/old.json"}], stack)
        for key in ("customer-data", ".pulumi/stacks/foundation/foundation-dev-ca-central-1.json",
                    ".pulumi/stacks/desktop/desktop-123.json"):
            with self.subTest(key=key), self.assertRaisesRegex(RuntimeError, "unrelated object"):
                cleanup.validate_state_keys([{"Key": key}], stack)

    def test_allows_only_verified_terminated_desktop_state(self):
        foundation = "foundation-prod-ca-central-1"
        name = "desktop-d-0123abcd"
        keys = [
            {"Key": f".pulumi/stacks/desktop/{name}.json"},
            {"Key": f".pulumi/history/desktop/{name}/old.json"},
            {"Key": f".pulumi/backups/desktop/{name}/old.json"},
        ]
        with self.assertRaisesRegex(RuntimeError, "unrelated object"):
            cleanup.validate_state_keys(keys, foundation)
        cleanup.validate_state_keys(keys, foundation, {name})
        with self.assertRaisesRegex(RuntimeError, "unrelated object"):
            cleanup.validate_state_keys([{"Key": ".pulumi/stacks/desktop/desktop-d-0123abce.json"}],
                                        foundation, {name})

        empty_stack = json.dumps({"deployment": {"resources": [{"type": "pulumi:pulumi:Stack"}]}})
        with patch.object(cleanup, "terminated_desktops", return_value={"d-0123abcd"}), \
             patch.object(cleanup, "run", side_effect=[json.dumps([{"name": "desktop/" + name}]), empty_stack]) as commands:
            approved, active = cleanup.inspect_desktop_stacks(
                "marka", "ca-central-1", "prod", "desktopctl-fleet-prod-ca-central-1",
                keys, Path("/tmp/desktop"), {})
        self.assertEqual((approved, active), ({name}, {name}))
        self.assertIn("export", commands.call_args.args)

        live_stack = json.dumps({"deployment": {"resources": [
            {"type": "pulumi:pulumi:Stack"}, {"type": "aws:ec2/instance:Instance"}]}})
        with patch.object(cleanup, "terminated_desktops", return_value={"d-0123abcd"}), \
             patch.object(cleanup, "run", side_effect=[json.dumps([{"name": name}]), live_stack]):
            with self.assertRaisesRegex(RuntimeError, "still owns resources"):
                cleanup.inspect_desktop_stacks("marka", "ca-central-1", "prod", "fleet", keys, Path("/tmp/desktop"), {})
        with patch.object(cleanup, "terminated_desktops", return_value=set()):
            with self.assertRaisesRegex(RuntimeError, "no terminated fleet record"):
                cleanup.inspect_desktop_stacks("marka", "ca-central-1", "prod", "fleet", keys, Path("/tmp/desktop"), {})

    def test_terminated_desktops_filters_identity_and_paginates(self):
        def item(desktop_id, state, environment="prod", region="ca-central-1"):
            return {"desktop_id": {"S": desktop_id}, "lifecycle_state": {"S": state},
                    "environment": {"S": environment}, "region": {"S": region}}
        pages = [
            {"Items": [item("d-0123abcd", "terminated"), item("d-0123abce", "ready")],
             "LastEvaluatedKey": {"desktop_id": {"S": "d-0123abce"}}},
            {"Items": [item("d-0123abcf", "terminated", "dev"),
                       item("d-0123abd0", "terminated", region="us-east-1")]},
        ]
        with patch.object(cleanup, "aws", side_effect=pages) as fake:
            ids = cleanup.terminated_desktops("marka", "ca-central-1", "fleet", "prod")
        self.assertEqual(ids, {"d-0123abcd"})
        self.assertIn("--exclusive-start-key", fake.call_args.args)

    def test_paginates_versions_and_delete_markers(self):
        pages = [
            {"Versions": [{"Key": "a", "VersionId": "1"}], "IsTruncated": True,
             "NextKeyMarker": "a", "NextVersionIdMarker": "1"},
            {"DeleteMarkers": [{"Key": "a", "VersionId": "2"}], "IsTruncated": False},
        ]
        with patch.object(cleanup, "aws", side_effect=pages) as fake:
            self.assertEqual(list(cleanup.versions("p", "r", "b", "123")),
                             [{"Key": "a", "VersionId": "1"}, {"Key": "a", "VersionId": "2"}])
        self.assertIn("--key-marker", fake.call_args.args)

    def test_dns_cleanup_requires_pinned_zone(self):
        self.assertIsNone(cleanup.validate_dns_config({}))
        with self.assertRaisesRegex(RuntimeError, "both dns.zone"):
            cleanup.validate_dns_config({"dns": {"zone": "desktops.example.com"}})
        self.assertEqual(cleanup.validate_dns_config({"dns": {
            "zone": "Desktops.Example.Com.", "hosted_zone_id": "/hostedzone/Z123ABC"}}),
            ("desktops.example.com", "/hostedzone/Z123ABC"))

    def test_dns_cleanup_rejects_wrong_or_nonempty_zone(self):
        dns = ("desktops.example.com", "/hostedzone/Z123ABC")
        zone = {"HostedZone": {"Id": "/hostedzone/Z123ABC", "Name": "desktops.example.com.", "Config": {"PrivateZone": False}}}
        records = {"ResourceRecordSets": [
            {"Name": "desktops.example.com.", "Type": "NS"},
            {"Name": "desktops.example.com.", "Type": "SOA"},
        ]}
        with patch.object(cleanup, "aws", side_effect=[zone, records]) as fake:
            self.assertTrue(cleanup.inspect_dns_zone("p", "r", dns))
        self.assertEqual(fake.call_count, 2)
        with patch.object(cleanup, "aws", return_value={"HostedZone": {**zone["HostedZone"], "Name": "other.example.com."}}):
            with self.assertRaisesRegex(RuntimeError, "does not match"):
                cleanup.inspect_dns_zone("p", "r", dns)
        records["ResourceRecordSets"].append({"Name": "desktop.desktops.example.com.", "Type": "A"})
        with patch.object(cleanup, "aws", side_effect=[zone, records]):
            with self.assertRaisesRegex(RuntimeError, "still has DNS records"):
                cleanup.inspect_dns_zone("p", "r", dns)

    def test_dns_cleanup_allows_retry_after_zone_deletion(self):
        with patch.object(cleanup, "aws", side_effect=RuntimeError("NoSuchHostedZone")):
            self.assertFalse(cleanup.inspect_dns_zone("p", "r", ("desktops.example.com", "Z123ABC")))

    def test_apply_deletes_zone_before_state_bucket(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "config.yaml"
            config.write_text("""aws:
  profile: marka
  region: ca-central-1
pulumi:
  backend_bucket: marka-desktopctl-prod-ca-central-1-819363892004
  infra_dir: /tmp/desktopctl
fleet:
  environment: prod
github:
  owner: orchael
dns:
  zone: desktops.example.com
  hosted_zone_id: /hostedzone/Z123ABC
""")
            events = []

            def fake_aws(_profile, _region, service, operation, *args):
                events.append((service, operation))
                if service == "sts":
                    return {"Account": "819363892004"}
                if operation == "get-bucket-location":
                    return {"LocationConstraint": "ca-central-1"}
                if operation == "get-hosted-zone":
                    return {"HostedZone": {"Id": "/hostedzone/Z123ABC", "Name": "desktops.example.com.", "Config": {"PrivateZone": False}}}
                if operation == "list-resource-record-sets":
                    return {"ResourceRecordSets": [{"Name": "desktops.example.com.", "Type": "NS"},
                                                   {"Name": "desktops.example.com.", "Type": "SOA"}]}
                if operation == "list-secrets":
                    return {"SecretList": []}
                return {}

            def fake_run(*args, **_kwargs):
                events.append((args[0], args[1]))
                return "[]" if args[0] == "pulumi" else ""

            with patch.object(cleanup, "aws", side_effect=fake_aws), \
                 patch.object(cleanup, "versions", return_value=iter([])), \
                 patch.object(cleanup, "run", side_effect=fake_run), \
                 patch.object(sys, "argv", [str(script_path), "--config", str(config), "--account-id", "819363892004", "--cli", str(config), "--apply"]):
                cleanup.main()
            self.assertLess(events.index(("route53", "delete-hosted-zone")),
                            events.index(("s3api", "delete-bucket")))

    def test_apply_removes_verified_desktop_state_before_foundation(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "config.yaml"
            config.write_text("""aws:\n  profile: marka\n  region: ca-central-1
pulumi:\n  backend_bucket: marka-desktopctl-prod-ca-central-1-819363892004
  infra_dir: /tmp/desktopctl
fleet:\n  environment: prod
github:\n  owner: orchael
""")
            desktop_key = ".pulumi/stacks/desktop/desktop-d-0123abcd.json"
            objects = [{"Key": desktop_key, "VersionId": "one"}, {"Key": ".pulumi/meta.yaml", "VersionId": "two"}]
            events = []

            def fake_aws(_profile, _region, service, operation, *args):
                events.append((service, operation))
                if service == "sts":
                    return {"Account": "819363892004"}
                if operation == "get-bucket-location":
                    return {"LocationConstraint": "ca-central-1"}
                return {}

            def fake_versions(*_args):
                return iter(objects[:])

            def fake_delete(*_args):
                predicate = _args[-1] if callable(_args[-1]) else None
                events.append(("state", "delete-desktop" if predicate else "delete-all"))
                if predicate:
                    objects[:] = [obj for obj in objects if not predicate(obj)]
                else:
                    objects.clear()

            def fake_run(*args, **_kwargs):
                operation = " ".join(str(arg) for arg in args)
                events.append(("run", operation))
                return "[]" if "stack ls" in operation else ""

            with patch.object(cleanup, "aws", side_effect=fake_aws), \
                 patch.object(cleanup, "versions", side_effect=fake_versions), \
                 patch.object(cleanup, "inspect_desktop_stacks", return_value=({"desktop-d-0123abcd"}, {"desktop-d-0123abcd"})), \
                 patch.object(cleanup, "delete_versions", side_effect=fake_delete), \
                 patch.object(cleanup, "run", side_effect=fake_run), \
                 patch.object(sys, "argv", [str(script_path), "--config", str(config), "--account-id", "819363892004", "--cli", str(config), "--apply"]):
                cleanup.main()
            desktop_delete = events.index(("state", "delete-desktop"))
            foundation_destroy = next(i for i, event in enumerate(events) if event[0] == "run" and "destroy-foundation" in event[1])
            self.assertLess(desktop_delete, foundation_destroy)

    def test_preview_never_runs_destructive_commands(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "config.yaml"
            config.write_text("""aws:\n  profile: marka\n  region: ca-central-1
pulumi:\n  backend_bucket: marka-desktopctl-prod-ca-central-1-819363892004
fleet:\n  environment: prod
github:\n  owner: orchael
  github_secret: /desktopctl/prod/ca-central-1/orchael/github
  agent_secret: /desktopctl/prod/ca-central-1/orchael/agents
desktop:\n  ssh_key_name: desktopctl-prod-ca-central-1-orchael
""")
            def fake_aws(_profile, _region, service, operation, *args):
                if service == "sts":
                    return {"Account": "819363892004"}
                if service == "s3api" and operation == "get-bucket-location":
                    return {"LocationConstraint": "ca-central-1"}
                self.fail(f"unexpected AWS call: {service} {operation}")
            with patch.object(cleanup, "aws", side_effect=fake_aws), \
                 patch.object(cleanup, "versions", return_value=iter([{"Key": ".pulumi/meta.yaml", "VersionId": "1"}])), \
                 patch.object(cleanup, "run") as commands, \
                 patch.object(sys, "argv", [str(script_path), "--config", str(config), "--account-id", "819363892004"]):
                cleanup.main()
            commands.assert_not_called()

    def test_preview_skips_custom_setup_names_without_blocking_foundation(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / "config.yaml"
            config.write_text("""aws:\n  profile: marka\n  region: ca-central-1
pulumi:\n  backend_bucket: marka-desktopctl-prod-ca-central-1-819363892004
fleet:\n  environment: prod
github:\n  owner: orchael
  github_secret: /shared/github
desktop:\n  ssh_key_name: shared-key
""")
            def fake_aws(_profile, _region, service, operation, *args):
                if service == "sts":
                    return {"Account": "819363892004"}
                if service == "s3api" and operation == "get-bucket-location":
                    return {"LocationConstraint": "ca-central-1"}
                self.fail(f"unexpected AWS call: {service} {operation}")
            with patch.object(cleanup, "aws", side_effect=fake_aws), \
                 patch.object(cleanup, "versions", return_value=iter([])), \
                 patch.object(cleanup, "run") as commands, \
                 patch.object(sys, "argv", [str(script_path), "--config", str(config), "--account-id", "819363892004"]):
                cleanup.main()
            commands.assert_not_called()


if __name__ == "__main__":
    unittest.main()
