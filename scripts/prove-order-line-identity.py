#!/usr/bin/env python3
"""Collect order-line identity mutation evidence only on GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "migrations/001_initial_schema.up.sql"
EVIDENCE = ROOT / "order-line-identity-proof"
PACKAGE = "github.com/koopa0/goen/internal/db"
SNAPSHOT = "TestOrderLineIdentityPreservesLocalizedPurchaseSnapshots"
SKU = "TestRulesReject/order_lines_sku_matches_variant"
REQUIRED = {
    SNAPSHOT,
    SKU,
    "TestRulesReject/order_lines_name_matches_product",
    "TestRulesAccept/order_lines_sku_matches_variant",
    "TestRulesAccept/order_lines_name_matches_product",
}
COMMAND = [
    "go", "test", "-json", "-tags=integration", "-count=1", "-timeout=4m",
    "./internal/db", "-run",
    "^(TestRulesReject|TestRulesAccept|" + SNAPSHOT + ")$/"
    "^(order_lines_sku_matches_variant|order_lines_name_matches_product)$",
]
MUTANTS = [
    (
        "sku-guard-bypass",
        b"IF NEW.sku IS DISTINCT FROM v_sku THEN",
        b"IF FALSE THEN",
        SKU,
        "the database accepted it; order_lines_sku_matches_variant does not enforce this",
    ),
    (
        "localized-name-refused",
        b"IF NEW.product_name IS DISTINCT FROM v_name\n"
        b"               AND NEW.product_name IS DISTINCT FROM v_name_en THEN",
        b"IF NEW.product_name IS DISTINCT FROM v_name THEN",
        SNAPSHOT,
        "localized checkout snapshot: ERROR: order line name must identify its product (SQLSTATE 23514)",
    ),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run_case(label, records, expected_test=None, assertion=None):
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (
        EVIDENCE / (label + ".stderr")
    ).open("wb") as err:
        result = subprocess.run(
            COMMAND, cwd=ROOT, stdout=out, stderr=err, timeout=300, check=False
        )
    events = [
        json.loads(line)
        for line in (EVIDENCE / (label + ".jsonl")).read_text().splitlines()
    ]
    passed = {
        event["Test"] for event in events
        if event.get("Package") == PACKAGE and event.get("Action") == "pass"
        and "Test" in event
    }
    failed = {
        event["Test"] for event in events
        if event.get("Package") == PACKAGE and event.get("Action") == "fail"
        and "Test" in event
    }
    expected_failures = set()
    if expected_test:
        expected_failures.add(expected_test)
        if "/" in expected_test:
            expected_failures.add(expected_test.split("/", 1)[0])
    output = "".join(
        event.get("Output", "") for event in events
        if event.get("Package") == PACKAGE and event.get("Test") == expected_test
    )
    package_result = any(
        event.get("Package") == PACKAGE and "Test" not in event
        and event.get("Action") == ("fail" if expected_test else "pass")
        for event in events
    )
    valid = (
        result.returncode == (1 if expected_test else 0)
        and failed == expected_failures
        and REQUIRED - expected_failures <= passed
        and package_result
        and (assertion is None or assertion in output)
    )
    records.append({
        "label": label,
        "source_sha256": sha(SOURCE.read_bytes()),
        "exit_code": result.returncode,
        "failed_tests": sorted(failed),
        "passed_tests": sorted(passed),
        "expected_assertion": assertion,
        "matched_assertion": assertion if assertion and assertion in output else None,
        "valid": valid,
    })
    print(json.dumps(records[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": required named test/assertion evidence missing")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    original = SOURCE.read_bytes()
    (EVIDENCE / "original.sql").write_bytes(original)
    # Pin the oracle and migration loader as well as production input: each Go
    # process uses dbtest.Start to read these bytes into a new PostgreSQL.
    inputs = {
        str(path.relative_to(ROOT)): path.read_bytes()
        for path in sorted((ROOT / "internal/db").rglob("*.go"))
    }
    records = []
    report = {
        "checkout_commit": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip(),
        "github_sha": os.environ.get("GITHUB_SHA"),
        "command": COMMAND,
        "original_sha256": sha(original),
        "go_input_sha256": {path: sha(data) for path, data in inputs.items()},
        "cases": records,
        "errors": [],
    }
    try:
        run_case("baseline", records)
        for label, before, after, test, assertion in MUTANTS:
            if original.count(before) != 1:
                raise RuntimeError(label + ": production target must match exactly once")
            mutated = original.replace(before, after, 1)
            SOURCE.write_bytes(mutated)
            (EVIDENCE / (label + ".sql")).write_bytes(mutated)
            try:
                run_case(label, records, test, assertion)
            finally:
                SOURCE.write_bytes(original)
                if SOURCE.read_bytes() != original:
                    raise RuntimeError(label + ": byte-exact restoration failed")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        SOURCE.write_bytes(original)
        report["restored_byte_exact"] = SOURCE.read_bytes() == original
        report["restored_sha256"] = sha(SOURCE.read_bytes())
        report["go_inputs_byte_exact"] = all(
            (ROOT / path).read_bytes() == data for path, data in inputs.items()
        )
        try:
            run_case("restored", records)
        except Exception as error:
            report["errors"].append(str(error))
        report["success"] = (
            not report["errors"] and report["restored_byte_exact"]
            and report["go_inputs_byte_exact"] and len(records) == 4
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        hashes = [
            sha(path.read_bytes()) + "  " + path.name
            for path in sorted(EVIDENCE.iterdir()) if path.is_file()
        ]
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect order-line-identity-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
