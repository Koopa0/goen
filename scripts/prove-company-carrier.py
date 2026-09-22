#!/usr/bin/env python3
"""Collect company-carrier mutation evidence only on GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
EVIDENCE = ROOT / "company-carrier-proof"
PACKAGE = "github.com/koopa0/goen/internal/cart"
WIRE = "TestCompanyDeliverySurvivesCheckoutSnapshotAndProviderWire"
VERDICTS = "TestCompanyMobileUsesEveryCarrierVerdict"
FALLBACK = "TestCompanyUnknownPlatformChoiceKeepsTheCompanyIdentity"
REQUIRED = {
    WIRE, WIRE + "/email", WIRE + "/mobile", VERDICTS,
    VERDICTS + "/0", VERDICTS + "/1", VERDICTS + "/2", FALLBACK,
}
COMMAND = [
    "go", "test", "-json", "-tags=integration", "-count=1", "-timeout=4m",
    "./internal/cart", "-run", "^(" + "|".join([WIRE, VERDICTS, FALLBACK]) + ")$",
]
COMPANY_MOBILE = (
    b'\t\tif in.CarrierCode != "" {\n'
    b'\t\t\treq.CarrierT = CarrierMobile\n'
    b'\t\t\treq.CarrierNum = in.CarrierCode\n'
    b'\t\t}'
)
# Each wire defect affects only company mobile, retaining the company email
# control and the handler verdict/fallback controls in the same invocation.
MUTANTS = [
    (
        "company-wire-buyer-lost", "internal/invoice/issue.go", COMPANY_MOBILE,
        COMPANY_MOBILE.replace(b'\n\t\t}', b'\n\t\t\treq.CustomerName = ""\n\t\t}'),
        {WIRE + "/mobile": ["CustomerName did not retain the chosen buyer/delivery"]},
    ),
    (
        "company-wire-tax-id-lost", "internal/invoice/issue.go", COMPANY_MOBILE,
        COMPANY_MOBILE.replace(b'\n\t\t}', b'\n\t\t\treq.CustomerIdentifier = ""\n\t\t}'),
        {WIRE + "/mobile": ["CustomerIdentifier did not retain the chosen buyer/delivery"]},
    ),
    (
        "company-wire-print-enabled", "internal/invoice/issue.go", COMPANY_MOBILE,
        COMPANY_MOBILE.replace(b'\n\t\t}', b'\n\t\t\treq.Print = "1"\n\t\t}'),
        {WIRE + "/mobile": ["Print did not retain the chosen buyer/delivery"]},
    ),
    (
        "company-wire-carrier-cleared", "internal/invoice/issue.go", COMPANY_MOBILE,
        COMPANY_MOBILE.replace(b"req.CarrierT = CarrierMobile", b"req.CarrierT = CarrierMember")
        .replace(b"req.CarrierNum = in.CarrierCode", b'req.CarrierNum = ""'),
        {WIRE + "/mobile": [
            "CarrierType did not retain the chosen buyer/delivery",
            "wire carrier differs from the checkout snapshot",
        ]},
    ),
    (
        "company-mobile-predicate-excluded", "internal/cart/cart.go",
        b'return i.Type == invoicepkg.PreferenceMobile ||\n'
        b'\t\t(i.Type == invoicepkg.PreferenceCompany && i.CompanyDelivery == invoicepkg.CompanyDeliveryMobile)',
        b'return i.Type == invoicepkg.PreferenceMobile',
        {
            **{VERDICTS + "/" + status: ["company mobile bypassed the shared provider check"]
               for status in ("0", "1", "2")},
            FALLBACK: ["unknown did not ask for a choice"],
        },
    ),
    (
        "company-fallback-demoted-to-member", "internal/cart/handler.go",
        b'if r.PostFormValue("update") == "invoice_member" {\n'
        b'\t\tif inv.Type == invoicepkg.PreferenceCompany {\n'
        b'\t\t\tinv.CompanyDelivery = invoicepkg.CompanyDeliveryEmail\n'
        b'\t\t\tinv.Carrier = ""\n'
        b'\t\t} else {\n'
        b'\t\t\tinv.Type = invoicepkg.PreferenceMember\n'
        b'\t\t}\n\t}',
        b'if r.PostFormValue("update") == "invoice_member" {\n'
        b'\t\tinv.Type = invoicepkg.PreferenceMember\n\t}',
        {FALLBACK: ['platform choice lost "name=\\"invoice_company_name\\""']},
    ),
]
SOURCES = sorted({mutant[1] for mutant in MUTANTS})


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run_case(label, records, expected=None):
    expected = expected or {}
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (
        EVIDENCE / (label + ".stderr")
    ).open("wb") as err:
        result = subprocess.run(
            COMMAND, cwd=ROOT, stdout=out, stderr=err, timeout=300, check=False
        )
    events = [json.loads(line) for line in
              (EVIDENCE / (label + ".jsonl")).read_text().splitlines()]
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
    expected_failures = set(expected) | {test.split("/", 1)[0] for test in expected}
    matched = {}
    for test, assertions in expected.items():
        output = "".join(
            event.get("Output", "") for event in events
            if event.get("Package") == PACKAGE and event.get("Test") == test
        )
        matched[test] = [assertion for assertion in assertions if assertion in output]
    package_result = any(
        event.get("Package") == PACKAGE and "Test" not in event
        and event.get("Action") == ("fail" if expected else "pass")
        for event in events
    )
    valid = (
        result.returncode == (1 if expected else 0)
        and failed == expected_failures and REQUIRED - expected_failures <= passed
        and package_result and matched == expected
    )
    records.append({
        "label": label,
        "source_sha256": {path: sha((ROOT / path).read_bytes()) for path in SOURCES},
        "exit_code": result.returncode,
        "failed_tests": sorted(failed), "passed_tests": sorted(passed),
        "expected_assertions": expected, "matched_assertions": matched, "valid": valid,
    })
    print(json.dumps(records[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": required named runtime assertion evidence missing")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    originals = {path: (ROOT / path).read_bytes() for path in SOURCES}
    for path, data in originals.items():
        (EVIDENCE / ("original-" + path.replace("/", "__"))).write_bytes(data)
    # No template is mutated: these Go inputs compile directly. Hash the actual
    # oracles, generated UI, database loader, schema and fixtures as fixed inputs.
    fixed_paths = sorted({
        path for directory in ("internal", "migrations", "seed")
        for path in (ROOT / directory).rglob("*")
        if path.is_file() and path.suffix in (".go", ".templ", ".sql")
        and str(path.relative_to(ROOT)) not in SOURCES
    } | {ROOT / "go.mod", ROOT / "go.sum"})
    fixed = {str(path.relative_to(ROOT)): path.read_bytes() for path in fixed_paths}
    records = []
    report = {
        "checkout_commit": subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip(),
        "pr_head": os.environ.get("GOEN_PROOF_HEAD"),
        "github_sha": os.environ.get("GITHUB_SHA"),
        "github_run_id": os.environ.get("GITHUB_RUN_ID"),
        "command": COMMAND, "proof_script_sha256": sha(Path(__file__).read_bytes()),
        "original_sha256": {path: sha(data) for path, data in originals.items()},
        "fixed_input_sha256": {path: sha(data) for path, data in fixed.items()},
        "cases": records, "errors": [],
    }

    def restore():
        for path, data in originals.items():
            (ROOT / path).write_bytes(data)
        if any((ROOT / path).read_bytes() != data for path, data in originals.items()):
            raise RuntimeError("byte-exact production restoration failed")

    try:
        run_case("baseline", records)
        for label, path, before, after, assertions in MUTANTS:
            original = originals[path]
            if original.count(before) != 1 or before == after:
                raise RuntimeError(label + ": production target must match exactly once and change")
            mutated = original.replace(before, after, 1)
            (ROOT / path).write_bytes(mutated)
            (EVIDENCE / (label + ".go")).write_bytes(mutated)
            try:
                run_case(label, records, assertions)
            finally:
                restore()
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        restore()
        report["restored_byte_exact"] = all(
            (ROOT / path).read_bytes() == data for path, data in originals.items()
        )
        report["restored_sha256"] = {
            path: sha((ROOT / path).read_bytes()) for path in SOURCES
        }
        report["fixed_inputs_byte_exact"] = all(
            (ROOT / path).read_bytes() == data for path, data in fixed.items()
        )
        try:
            run_case("restored", records)
        except Exception as error:
            report["errors"].append(str(error))
        report["success"] = (
            not report["errors"] and report["restored_byte_exact"]
            and report["fixed_inputs_byte_exact"] and len(records) == len(MUTANTS) + 2
        )
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        hashes = [sha(path.read_bytes()) + "  " + path.name
                  for path in sorted(EVIDENCE.iterdir()) if path.is_file()]
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(hashes) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect company-carrier-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
