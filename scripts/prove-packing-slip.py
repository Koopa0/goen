#!/usr/bin/env python3
"""Exercise packing-slip field and privacy oracles against production mutations in CI."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "internal/ui/pages/packingslip.go"
TEST_SOURCE = ROOT / "internal/ui/pages/packingslip_test.go"
GENERATED = ROOT / "internal/ui/pages/packingslip_templ.go"
EVIDENCE = ROOT / "packing-slip-proof"
PACKAGE = "github.com/koopa0/goen/internal/ui/pages"
TEST = "TestPackingSlipContainsOnlyFulfilmentData"
CASES = {TEST + "/" + locale + "/" + destination for locale in ("en", "zh-Hant") for destination in ("home", "pickup")}
REQUIRED = CASES | {TEST}
COMMAND = ["go", "test", "-json", "-count=1", "-timeout=3m", "./internal/ui/pages", "-run", "^" + TEST + "$"]
MUTANTS = [
    ("recipient-leaks-email", b"Recipient: v.Recipient", b"Recipient: v.Email", 'packing slip leaks "private@example.com"'),
    ("quantity-leaks-price", b"Quantity: line.QuantityText()", b"Quantity: line.UnitPrice()", 'packing slip leaks "123,456"'),
    ("sku-disappears", b"SKU: line.SKU", b'SKU: ""', 'packing slip missing "SKU-143"'),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run_case(label, cases, assertion=None):
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (EVIDENCE / (label + ".stderr")).open("wb") as err:
        result = subprocess.run(COMMAND, cwd=ROOT, stdout=out, stderr=err, timeout=240, check=False)
    events = [json.loads(line) for line in (EVIDENCE / (label + ".jsonl")).read_text().splitlines()]
    outcomes = {
        action: {e["Test"] for e in events if e.get("Package") == PACKAGE and e.get("Action") == action and "Test" in e}
        for action in ("pass", "fail", "skip")
    }
    matched = {
        name: assertion in "".join(e.get("Output", "") for e in events if e.get("Package") == PACKAGE and e.get("Test") == name)
        for name in CASES
    } if assertion else {}
    package_action = "fail" if assertion else "pass"
    package_result = any(e.get("Package") == PACKAGE and "Test" not in e and e.get("Action") == package_action for e in events)
    valid = (
        result.returncode == (1 if assertion else 0)
        and outcomes["fail"] == (REQUIRED if assertion else set())
        and outcomes["pass"] == (set() if assertion else REQUIRED)
        and not outcomes["skip"]
        and package_result
        and all(matched.values())
    )
    cases.append({
        "label": label,
        "source_sha256": sha(SOURCE.read_bytes()),
        "test_sha256": sha(TEST_SOURCE.read_bytes()),
        "generated_sha256": sha(GENERATED.read_bytes()),
        "exit_code": result.returncode,
        "outcomes": {key: sorted(value) for key, value in outcomes.items()},
        "expected_assertion": assertion,
        "matched_assertion": matched,
        "valid": valid,
    })
    print(json.dumps(cases[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": named four-case behavioral red or green is missing")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    original = SOURCE.read_bytes()
    generated = GENERATED.read_bytes()
    tests = TEST_SOURCE.read_bytes()
    inputs = {
        str(path.relative_to(ROOT)): path.read_bytes()
        for path in sorted(set((ROOT / "internal").rglob("*.go")) | set((ROOT / "internal/ui").rglob("*.templ")) | {ROOT / "go.mod", ROOT / "go.sum"})
        if path != SOURCE
    }
    cases = []
    report = {
        "checkout_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "github_sha": os.environ.get("GITHUB_SHA"),
        "command": COMMAND,
        "original_sha256": sha(original),
        "generated_sha256": sha(generated),
        "test_sha256": sha(tests),
        "input_sha256": {path: sha(data) for path, data in inputs.items()},
        "cases": cases,
        "errors": [],
    }
    (EVIDENCE / "original.go").write_bytes(original)
    try:
        run_case("baseline", cases)
        for label, before, after, assertion in MUTANTS:
            if original.count(before) != 1 or after in original:
                raise RuntimeError(label + ": production mutation target is not unique")
            SOURCE.write_bytes(original.replace(before, after, 1))
            (EVIDENCE / (label + ".go")).write_bytes(SOURCE.read_bytes())
            try:
                run_case(label, cases, assertion)
            finally:
                SOURCE.write_bytes(original)
                if SOURCE.read_bytes() != original:
                    raise RuntimeError(label + ": source restoration differs from original bytes")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        SOURCE.write_bytes(original)
        try:
            run_case("restored", cases)
        except Exception as error:
            report["errors"].append(str(error))
        report["source_restored_byte_exact"] = SOURCE.read_bytes() == original
        report["generated_unchanged_byte_exact"] = GENERATED.read_bytes() == generated
        report["test_unchanged_byte_exact"] = TEST_SOURCE.read_bytes() == tests
        report["inputs_byte_exact"] = all((ROOT / path).read_bytes() == data for path, data in inputs.items())
        report["success"] = (
            not report["errors"]
            and all(report[key] for key in ("source_restored_byte_exact", "generated_unchanged_byte_exact", "test_unchanged_byte_exact", "inputs_byte_exact"))
            and len(cases) == 5
            and all(case["valid"] for case in cases)
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(sha(path.read_bytes()) + "  " + path.name for path in sorted(EVIDENCE.iterdir()) if path.is_file()) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect packing-slip-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
