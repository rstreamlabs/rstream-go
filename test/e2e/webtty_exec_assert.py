"""Assert actual command results, excluding echoed arguments and diagnostics."""

import argparse
import json
import sys


def verify(result, expected):
    if not isinstance(result, dict):
        raise ValueError("WebTTY exec must return a result object")
    if result.get("exit_code") != 0:
        raise ValueError("WebTTY command did not exit successfully")
    if result.get("stdout") != expected or result.get("stderr") != "":
        raise ValueError(
            "WebTTY command stdout/stderr does not match the expected result"
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("expected")
    parser.add_argument("--newline", action="store_true")
    args = parser.parse_args()
    raw = sys.stdin.read(65537)
    if len(raw) > 65536:
        raise ValueError("Unexpectedly large WebTTY test result")
    verify(json.loads(raw), args.expected + ("\n" if args.newline else ""))


if __name__ == "__main__":
    main()
