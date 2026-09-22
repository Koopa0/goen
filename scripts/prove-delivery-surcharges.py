#!/usr/bin/env python3
"""Collect named delivery-surcharge mutation evidence only in GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "internal/admin/delivery.go"
EVIDENCE = ROOT / "delivery-surcharge-proof"
PACKAGE = "github.com/koopa0/goen/internal/admin"
DIRECTIONS = "TestDeliveryCorrectionRefusesBothSurchargeDirections"
FROZEN = "TestSameSurchargeCorrectionRetainsFrozenPriceAfterMethodRetires"
WAITER = "TestDeliveryCorrectionReadsPostalAfterWaitingForPriorCorrection"
REQUIRED = {DIRECTIONS, DIRECTIONS + "/increase", DIRECTIONS + "/decrease", FROZEN, WAITER}
COMMAND = [
    "go", "test", "-tags=integration", "-race", "-json", "-count=1", "-timeout=4m",
    "./internal/admin", "-run", "^(" + "|".join([DIRECTIONS, FROZEN, WAITER]) + ")$",
]
MUTANTS = [
    {
        "label": "positive-delta-accepted",
        "before": b"if delta := comparison.NewSurcharge - comparison.OldSurcharge; delta != 0 {",
        "after": b"if delta := comparison.NewSurcharge - comparison.OldSurcharge; delta < 0 {",
        "fail": {DIRECTIONS, DIRECTIONS + "/increase"},
        "assertions": {DIRECTIONS + "/increase": "correction error=<nil> change=admin: delivery surcharge changes"},
    },
    {
        "label": "negative-delta-accepted",
        "before": b"if delta := comparison.NewSurcharge - comparison.OldSurcharge; delta != 0 {",
        "after": b"if delta := comparison.NewSurcharge - comparison.OldSurcharge; delta > 0 {",
        "fail": {DIRECTIONS, DIRECTIONS + "/decrease"},
        "assertions": {DIRECTIONS + "/decrease": "correction error=<nil> change=admin: delivery surcharge changes"},
    },
    {
        "label": "frozen-charge-substituted-for-current-quote",
        "before": b"comparison.NewSurcharge - comparison.OldSurcharge",
        "after": b"comparison.NewSurcharge - order.ShippingCents",
        "fail": {FROZEN, WAITER},
        "assertions": {
            FROZEN: "admin: delivery surcharge changes",
            WAITER: "did not read committed predecessor address: admin: delivery surcharge changes",
        },
    },
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def unchanged(inputs):
    return all((ROOT / name).is_file() and (ROOT / name).read_bytes() == data for name, data in inputs.items())


def run_case(label, cases, inputs, failed=None, assertions=None):
    expected_fail = failed or set()
    expected_assertions = assertions or {}
    if not unchanged(inputs):
        raise RuntimeError(label + ": an oracle, fixture or other production input changed")
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (EVIDENCE / (label + ".stderr")).open("wb") as err:
        result = subprocess.run(COMMAND, cwd=ROOT, stdout=out, stderr=err, timeout=360, check=False)
    events = [json.loads(line) for line in (EVIDENCE / (label + ".jsonl")).read_text().splitlines()]
    outcomes = {
        action: {e["Test"] for e in events if e.get("Package") == PACKAGE and e.get("Action") == action and "Test" in e}
        for action in ["pass", "fail", "skip"]
    }
    matches = {}
    for test, assertion in expected_assertions.items():
        output = "".join(e.get("Output", "") for e in events if e.get("Package") == PACKAGE and e.get("Test") == test)
        matches[test] = assertion if assertion in output else None
    package_action = "fail" if expected_fail else "pass"
    package_result = any(e.get("Package") == PACKAGE and "Test" not in e and e.get("Action") == package_action for e in events)
    valid = (
        result.returncode == (1 if expected_fail else 0)
        and outcomes["fail"] == expected_fail
        and outcomes["pass"] == REQUIRED - expected_fail
        and not outcomes["skip"]
        and package_result
        and all(matches.values())
        and unchanged(inputs)
    )
    cases.append({
        "label": label, "source_sha256": sha(SOURCE.read_bytes()),
        "exit_code": result.returncode, "outcomes": {k: sorted(v) for k, v in outcomes.items()},
        "expected_failed_tests": sorted(expected_fail), "expected_assertions": expected_assertions,
        "matched_assertions": matches, "valid": valid,
    })
    print(json.dumps(cases[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": named behavioral evidence is incomplete; build/setup failures are not red tests")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    original = SOURCE.read_bytes()
    paths = set()
    for directory in ["internal", "migrations", "seed"]:
        paths.update(p for p in (ROOT / directory).rglob("*") if p.is_file())
    paths.update([ROOT / "go.mod", ROOT / "go.sum"])
    paths.remove(SOURCE)
    inputs = {str(p.relative_to(ROOT)): p.read_bytes() for p in sorted(paths)}
    cases = []
    report = {
        "checkout_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "github_sha": os.environ.get("GITHUB_SHA"), "command": COMMAND,
        "scope": "Both signed refusal directions and current surcharge versus frozen shipping, including a synchronized waiting correction. This does not independently mutate lock placement or prove every concurrency invariant.",
        "original_source_sha256": sha(original),
        "input_sha256": {name: sha(data) for name, data in inputs.items()},
        "cases": cases, "restorations": [], "errors": [],
    }
    (EVIDENCE / "original-delivery.go").write_bytes(original)
    try:
        run_case("baseline", cases, inputs)
        for mutant in MUTANTS:
            label = mutant["label"]
            if original.count(mutant["before"]) != 1:
                raise RuntimeError(label + ": production mutation target is not unique")
            mutated = original.replace(mutant["before"], mutant["after"], 1)
            SOURCE.write_bytes(mutated)
            try:
                if SOURCE.read_bytes() == original or mutant["after"] not in SOURCE.read_bytes():
                    raise RuntimeError(label + ": production mutation was not installed")
                (EVIDENCE / (label + ".go")).write_bytes(mutated)
                run_case(label, cases, inputs, mutant["fail"], mutant["assertions"])
            finally:
                SOURCE.write_bytes(original)
                restored = SOURCE.read_bytes() == original
                report["restorations"].append({"after": label, "byte_exact": restored, "sha256": sha(SOURCE.read_bytes())})
                if not restored:
                    raise RuntimeError(label + ": source restoration differs from original bytes")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        SOURCE.write_bytes(original)
        try:
            run_case("restored", cases, inputs)
        except Exception as error:
            report["errors"].append(str(error))
        report["source_restored_byte_exact"] = SOURCE.read_bytes() == original
        report["inputs_byte_exact"] = unchanged(inputs)
        report["success"] = (
            not report["errors"] and report["source_restored_byte_exact"] and report["inputs_byte_exact"]
            and [c["label"] for c in cases] == ["baseline"] + [m["label"] for m in MUTANTS] + ["restored"]
            and len(report["restorations"]) == len(MUTANTS)
            and all(r["byte_exact"] for r in report["restorations"])
            and all(c["valid"] for c in cases)
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(sha(p.read_bytes()) + "  " + p.name for p in sorted(EVIDENCE.iterdir()) if p.is_file()) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect delivery-surcharge-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
