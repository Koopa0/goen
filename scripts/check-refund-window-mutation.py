#!/usr/bin/env python3
"""Prove the refund-only report regression in CI and restore its source."""

import json
import os
from pathlib import Path
import signal
import subprocess


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "internal/ui/pages/adminreport.go"
TEST = "TestRefundOnlyReportWindow"
MARKER = "refund-only window hid settled money"


def records(path):
    events = []
    for line in path.read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict):
            events.append(event)
    return events


def run_case(directory, name):
    path = directory / (name + ".jsonl")
    with path.open("w") as output:
        result = subprocess.run(
            ["go", "test", "-json", "-tags=integration", "-race", "-count=1",
             "-timeout=5m", "./internal/admin", "-run", "^" + TEST + "$"],
            cwd=ROOT, stdout=output, stderr=subprocess.STDOUT, check=False,
        )
    return result.returncode, records(path)


def require_green(directory, name):
    code, events = run_case(directory, name)
    actions = {e.get("Action") for e in events if e.get("Test") == TEST}
    if code != 0 or not {"run", "pass"}.issubset(actions):
        raise RuntimeError(name + " did not execute and pass " + TEST)


def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise SystemExit("This proof runs only in GitHub CI.")
    directory = Path(os.environ["RUNNER_TEMP"]) / "goen-refund-window-mutation"
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "checkout-sha.txt").write_bytes(subprocess.check_output(
        ["git", "rev-parse", "HEAD"], cwd=ROOT,
    ))
    (directory / "pr-head.txt").write_text(os.environ.get("GOEN_PROOF_HEAD", "unknown") + "\n")
    original = SOURCE.read_bytes()
    before = b"return v.Placed == 0 && v.RefundedCents == 0"
    if original.count(before) != 1:
        raise RuntimeError("refund window mutation target is not unique")
    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    try:
        require_green(directory, "baseline")
        SOURCE.write_bytes(original.replace(before, b"return v.Placed == 0", 1))
        (directory / "empty-predicate.patch").write_bytes(subprocess.check_output(
            ["git", "diff", "--", str(SOURCE)], cwd=ROOT,
        ))
        code, events = run_case(directory, "empty-predicate")
        actions = {e.get("Action") for e in events if e.get("Test") == TEST}
        assertions = [e.get("Output", "").strip() for e in events
                      if e.get("Test") == TEST and MARKER in e.get("Output", "")]
        if code != 1 or not {"run", "fail"}.issubset(actions) or not assertions:
            raise RuntimeError("mutant did not reach the required runtime assertion")
        print("Observed runtime red: " + assertions[0], flush=True)
    finally:
        SOURCE.write_bytes(original)
        if SOURCE.read_bytes() != original:
            raise RuntimeError("production source was not restored byte for byte")
    require_green(directory, "restored")
    print("Production source restored; named regression passed.", flush=True)


if __name__ == "__main__":
    main()
