#!/usr/bin/env python3
"""Collect invoice-cancellation mutation evidence only on GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "migrations/001_initial_schema.up.sql"
EVIDENCE = ROOT / "invoice-cancellation-proof"
PACKAGE = "github.com/koopa0/goen/internal/invoice"
OPERATIONS = "TestUnresolvedInvoiceOperationsBlockCancellationEvenAfterFullAllowance"
AMOUNTS = "TestCancellationRequiresResolvedInvoiceAmounts"
OPERATION_TESTS = {OPERATIONS} | {
    OPERATIONS + "/" + kind + "/" + status
    for kind in ("issue", "void", "allowance")
    for status in ("pending", "attention", "rejected")
}
AMOUNT_CASES = (
    "live_invoice", "partial_allowance", "full_allowance",
    "voided_allowance", "voided_invoice",
)
AMOUNT_TESTS = {AMOUNTS} | {
    AMOUNTS + "/" + role + "/" + case
    for role in ("store", "admin") for case in AMOUNT_CASES
}
OPERATION_TARGET = (
    b"    IF EXISTS (SELECT 1 FROM invoice_operations\n"
    b"               WHERE order_id = NEW.id AND status IN ('pending', 'attention'))"
)
AMOUNT_TARGET = (
    b"           WHERE d.order_id = NEW.id AND d.kind = 'invoice' AND d.status = 'issued'\n"
    b"             AND d.amount_cents > coalesce(("
)
MUTANTS = [
    (
        "unresolved-operation-guard-bypass",
        OPERATION_TARGET,
        OPERATION_TARGET.replace(b"IN ('pending', 'attention'))", b"IN ('pending', 'attention') AND false)"),
        OPERATIONS,
        OPERATION_TESTS,
        {
            OPERATIONS + "/" + kind + "/" + status:
                "unresolved " + kind + " " + status + " cancelled: <nil>"
            for kind in ("issue", "void", "allowance")
            for status in ("pending", "attention")
        },
    ),
    (
        "unrelieved-invoice-guard-bypass",
        AMOUNT_TARGET,
        AMOUNT_TARGET.replace(b"AND d.amount_cents >", b"AND false AND d.amount_cents >"),
        AMOUNTS,
        AMOUNT_TESTS,
        {
            AMOUNTS + "/" + role + "/" + case: "unresolved cancellation: <nil>"
            for role in ("store", "admin")
            for case in ("live_invoice", "partial_allowance", "voided_allowance")
        },
    ),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def command(parents):
    return [
        "go", "test", "-json", "-tags=integration", "-count=1", "-timeout=4m",
        "./internal/invoice", "-run", "^(" + "|".join(sorted(parents)) + ")$",
    ]


def run_case(label, records, parents, required, assertions=None):
    assertions = assertions or {}
    cmd = command(parents)
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (
        EVIDENCE / (label + ".stderr")
    ).open("wb") as err:
        result = subprocess.run(
            cmd, cwd=ROOT, stdout=out, stderr=err, timeout=300, check=False
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
    skipped = {
        event["Test"] for event in events
        if event.get("Package") == PACKAGE and event.get("Action") == "skip"
        and "Test" in event
    }
    expected_failures = set(assertions) | {
        test.split("/", 1)[0] for test in assertions
    }
    matched = {}
    for test, assertion in assertions.items():
        output = "".join(
            event.get("Output", "") for event in events
            if event.get("Package") == PACKAGE and event.get("Test") == test
        )
        match = re.search(
            r"(?m)^\s+cancellation_integration_test\.go:\d+: "
            + re.escape(assertion) + r"\s*$", output
        )
        if match:
            matched[test] = match.group(0).strip()
    package_result = any(
        event.get("Package") == PACKAGE and "Test" not in event
        and event.get("Action") == ("fail" if assertions else "pass")
        for event in events
    )
    # Only the named behavioral failures count. Build, migration, permission,
    # fixture, and unexpected sibling failures cannot satisfy this proof.
    valid = (
        result.returncode == (1 if assertions else 0)
        and failed == expected_failures
        and passed == required - expected_failures
        and not skipped
        and package_result
        and set(matched) == set(assertions)
    )
    records.append({
        "label": label,
        "command": cmd,
        "source_sha256": sha(SOURCE.read_bytes()),
        "exit_code": result.returncode,
        "failed_tests": sorted(failed),
        "passed_tests": sorted(passed),
        "skipped_tests": sorted(skipped),
        "expected_assertions": assertions,
        "matched_assertions": matched,
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
    # Each go test process calls dbtest.Start, which reads these migration bytes
    # into a fresh PostgreSQL. Pin the oracles, loader, and Go production inputs
    # so changing an assertion cannot masquerade as observing a SQL mutation.
    input_paths = set((ROOT / "internal").rglob("*.go"))
    input_paths.update((ROOT / "migrations").glob("*.sql"))
    input_paths.discard(SOURCE)
    input_paths.update({ROOT / "go.mod", ROOT / "go.sum", ROOT / "seed/dev_catalog.sql"})
    inputs = {
        str(path.relative_to(ROOT)): path.read_bytes()
        for path in sorted(input_paths)
    }
    parents = {OPERATIONS, AMOUNTS}
    required = OPERATION_TESTS | AMOUNT_TESTS
    records = []
    report = {
        "checkout_commit": None,
        "github_sha": os.environ.get("GITHUB_SHA"),
        "proof_script_sha256": sha(Path(__file__).read_bytes()),
        "baseline_command": command(parents),
        "original_sha256": sha(original),
        "input_sha256": {path: sha(data) for path, data in inputs.items()},
        "oracle_sha256": {
            path: sha(data) for path, data in inputs.items()
            if path.startswith("internal/invoice/") and path.endswith("_test.go")
        },
        "cases": records,
        "errors": [],
    }
    try:
        report["checkout_commit"] = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip()
        run_case("baseline", records, parents, required)
        for label, before, after, parent, tests, assertions in MUTANTS:
            if original.count(before) != 1:
                raise RuntimeError(label + ": production target must match exactly once")
            mutated = original.replace(before, after, 1)
            if mutated == original:
                raise RuntimeError(label + ": production bytes did not change")
            SOURCE.write_bytes(mutated)
            (EVIDENCE / (label + ".sql")).write_bytes(mutated)
            try:
                run_case(label, records, {parent}, tests, assertions)
            finally:
                SOURCE.write_bytes(original)
                if SOURCE.read_bytes() != original:
                    raise RuntimeError(label + ": byte-exact restoration failed")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        SOURCE.write_bytes(original)
        try:
            run_case("restored", records, parents, required)
        except Exception as error:
            report["errors"].append(str(error))
        report["restored_byte_exact"] = SOURCE.read_bytes() == original
        report["restored_sha256"] = sha(SOURCE.read_bytes())
        report["inputs_byte_exact"] = all(
            (ROOT / path).is_file() and (ROOT / path).read_bytes() == data
            for path, data in inputs.items()
        )
        report["success"] = (
            not report["errors"] and report["restored_byte_exact"]
            and report["inputs_byte_exact"] and len(records) == 4
            and all(record["valid"] for record in records)
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        hashes = [
            sha(path.read_bytes()) + "  " + path.name
            for path in sorted(EVIDENCE.iterdir()) if path.is_file()
        ]
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect invoice-cancellation-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
