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


if __name__ == "__main__":
    unittest.main()
