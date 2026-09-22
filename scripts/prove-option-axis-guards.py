#!/usr/bin/env python3
"""Collect option-axis refusal and lock-order evidence only on GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
EVIDENCE = ROOT / "option-axis-proof"
PACKAGE = "github.com/koopa0/goen/internal/admin"
SCHEMA = "migrations/001_initial_schema.up.sql"
VARIANT = "internal/admin/variant.go"
REFUSAL = "TestOptionAxesMustPrecedeVariants"
SERIALIZATION = "TestOptionAxisAndVariantCreationSerialize"
RACE_TESTS = {SERIALIZATION, SERIALIZATION + "/axis-first", SERIALIZATION + "/variant-first"}
GUARD = b"    IF TG_TABLE_NAME = 'product_options' AND EXISTS (\n"
LOCK = (
    b"\t\t\tif _, err := q.LockProductCatalogue(ctx, slug); err != nil {\n"
    b"\t\t\t\treturn err\n"
    b"\t\t\t}\n"
)
SELECTION = (
    b"\t\t\tchosen, err := chosenOptionValues(ctx, q, slug, f.OptionValues)\n"
    b"\t\t\tif err != nil {\n"
    b"\t\t\t\treturn err\n"
    b"\t\t\t}\n"
)
MUTANTS = [
    (
        "existing-sku-guard-bypass", SCHEMA,
        GUARD, GUARD.replace(b"IF TG_TABLE_NAME", b"IF false AND TG_TABLE_NAME"),
        REFUSAL, {REFUSAL},
        {REFUSAL: r"zh-Hant response = 303:[^\n]*"},
    ),
    (
        "reparent-guard-bypass", SCHEMA,
        GUARD, GUARD.replace(b"AND EXISTS", b"AND TG_OP = 'INSERT' AND EXISTS"),
        REFUSAL, {REFUSAL},
        {REFUSAL: re.escape("reparent bypassed axis guard: <nil>")},
    ),
    (
        "selection-before-product-lock", VARIANT,
        LOCK + SELECTION, SELECTION + LOCK.replace(b"err", b"lockErr"),
        SERIALIZATION, RACE_TESTS,
        {SERIALIZATION + "/axis-first": re.escape("concurrent write = <nil> map[], want options")},
    ),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def source_hashes():
    return {path: sha((ROOT / path).read_bytes()) for path in (SCHEMA, VARIANT)}


def command(parents):
    return [
        "go", "test", "-json", "-tags=integration", "-count=1", "-timeout=4m",
        "./internal/admin", "-run", "^(" + "|".join(sorted(parents)) + ")$",
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
            r"(?m)^\s+option_axis_integration_test\.go:\d+: "
            + assertion + r"\s*$", output
        )
        if match:
            matched[test] = match.group(0).strip()
    package_result = any(
        event.get("Package") == PACKAGE and "Test" not in event
        and event.get("Action") == ("fail" if assertions else "pass")
        for event in events
    )
    # The race oracle observes pg_blocking_pids before releasing the first
    # writer. A timeout, build/migration failure, or other assertion is not red.
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
        "source_sha256": source_hashes(),
        "exit_code": result.returncode,
        "failed_tests": sorted(failed),
        "passed_tests": sorted(passed),
        "skipped_tests": sorted(skipped),
        "expected_assertion_regex": assertions,
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
    originals = {path: (ROOT / path).read_bytes() for path in (SCHEMA, VARIANT)}
    for path, data in originals.items():
        (EVIDENCE / ("original-" + Path(path).name)).write_bytes(data)
    # dbtest.Start reads SQL into a fresh PostgreSQL in each Go process; the Go
    # ordering mutant is compiled into AddVariant itself. Generated queries and
    # the blocking/assertion oracles are pinned and never mutated.
    input_paths = set((ROOT / "internal").rglob("*.go"))
    input_paths.update((ROOT / "migrations").glob("*.sql"))
    input_paths.difference_update(ROOT / path for path in originals)
    input_paths.update({ROOT / "go.mod", ROOT / "go.sum", ROOT / "seed/dev_catalog.sql"})
    inputs = {
        str(path.relative_to(ROOT)): path.read_bytes()
        for path in sorted(input_paths)
    }
    parents = {REFUSAL, SERIALIZATION}
    required = {REFUSAL} | RACE_TESTS
    records = []
    report = {
        "checkout_commit": None,
        "github_sha": os.environ.get("GITHUB_SHA"),
        "proof_script_sha256": sha(Path(__file__).read_bytes()),
        "baseline_command": command(parents),
        "original_sha256": {path: sha(data) for path, data in originals.items()},
        "input_sha256": {path: sha(data) for path, data in inputs.items()},
        "oracle_sha256": {
            path: sha(data) for path, data in inputs.items()
            if path.startswith("internal/admin/") and path.endswith("_test.go")
        },
        "cases": records,
        "errors": [],
    }
    try:
        report["checkout_commit"] = subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip()
        run_case("baseline", records, parents, required)
        for label, path, before, after, parent, tests, assertions in MUTANTS:
            original = originals[path]
            if original.count(before) != 1:
                raise RuntimeError(label + ": production target must match exactly once")
            mutated = original.replace(before, after, 1)
            if mutated == original:
                raise RuntimeError(label + ": production bytes did not change")
            source = ROOT / path
            source.write_bytes(mutated)
            (EVIDENCE / (label + Path(path).suffix)).write_bytes(mutated)
            try:
                run_case(label, records, {parent}, tests, assertions)
            finally:
                source.write_bytes(original)
                if source.read_bytes() != original:
                    raise RuntimeError(label + ": byte-exact restoration failed")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        for path, data in originals.items():
            (ROOT / path).write_bytes(data)
        try:
            run_case("restored", records, parents, required)
        except Exception as error:
            report["errors"].append(str(error))
        report["restored_byte_exact"] = all(
            (ROOT / path).read_bytes() == data for path, data in originals.items()
        )
        report["restored_sha256"] = source_hashes()
        report["inputs_byte_exact"] = all(
            (ROOT / path).is_file() and (ROOT / path).read_bytes() == data
            for path, data in inputs.items()
        )
        report["success"] = (
            not report["errors"] and report["restored_byte_exact"]
            and report["inputs_byte_exact"] and len(records) == 5
            and all(record["valid"] for record in records)
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        hashes = [
            sha(path.read_bytes()) + "  " + path.name
            for path in sorted(EVIDENCE.iterdir()) if path.is_file()
        ]
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect option-axis-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
