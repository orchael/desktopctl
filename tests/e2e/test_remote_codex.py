"""Offline subprocess regressions for the desktop-side output controller."""

import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("remote_codex", Path(__file__).with_name("remote_codex.py"))
SCENARIO = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SCENARIO)


class DriverTests(unittest.TestCase):
    def test_transient_401_then_success_does_not_fail_while_refreshing(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            meta = base / "meta.json"
            meta.write_text(json.dumps({"sentinel": "CODEX_E2E_OK"}))
            result = base / "first-result.json"
            # A real child emits an initial 401, then waits for simulated refresh
            # before producing the successful assistant response and exiting.
            child = """
import json, pathlib, sys, time
base = pathlib.Path(sys.argv[1])
print('401 Unauthorized; refreshing account token', flush=True)
(base / 'initial-error').touch()
while not (base / 'refresh-complete').exists():
    time.sleep(0.01)
print(json.dumps({'type':'item.completed','item':{'type':'agent_message','text':'CODEX_E2E_OK'}}), flush=True)
"""
            original_popen = subprocess.Popen

            def launch(_args, **kwargs):
                return original_popen([sys.executable, "-c", child, str(base)], **kwargs)

            failures = []

            def run_driver():
                try:
                    SCENARIO.driver("first")
                except Exception as error:
                    failures.append(error)

            with mock.patch.object(SCENARIO, "BASE", base), mock.patch.object(SCENARIO, "META", meta), mock.patch.object(SCENARIO.subprocess, "Popen", launch):
                thread = threading.Thread(target=run_driver)
                thread.start()
                try:
                    deadline = time.monotonic() + 5
                    while not (base / "initial-error").exists() and time.monotonic() < deadline:
                        time.sleep(0.01)
                    self.assertTrue((base / "initial-error").exists())
                    time.sleep(0.1)
                    self.assertFalse(result.exists(), "a retriable 401 must not end the scenario")
                finally:
                    (base / "refresh-complete").touch()
                    thread.join(timeout=5)
                self.assertFalse(thread.is_alive())
                self.assertEqual(failures, [])
                self.assertEqual(json.loads(result.read_text()), {"answered": True})

    def test_terminal_401_without_answer_reports_safe_category(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            meta = base / "meta.json"
            meta.write_text(json.dumps({"sentinel": "CODEX_E2E_OK"}))
            original_popen = subprocess.Popen

            def launch(_args, **kwargs):
                return original_popen([sys.executable, "-c", "print('401 Unauthorized PRIVATE_VALUE', flush=True)"], **kwargs)

            with mock.patch.object(SCENARIO, "BASE", base), mock.patch.object(SCENARIO, "META", meta), mock.patch.object(SCENARIO.subprocess, "Popen", launch):
                SCENARIO.driver("first")
            result = (base / "first-result.json").read_text()
            self.assertEqual(json.loads(result), {"answered": False, "category": "authentication_failed"})
            self.assertNotIn("PRIVATE_VALUE", result)


class AuthPathTests(unittest.TestCase):
    def test_innermost_mount_detects_nfs_beneath_local_home(self):
        mounts = """1 0 8:1 / / rw - ext4 /dev/root rw
2 1 0:2 / /home/ubuntu/.codex rw - nfs4 fs-test:/ rw
3 1 0:3 / /home/ubuntu/other\\040dir rw - nfs4 fs-other:/ rw
"""
        self.assertEqual(SCENARIO.mount_filesystem(Path("/home/ubuntu/.codex/auth.json"), mounts), "nfs4")
        self.assertEqual(SCENARIO.mount_filesystem(Path("/home/ubuntu/local/auth.json"), mounts), "ext4")
        self.assertEqual(SCENARIO.mount_filesystem(Path("/home/ubuntu/other dir/auth.json"), mounts), "nfs4")

    def test_only_private_local_auth_under_home_is_allowed(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory) / "home"
            auth_home = home / ".codex"
            auth_home.mkdir(parents=True, mode=0o700)
            auth_file = auth_home / "auth.json"
            auth_file.write_text("{}")
            auth_file.chmod(0o600)
            with mock.patch.object(Path, "home", return_value=home), mock.patch.object(SCENARIO, "mount_filesystem", return_value="ext4"):
                self.assertEqual(SCENARIO.private_auth_path(auth_home), auth_file)
                for candidate in (Path("relative"), Path(directory)):
                    with self.assertRaises(AssertionError):
                        SCENARIO.private_auth_path(candidate)
                auth_home.chmod(0o755)
                with self.assertRaises(AssertionError):
                    SCENARIO.private_auth_path(auth_home)
                auth_home.chmod(0o700)
                auth_file.chmod(0o644)
                with self.assertRaises(AssertionError):
                    SCENARIO.private_auth_path(auth_home)
            auth_file.chmod(0o600)
            with mock.patch.object(Path, "home", return_value=home), mock.patch.object(SCENARIO, "mount_filesystem", return_value="nfs4"):
                with self.assertRaises(AssertionError):
                    SCENARIO.private_auth_path(auth_home)

    def test_symlink_escape_is_rejected_before_reading_auth(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory) / "home"
            workspace = Path(directory) / "workspace"
            home.mkdir()
            workspace.mkdir(mode=0o700)
            shared_auth = workspace / "auth.json"
            shared_auth.write_text("DO NOT READ OR MODIFY")
            shared_auth.chmod(0o600)
            auth_home = home / ".codex"
            auth_home.symlink_to(workspace, target_is_directory=True)
            with mock.patch.object(Path, "home", return_value=home):
                with self.assertRaises(AssertionError):
                    SCENARIO.private_auth_path(auth_home)
                auth_home.unlink()
                auth_home.mkdir(mode=0o700)
                (auth_home / "auth.json").symlink_to(shared_auth)
                with self.assertRaises(AssertionError):
                    SCENARIO.private_auth_path(auth_home)
            self.assertEqual(shared_auth.read_text(), "DO NOT READ OR MODIFY")


if __name__ == "__main__":
    unittest.main()
