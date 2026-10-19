"""Offline safety checks for scripts/public-amis.sh."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("public-amis.sh")
ACCOUNT = "819363892004"


def image(ami_id, name, date, *, public=True, owner=ACCOUNT, managed=True):
    return {
        "ImageId": ami_id,
        "Name": name,
        "CreationDate": date,
        "Public": public,
        "OwnerId": owner,
        "Tags": [{"Key": "ManagedBy", "Value": "desktopctl-packer" if managed else "other"}],
    }


class PublicAMIScriptTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.config = root / "config.yaml"
        self.config.write_text("aws:\n  region: ca-central-1\n")
        self.calls = root / "cli-calls"
        self.confirm = root / "cli-confirm"
        self.aws_calls = root / "aws-calls"
        self.block_state = root / "block-state.json"
        self.block_state.write_text(json.dumps({"ImageBlockPublicAccessState": "unblocked", "ManagedBy": "account"}))
        self.images = root / "images.json"
        self.images.write_text(json.dumps({"Images": []}))
        binary = root / "bin"
        binary.mkdir()
        aws = binary / "aws"
        aws.write_text(
            "#!/usr/bin/env python3\n"
            "import json,os,sys\n"
            "a=sys.argv[1:]\n"
            "if a[0]=='sts': print(os.environ['FAKE_ACCOUNT'])\n"
            "elif 'describe-images' in a: print(open(os.environ['FAKE_IMAGES']).read())\n"
            "elif 'describe-instances' in a: print(os.environ.get('FAKE_INSTANCES',''))\n"
            "elif 'get-image-block-public-access-state' in a: print(open(os.environ['FAKE_BLOCK_STATE']).read())\n"
            "elif 'disable-image-block-public-access' in a:\n"
            "  with open(os.environ['FAKE_AWS_CALLS'],'a') as f: f.write(' '.join(a)+'\\n')\n"
            "  state={'ImageBlockPublicAccessState':'unblocked','ManagedBy':'account'}\n"
            "  with open(os.environ['FAKE_BLOCK_STATE'],'w') as f: json.dump(state,f)\n"
            "  print(json.dumps(state))\n"
            "else: sys.exit(3)\n"
        )
        aws.chmod(0o755)
        self.cli = binary / "desktopctl"
        self.cli.write_text(
            "#!/usr/bin/env python3\n"
            "import os,sys\n"
            "with open(os.environ['FAKE_CLI_CALLS'],'a') as f: f.write(' '.join(sys.argv[1:])+'\\n')\n"
            "if 'delete' in sys.argv:\n"
            "  answer=sys.stdin.readline().strip()\n"
            "  with open(os.environ['FAKE_CLI_CONFIRM'],'w') as f: f.write(answer)\n"
            "  if answer != 'yes': sys.exit(4)\n"
        )
        self.cli.chmod(0o755)
        self.env = os.environ.copy()
        self.env.update(
            PATH=str(binary) + os.pathsep + self.env["PATH"],
            FAKE_ACCOUNT=ACCOUNT,
            FAKE_IMAGES=str(self.images),
            FAKE_CLI_CALLS=str(self.calls),
            FAKE_CLI_CONFIRM=str(self.confirm),
            FAKE_AWS_CALLS=str(self.aws_calls),
            FAKE_BLOCK_STATE=str(self.block_state),
        )

    def run_script(self, action, *args, input=None):
        return subprocess.run(
            [str(SCRIPT), action, "--config", str(self.config), "--cli", str(self.cli),
             "--profile", "marka", "--regions", "ca-central-1", "--account-id", ACCOUNT, *args],
            cwd=self.tmp.name, env=self.env, text=True, capture_output=True, check=False, input=input,
        )

    def test_delete_passes_operator_confirmation_to_cli(self):
        self.images.write_text(json.dumps({"Images": [
            image("ami-old0", "desktopctl-v0-20261017-100", "2026-10-17T00:00:00Z"),
        ]}))
        result = self.run_script("delete", "--ami", "ami-old0", "--apply", input="yes\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.confirm.read_text(), "yes")

    def test_list_needs_no_config_or_cli(self):
        self.images.write_text(json.dumps({"Images": [
            image("ami-public", "desktopctl-v0-20261018-100", "2026-10-18T00:00:00Z"),
            image("ami-unmanaged", "desktopctl-v0-20261018-101", "2026-10-18T01:00:00Z", managed=False),
        ]}))
        result = subprocess.run(
            [str(SCRIPT), "list", "--profile", "marka", "--regions", "ca-central-1"],
            cwd=self.tmp.name, env=self.env, text=True, capture_output=True, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Account " + ACCOUNT, result.stdout)
        self.assertIn("ami-public", result.stdout)
        self.assertNotIn("ami-unmanaged", result.stdout)
        self.assertFalse(self.calls.exists())

    def test_explicit_account_mismatch_fails_before_image_query(self):
        result = subprocess.run(
            [str(SCRIPT), "list", "--profile", "marka", "--regions", "ca-central-1",
             "--account-id", "111111111111"],
            cwd=self.tmp.name, env=self.env, text=True, capture_output=True, check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("expected 111111111111", result.stderr)
        self.assertNotIn("region ca-central-1", result.stdout)

    def test_prune_previews_only_old_managed_public_images(self):
        self.images.write_text(json.dumps({"Images": [
            image("ami-old0", "desktopctl-v0-20261017-100", "2026-10-17T00:00:00Z"),
            image("ami-new0", "desktopctl-v0-20261018-200", "2026-10-18T00:00:00Z"),
            image("ami-old1", "desktopctl-v1-20261017-300", "2026-10-17T00:00:00Z"),
            image("ami-new1", "desktopctl-v1-20261018-400", "2026-10-18T00:00:00Z"),
            image("ami-private", "desktopctl-v0-20261016-100", "2026-10-16T00:00:00Z", public=False),
            image("ami-other", "desktopctl-v0-20261015-100", "2026-10-15T00:00:00Z", managed=False),
            image("ami-legacy", "desktopctl-v0-old", "2026-10-14T00:00:00Z"),
        ]}))
        result = self.run_script("prune", "--keep", "1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Candidate: ami-old0", result.stdout)
        self.assertIn("Candidate: ami-old1", result.stdout)
        for excluded in ("ami-new0", "ami-new1", "ami-private", "ami-other", "ami-legacy"):
            self.assertNotIn("Candidate: " + excluded, result.stdout)
        self.assertFalse(self.calls.exists())

    def test_delete_rejects_unmanaged_and_builds_each_region(self):
        self.images.write_text(json.dumps({"Images": [
            image("ami-unmanaged", "desktopctl-v0-20261017-100", "2026-10-17T00:00:00Z", managed=False),
        ]}))
        rejected = self.run_script("delete", "--ami", "ami-unmanaged", "--apply")
        self.assertNotEqual(rejected.returncode, 0)
        self.assertFalse(self.calls.exists())

        built = subprocess.run(
            [str(SCRIPT), "build", "--config", str(self.config), "--cli", str(self.cli),
             "--profile", "marka", "--regions", "ca-central-1,us-east-1"],
            cwd=self.tmp.name, env=self.env, text=True, capture_output=True, check=False,
        )
        self.assertEqual(built.returncode, 0, built.stderr)
        calls = self.calls.read_text()
        self.assertIn("ami build --regions ca-central-1 --public", calls)
        self.assertIn("ami build --regions us-east-1 --public", calls)

    def test_build_enables_public_sharing_in_approved_region(self):
        self.block_state.write_text(json.dumps({"ImageBlockPublicAccessState": "block-new-sharing", "ManagedBy": "account"}))
        result = self.run_script("build", "--regions", "us-east-2", "--enable-public-sharing")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("disable-image-block-public-access", self.aws_calls.read_text())
        self.assertIn("region us-east-2", self.aws_calls.read_text())
        self.assertIn("ami build --regions us-east-2 --public", self.calls.read_text())
        self.assertEqual(json.loads(self.block_state.read_text())["ImageBlockPublicAccessState"], "unblocked")

    def test_build_requires_explicit_flag_to_change_account_setting(self):
        self.block_state.write_text(json.dumps({"ImageBlockPublicAccessState": "block-new-sharing", "ManagedBy": "account"}))
        result = self.run_script("build", "--regions", "us-west-1")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("pass --enable-public-sharing", result.stderr)
        self.assertFalse(self.aws_calls.exists())
        self.assertFalse(self.calls.exists())

    def test_build_rejects_policy_managed_and_unapproved_regions(self):
        self.block_state.write_text(json.dumps({"ImageBlockPublicAccessState": "block-new-sharing", "ManagedBy": "declarative-policy"}))
        policy = self.run_script("build", "--regions", "us-east-1", "--enable-public-sharing")
        self.assertNotEqual(policy.returncode, 0)
        self.assertIn("managed by declarative-policy", policy.stderr)
        self.assertFalse(self.aws_calls.exists())

        self.block_state.write_text(json.dumps({"ImageBlockPublicAccessState": "block-new-sharing", "ManagedBy": "account"}))
        region = self.run_script("build", "--regions", "eu-west-1", "--enable-public-sharing")
        self.assertNotEqual(region.returncode, 0)
        self.assertIn("limited to", region.stderr)
        self.assertFalse(self.aws_calls.exists())
        self.assertFalse(self.calls.exists())

    def test_delete_refuses_image_used_by_instance(self):
        self.images.write_text(json.dumps({"Images": [
            image("ami-in-use", "desktopctl-v0-20261017-100", "2026-10-17T00:00:00Z"),
        ]}))
        self.env["FAKE_INSTANCES"] = "i-0123456789abcdef0"
        result = self.run_script("delete", "--ami", "ami-in-use", "--apply")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to delete", result.stderr)
        self.assertFalse(self.calls.exists())
        self.env["FAKE_INSTANCES"] = ""
        deleted = self.run_script("delete", "--ami", "ami-in-use", "--apply", input="yes\n")
        self.assertEqual(deleted.returncode, 0, deleted.stderr)
        self.assertIn("ami delete ami-in-use", self.calls.read_text())


if __name__ == "__main__":
    unittest.main()
