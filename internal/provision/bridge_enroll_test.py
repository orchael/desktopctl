"""Contract checks for the short-lived Bridge enrollment helper."""

import io
import json
import os
import subprocess
import unittest
from unittest import mock

import bridge_enroll


class BridgeEnrollTest(unittest.TestCase):
    def test_passes_only_selected_credentials_to_login(self):
        values = {
            "BRIDGECTL_CLIENT_ID": "client",
            "BRIDGECTL_CLIENT_SECRET": "private-value",
            "BRIDGECTL_OIDC_ISSUER": "https://issuer.example",
            "BRIDGECTL_OIDC_PROJECT_ID": "project",
        }
        inherited = {
            "BRIDGECTL_ENROLL_KEY": "stale-key",
            "BRIDGECTL_STATE_DIR": "/tmp/wrong-state",
        }
        with mock.patch("sys.stdin", io.StringIO(json.dumps(values))), mock.patch.dict(
            os.environ, inherited
        ), mock.patch("bridge_enroll.subprocess.run") as run:
            bridge_enroll.main()
        run.assert_called_once()
        args, kwargs = run.call_args
        self.assertEqual(args[0], ["/usr/bin/bridgectl", "login", "--non-interactive"])
        self.assertTrue(kwargs["check"])
        self.assertEqual(kwargs["env"]["BRIDGECTL_CLIENT_SECRET"], "private-value")
        self.assertNotIn("BRIDGECTL_ENROLL_KEY", kwargs["env"])
        self.assertNotIn("BRIDGECTL_STATE_DIR", kwargs["env"])
        self.assertEqual(kwargs["env"]["HOME"], "/home/ubuntu")

    def test_rejects_invalid_secret_without_running_login(self):
        for values in ([], {}, {"EXTRA": "value"}, {"BRIDGECTL_ENROLL_KEY": 1}):
            with self.subTest(values=values), mock.patch(
                "sys.stdin", io.StringIO(json.dumps(values))
            ), mock.patch("bridge_enroll.subprocess.run") as run:
                with self.assertRaises(ValueError):
                    bridge_enroll.main()
                run.assert_not_called()

    def test_propagates_login_failure(self):
        with mock.patch(
            "sys.stdin", io.StringIO('{"BRIDGECTL_ENROLL_KEY":"key"}')
        ), mock.patch(
            "bridge_enroll.subprocess.run",
            side_effect=subprocess.CalledProcessError(1, ["bridgectl"]),
        ):
            with self.assertRaises(subprocess.CalledProcessError):
                bridge_enroll.main()


if __name__ == "__main__":
    unittest.main()
