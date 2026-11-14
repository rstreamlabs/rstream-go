"""The runtime deadline must fail closed and clean up descendant processes."""

from pathlib import Path
import os
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch
import run_with_deadline as deadline

HELPER = Path(__file__).with_name("run_with_deadline.py")


class DeadlineTests(unittest.TestCase):
    def run_command(self, seconds, source, **kwargs):
        return subprocess.run(
            [sys.executable, str(HELPER), str(seconds), sys.executable, "-c", source],
            capture_output=True,
            text=True,
            timeout=8,
            **kwargs,
        )

    def test_io_and_exit_status_are_preserved(self):
        result = self.run_command(
            5,
            "import sys; print(sys.stdin.read()); print('error', file=sys.stderr); sys.exit(7)",
            input="input",
        )
        self.assertEqual(result.returncode, 7)
        self.assertEqual(result.stdout, "input\n")
        self.assertEqual(result.stderr, "error\n")

    def test_invalid_deadline_never_executes(self):
        for value in (0, -1, "nan", "inf"):
            with self.subTest(value=value):
                result = self.run_command(value, "print('executed')")
                self.assertEqual(result.returncode, 2)
                self.assertNotIn("executed", result.stdout)

    def test_child_signal_status_is_preserved(self):
        result = self.run_command(
            5, "import os, signal; os.kill(os.getpid(), signal.SIGTERM)"
        )
        self.assertEqual(result.returncode, 128 + signal.SIGTERM)

    def test_timeout_is_failure(self):
        result = self.run_command(0.1, "import time; time.sleep(60)")
        self.assertEqual(result.returncode, 124)
        self.assertIn("exceeded", result.stderr)

    def test_descendant_cannot_keep_output_open_after_parent_exit(self):
        source = (
            "import subprocess, sys; "
            "subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(60)']); "
            "print('done')"
        )
        result = self.run_command(5, source)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "done\n")

    def test_darwin_cleanup_verifies_group_after_permission_error(self):
        process = subprocess.CompletedProcess([], 0)
        process.pid = 12345
        for snapshot in ("42 S\n", "12345 Z+\n"):
            with (
                self.subTest(snapshot=snapshot),
                patch.object(deadline.sys, "platform", "darwin"),
                patch.object(deadline.os, "killpg", side_effect=PermissionError),
                patch.object(
                    deadline.subprocess,
                    "run",
                    return_value=subprocess.CompletedProcess([], 0, snapshot),
                ),
            ):
                deadline.signal_group(process, signal.SIGKILL)

    def test_permission_errors_with_live_or_unknown_group_fail_closed(self):
        process = subprocess.CompletedProcess([], 0)
        process.pid = 12345
        for snapshot in ("12345 S+\n", "12345 R\n"):
            with (
                self.subTest(snapshot=snapshot),
                patch.object(deadline.sys, "platform", "darwin"),
                patch.object(deadline.os, "killpg", side_effect=PermissionError),
                patch.object(
                    deadline.subprocess,
                    "run",
                    return_value=subprocess.CompletedProcess([], 0, snapshot),
                ),
                self.assertRaises(PermissionError),
            ):
                deadline.signal_group(process, signal.SIGKILL)
        with (
            patch.object(deadline.sys, "platform", "darwin"),
            patch.object(deadline.os, "killpg", side_effect=PermissionError),
            patch.object(
                deadline.subprocess,
                "run",
                side_effect=subprocess.TimeoutExpired("ps", 1),
            ),
            self.assertRaises(subprocess.TimeoutExpired),
        ):
            deadline.signal_group(process, signal.SIGKILL)

    def test_interrupt_kills_a_child_that_ignores_sigterm(self):
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / "ready"
            source = (
                "import os, signal, time; from pathlib import Path; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                f"Path({str(ready)!r}).write_text(str(os.getpid())); time.sleep(60)"
            )
            process = subprocess.Popen(
                [sys.executable, str(HELPER), "60", sys.executable, "-c", source],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            child = None
            try:
                deadline = time.monotonic() + 5
                while not ready.exists() and time.monotonic() < deadline:
                    time.sleep(0.01)
                self.assertTrue(ready.exists())
                child = int(ready.read_text())
                process.send_signal(signal.SIGTERM)
                process.communicate(timeout=5)
                self.assertEqual(process.returncode, 128 + signal.SIGTERM)
                with self.assertRaises(ProcessLookupError):
                    os.kill(child, 0)
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate(timeout=5)
                if child is not None:
                    try:
                        os.kill(child, signal.SIGKILL)
                    except ProcessLookupError:
                        pass


if __name__ == "__main__":
    unittest.main()
