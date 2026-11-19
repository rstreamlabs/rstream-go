"""Run a runtime-test command with inherited IO and bounded process-group cleanup."""

import argparse
import math
import os
import signal
import subprocess
import sys
import time


def signal_group(process, number):
    try:
        os.killpg(process.pid, number)
    except ProcessLookupError:
        pass
    except PermissionError:
        # Darwin may deny signaling an orphaned group after its last member
        # exits. Verify the group has no live members; never hide a real denial.
        if sys.platform != "darwin" or group_has_live_members(process.pid):
            raise


def group_has_live_members(group_id):
    snapshot = subprocess.run(
        ["ps", "-axo", "pgid=,stat="],
        capture_output=True,
        text=True,
        check=True,
        timeout=1,
    )
    for line in snapshot.stdout.splitlines():
        fields = line.split()
        if len(fields) != 2:
            raise ValueError("cannot verify process-group cleanup")
        if fields[0] == str(group_id) and not fields[1].startswith("Z"):
            return True
    return False


def stop_group(process):
    signal_group(process, signal.SIGTERM)
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        pass
    # A child may have exited while a grandchild still holds inherited IO open.
    signal_group(process, signal.SIGKILL)
    process.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("seconds", type=float)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if not math.isfinite(args.seconds) or args.seconds <= 0:
        parser.error("seconds must be finite and positive")
    if not args.command:
        parser.error("a command is required")
    interrupted = 0

    def interrupt(number, _frame):
        nonlocal interrupted
        interrupted = number

    for number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(number, interrupt)
    process = subprocess.Popen(args.command, start_new_session=True)
    deadline = time.monotonic() + args.seconds
    try:
        while True:
            if interrupted:
                return 128 + interrupted
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                print(
                    f"Runtime command exceeded its {args.seconds:g} second deadline",
                    file=sys.stderr,
                )
                return 124
            try:
                code = process.wait(timeout=min(remaining, 0.2))
                return code if code >= 0 else 128 - code
            except subprocess.TimeoutExpired:
                continue
    finally:
        stop_group(process)


if __name__ == "__main__":
    sys.exit(main())
