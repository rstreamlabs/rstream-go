"""An expected marker echoed in argv is not proof of execution."""

import unittest

from webtty_exec_assert import verify


class ExecAssertionTests(unittest.TestCase):
    def test_exact_success(self):
        verify({"exit_code": 0, "stdout": "marker", "stderr": ""}, "marker")

    def test_marker_only_in_command_is_rejected(self):
        for code in (-1, 0):
            with self.assertRaises(ValueError):
                verify(
                    {
                        "command": ["printf", "marker"],
                        "exit_code": code,
                        "stdout": "",
                        "stderr": "",
                    },
                    "marker",
                )

    def test_failure_output_and_incomplete_output_are_rejected(self):
        for result in (
            {"exit_code": 1, "stdout": "marker", "stderr": ""},
            {"exit_code": 0, "stdout": "marker", "stderr": "error"},
            {"exit_code": 0, "stdout": "mark", "stderr": ""},
            {"exit_code": 0, "stdout": "markerextra", "stderr": ""},
            [],
        ):
            with self.assertRaises(ValueError):
                verify(result, "marker")


if __name__ == "__main__":
    unittest.main()
