#!/usr/bin/env python3
"""Collect inventory-source mutation evidence only on GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "migrations/001_initial_schema.up.sql"
EVIDENCE = ROOT / "inventory-source-proof"
PACKAGE = "github.com/koopa0/goen/internal/db"
PARENTS = "TestInventoryMovementSourceParents"
OTHER_TESTS = {
    "TestInventorySourceRefusalRollsBackStock",
    "TestReturnInventoryHistoryResolvesOrder",
}
CASES = [
    "manual_receipt", "order", "reservation", "return", "null_source",
    "unknown_source", "manual_with_id", "order_without_id",
    "reservation_without_id", "return_without_id", "missing_order",
    "missing_reservation", "missing_return", "order_id_is_reservation",
    "reservation_id_is_order", "return_id_is_order",
    "reservation_wrong_variant", "return_wrong_variant",
]
REQUIRED = {PARENTS} | OTHER_TESTS | {
    PARENTS + "/" + role + "/" + case
    for role in ("admin", "schema") for case in CASES
}
COMMAND = [
    "go", "test", "-json", "-tags=integration", "-count=1", "-timeout=4m",
    "./internal/db", "-run",
    "^(" + "|".join([PARENTS] + sorted(OTHER_TESTS)) + ")$",
]
KNOWN = (
    b"source_type IS NOT NULL AND source_type IN "
    b"('admin', 'order', 'reservation', 'return_request')"
)
ADMIN_PAIR = b"(source_type = 'admin' AND source_id IS NULL)"
PARENT_PAIR = (
    b"(source_type IN ('order', 'reservation', 'return_request') "
    b"AND source_id IS NOT NULL)"
)
# Vocabulary is enforced in both CHECKs. Opening only one would still reject
# an unknown source through the other, so this mutant opens the same extra
# source in both predicates while retaining all ID rules for existing sources.
MUTANTS = [
    (
        "unknown-source-accepted",
        [
            (KNOWN, KNOWN[:-1] + b", 'purchase')"),
            (ADMIN_PAIR, b"(source_type IN ('admin', 'purchase') AND source_id IS NULL)"),
        ],
        {"unknown_source": "inventory_movements_source_known"},
    ),
    (
        "explicit-null-source-accepted",
        [(KNOWN, KNOWN.replace(b"source_type IS NOT NULL AND ", b"", 1))],
        {"null_source": "inventory_movements_source_known"},
    ),
    (
        "manual-source-id-accepted",
        [(ADMIN_PAIR, b"(source_type = 'admin')")],
        {"manual_with_id": "inventory_movements_source_paired"},
    ),
    (
        "required-source-id-omitted",
        [(PARENT_PAIR, PARENT_PAIR.replace(b" AND source_id IS NOT NULL", b"", 1))],
        {
            "order_without_id": "inventory_movements_source_paired",
            "reservation_without_id": "inventory_movements_source_paired",
            "return_without_id": "inventory_movements_source_paired",
        },
    ),
    (
        "reservation-variant-ignored",
        [(
            b"PERFORM 1 FROM inventory_reservations\n"
            b"        WHERE id = NEW.source_id AND variant_id = NEW.variant_id;",
            b"PERFORM 1 FROM inventory_reservations\n"
            b"        WHERE id = NEW.source_id;",
        )],
        {"reservation_wrong_variant": "inventory_movements_source_parent"},
    ),
    (
        "return-variant-ignored",
        [(
            b"WHERE rl.return_request_id = NEW.source_id AND ol.variant_id = NEW.variant_id;",
            b"WHERE rl.return_request_id = NEW.source_id;",
        )],
        {"return_wrong_variant": "inventory_movements_source_parent"},
    ),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run_case(label, records, failures=None):
    expected = {
        PARENTS + "/" + role + "/" + case:
        "error = <nil>; want 23514/" + rule
        for case, rule in (failures or {}).items()
        for role in ("admin", "schema")
    }
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
    expected_failures = set(expected) | ({PARENTS} if expected else set())
    matched = {}
    for test, assertion in expected.items():
        output = "".join(
            event.get("Output", "") for event in events
            if event.get("Package") == PACKAGE and event.get("Test") == test
        )
        if assertion in output:
            matched[test] = assertion
    package_result = any(
        event.get("Package") == PACKAGE and "Test" not in event
        and event.get("Action") == ("fail" if expected else "pass")
        for event in events
    )
    valid = (
        result.returncode == (1 if expected else 0)
        and failed == expected_failures
        and REQUIRED - expected_failures <= passed
        and package_result and matched == expected
    )
    records.append({
        "label": label,
        "source_sha256": sha(SOURCE.read_bytes()),
        "exit_code": result.returncode,
        "failed_tests": sorted(failed),
        "passed_tests": sorted(passed),
        "expected_assertions": expected,
        "matched_assertions": matched,
        "valid": valid,
    })
    print(json.dumps(records[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": required named runtime assertion evidence missing")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    original = SOURCE.read_bytes()
    (EVIDENCE / "original.sql").write_bytes(original)
    # Each uncached Go invocation starts a fresh PostgreSQL via dbtest.Start;
    # the loader reads the mutated SQL bytes directly, with no generated layer.
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
        "github_run_id": os.environ.get("GITHUB_RUN_ID"),
        "command": COMMAND,
        "proof_script_sha256": sha(Path(__file__).read_bytes()),
        "original_sha256": sha(original),
        "go_input_sha256": {path: sha(data) for path, data in inputs.items()},
        "cases": records,
        "errors": [],
    }
    try:
        run_case("baseline", records)
        for label, replacements, failures in MUTANTS:
            mutated = original
            for before, after in replacements:
                if original.count(before) != 1:
                    raise RuntimeError(label + ": production target must match exactly once")
                mutated = mutated.replace(before, after, 1)
            if mutated == original:
                raise RuntimeError(label + ": mutation did not change production input")
            SOURCE.write_bytes(mutated)
            (EVIDENCE / (label + ".sql")).write_bytes(mutated)
            try:
                run_case(label, records, failures)
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
            and report["go_inputs_byte_exact"] and len(records) == len(MUTANTS) + 2
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        hashes = [
            sha(path.read_bytes()) + "  " + path.name
            for path in sorted(EVIDENCE.iterdir()) if path.is_file()
        ]
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect inventory-source-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
