"""A successful subset or a skipped case cannot certify the direct profile."""

import unittest
from pathlib import Path
import tempfile

import webtty_runtime_report
from webtty_runtime_report import make_report


class RuntimeReportTests(unittest.TestCase):
    def setUp(self):
        self.manifest = {"profiles": {"full-direct": ["websocket", "webtransport"]}}
        self.cells = [
            {"id": name, "status": "PASS"}
            for name in self.manifest["profiles"]["full-direct"]
        ]

    def report(self, cells=None, completed=True, status=0, profile="full-direct"):
        return make_report(
            self.manifest,
            profile,
            self.cells if cells is None else cells,
            completed,
            status,
        )

    def test_complete_profile_is_still_not_complete_certification(self):
        report = self.report()
        self.assertTrue(report["passed"])
        self.assertFalse(report["complete_certification"])

    def test_incomplete_or_unexpected_cases_fail(self):
        for cells in (
            [],
            self.cells[:1],
            self.cells + [self.cells[0]],
            self.cells + [{"id": "other", "status": "PASS"}],
        ):
            with self.subTest(cells=cells):
                self.assertFalse(self.report(cells)["passed"])

    def test_failure_and_skip_are_failures(self):
        for status in ("FAIL", "SKIP", ""):
            self.cells[1]["status"] = status
            self.assertFalse(self.report()["passed"])

    def test_early_exit_and_unknown_profile_fail(self):
        self.assertFalse(self.report(completed=False)["passed"])
        self.assertFalse(self.report(status=1)["passed"])
        self.assertFalse(self.report(profile="unknown")["passed"])

    def test_successful_cases_cannot_hide_daemon_sanitizer_findings(self):
        for diagnostic in (
            "WARNING: ThreadSanitizer: data race",
            "ERROR: AddressSanitizer: heap-use-after-free",
            "ERROR: LeakSanitizer: detected memory leaks",
            "source.cpp:29:10: runtime error: load of misaligned address",
        ):
            with self.subTest(
                diagnostic=diagnostic
            ), tempfile.TemporaryDirectory() as root:
                path = Path(root)
                (path / "case.log").write_text("PASS real command\n")
                (path / "server.log").write_text("server ready\n" + diagnostic + "\n")
                findings = webtty_runtime_report.sanitizer_findings(path)
                self.assertEqual(len(findings), 1)
                self.assertEqual(findings[0]["file"], "server.log")
                self.assertEqual(findings[0]["line"], 2)
                report = make_report(
                    self.manifest, "full-direct", self.cells, True, 0, findings
                )
                self.assertFalse(report["passed"])

    def test_clean_runtime_logs_do_not_fail(self):
        with tempfile.TemporaryDirectory() as root:
            (Path(root) / "server.log").write_text("server ready\nserver stopped\n")
            self.assertEqual(webtty_runtime_report.sanitizer_findings(Path(root)), [])


if __name__ == "__main__":
    unittest.main()
