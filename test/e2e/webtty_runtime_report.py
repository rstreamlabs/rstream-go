"""Validate the exact case manifest of a partial WebTTY direct runtime profile."""

import argparse
from collections import Counter
import json
from pathlib import Path
import re
import sys
import xml.etree.ElementTree as ET


def sanitizer_findings(output):
    pattern = re.compile(
        r"(?:ERROR|WARNING|SUMMARY): (?:Address|Leak|Thread|Memory|UndefinedBehavior)Sanitizer|runtime error:"
    )
    findings = []
    for path in sorted(output.rglob("*.log")):
        with path.open(errors="replace") as log:
            for number, line in enumerate(log, 1):
                if pattern.search(line):
                    findings.append(
                        {"file": str(path.relative_to(output)), "line": number}
                    )
                    break
    return findings


def make_report(manifest, profile, cells, completed, status, findings=()):
    expected = manifest["profiles"].get(profile, [])
    counts = Counter(cell["id"] for cell in cells)
    errors = []
    if not expected or len(expected) != len(set(expected)):
        errors.append("Missing, empty or duplicate expected profile manifest")
    if not completed or status != 0:
        errors.append("Runtime did not complete successfully")
    if set(expected) != set(counts):
        errors.append("Expected and executed case IDs differ")
    if any(count != 1 for count in counts.values()):
        errors.append("A runtime case was executed more than once")
    if any(cell["status"] != "PASS" for cell in cells):
        errors.append("Runtime cases failed or were skipped")
    if findings:
        errors.append("Sanitizers reported runtime errors in client or daemon logs")
    return {
        "profile": profile,
        "complete_certification": False,
        "completed": completed,
        "passed": not errors,
        "expected": expected,
        "missing": sorted(set(expected) - set(counts)),
        "unexpected": sorted(set(counts) - set(expected)),
        "errors": errors,
        "cells": cells,
        "sanitizer_findings": list(findings),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("profile")
    parser.add_argument("completed", choices=("0", "1"))
    parser.add_argument("status", type=int)
    args = parser.parse_args()
    manifest = json.loads(
        Path(__file__).with_name("webtty-runtime-manifest.json").read_text()
    )
    cases = args.output / "cases.tsv"
    cells = (
        [
            dict(zip(("status", "id"), line.split("\t", 1)))
            for line in cases.read_text().splitlines()
        ]
        if cases.exists()
        else []
    )
    report = make_report(
        manifest,
        args.profile,
        cells,
        args.completed == "1",
        args.status,
        sanitizer_findings(args.output),
    )
    (args.output / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    suite = ET.Element("testsuite", name=args.profile, tests=str(len(cells) + 1))
    for cell in cells:
        case = ET.SubElement(suite, "testcase", name=cell["id"])
        if cell["status"] != "PASS":
            ET.SubElement(case, "failure", message=cell["status"])
    gate = ET.SubElement(suite, "testcase", name="complete-profile-manifest")
    if report["errors"]:
        ET.SubElement(gate, "failure", message="; ".join(report["errors"]))
    ET.ElementTree(suite).write(
        args.output / "junit.xml", encoding="utf-8", xml_declaration=True
    )
    if not report["passed"]:
        print(
            "WebTTY direct profile failed: " + "; ".join(report["errors"]),
            file=sys.stderr,
        )
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
