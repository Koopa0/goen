#!/usr/bin/env python3
"""Prove the rendered gross, quantity and scope assertions in GitHub Actions."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / "internal/ui/pages/adminreport.templ"
GENERATED = ROOT / "internal/ui/pages/adminreport_templ.go"
EVIDENCE = ROOT / "bestseller-labels-proof"
PACKAGE = "github.com/koopa0/goen/internal/ui/pages"
TEST = "TestBestSellerGrossCannotBeMistakenForOrderRevenue"
REQUIRED = {TEST, TEST + "/en", TEST + "/zh-Hant"}
COMMAND = ["go", "test", "-json", "-count=1", "-timeout=3m", "./internal/ui/pages", "-run", "^" + TEST + "$"]
MUTANTS = [
    ("gross-label-removed", b"fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepGross), item.Revenue())", b"item.Revenue()", b"i18n.KeyAdminRepGross)", 'report lacks "Product gross NT$1,000"', 'report lacks "商品毛額 NT$1,000"'),
    ("units-label-removed", b"fmt.Sprintf(i18n.T(ctx, i18n.KeyAdminRepUnits), item.UnitsText())", b"item.UnitsText()", b"i18n.KeyAdminRepUnits)", 'report lacks "2 units sold"', 'report lacks "售出 2 件"'),
    ("gross-scope-removed", b"i18n.T(ctx, i18n.KeyAdminRepGrossNote)", b'""', b"i18n.KeyAdminRepGrossNote)", 'report lacks "before order discounts or refunds and excluding shipping and order tax"', 'report lacks "未扣訂單折扣或退款，不含運費與訂單稅額"'),
]


def sha(data):
    return hashlib.sha256(data).hexdigest()


def run_case(label, cases, assertions=None):
    with (EVIDENCE / (label + ".jsonl")).open("wb") as out, (EVIDENCE / (label + ".stderr")).open("wb") as err:
        result = subprocess.run(COMMAND, cwd=ROOT, stdout=out, stderr=err, timeout=240, check=False)
    events = [json.loads(line) for line in (EVIDENCE / (label + ".jsonl")).read_text().splitlines()]
    outcomes = {action: {e["Test"] for e in events if e.get("Package") == PACKAGE and e.get("Action") == action and "Test" in e} for action in ["pass", "fail", "skip"]}
    matches = {}
    for locale, assertion in (assertions or {}).items():
        output = "".join(e.get("Output", "") for e in events if e.get("Package") == PACKAGE and e.get("Test") == TEST + "/" + locale)
        matches[locale] = assertion if assertion in output else None
    package_result = any(e.get("Package") == PACKAGE and "Test" not in e and e.get("Action") == ("fail" if assertions else "pass") for e in events)
    valid = result.returncode == (1 if assertions else 0) and outcomes["fail"] == (REQUIRED if assertions else set()) and outcomes["pass"] == (set() if assertions else REQUIRED) and not outcomes["skip"] and package_result and all(matches.values())
    cases.append({"label": label, "source_sha256": sha(SOURCE.read_bytes()), "generated_sha256": sha(GENERATED.read_bytes()), "exit_code": result.returncode, "outcomes": {k: sorted(v) for k, v in outcomes.items()}, "expected_assertions": assertions, "matched_assertions": matches, "valid": valid})
    print(json.dumps(cases[-1]), flush=True)
    if not valid:
        raise RuntimeError(label + ": named bilingual assertion evidence is missing")


def generate(label):
    with (EVIDENCE / (label + "-generation.log")).open("wb") as out:
        result = subprocess.run(["go", "tool", "templ", "generate", "-path", "internal/ui"], cwd=ROOT, stdout=out, stderr=subprocess.STDOUT, timeout=180, check=False)
    if result.returncode != 0:
        raise RuntimeError(label + ": generation failed, not a behavioral red")


def main():
    if os.environ.get("CI") != "true" or os.environ.get("GITHUB_ACTIONS") != "true":
        raise RuntimeError("this proof may run only in GitHub Actions")
    EVIDENCE.mkdir(exist_ok=False)
    original = SOURCE.read_bytes()
    generated = GENERATED.read_bytes()
    paths = set((ROOT / "internal").rglob("*.go")) | set((ROOT / "internal/ui").rglob("*.templ")) | {ROOT / "go.mod", ROOT / "go.sum"}
    paths -= {SOURCE, GENERATED}
    inputs = {str(p.relative_to(ROOT)): p.read_bytes() for p in sorted(paths)}
    cases = []
    report = {"checkout_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(), "github_sha": os.environ.get("GITHUB_SHA"), "command": COMMAND, "original_sha256": sha(original), "original_generated_sha256": sha(generated), "input_sha256": {p: sha(b) for p, b in inputs.items()}, "cases": cases, "errors": []}
    (EVIDENCE / "original.templ").write_bytes(original)
    (EVIDENCE / "original_templ.go").write_bytes(generated)
    try:
        run_case("baseline", cases)
        for label, before, after, marker, english, chinese in MUTANTS:
            if original.count(before) != 1 or marker not in generated:
                raise RuntimeError(label + ": production source/generated target is not identifiable")
            SOURCE.write_bytes(original.replace(before, after, 1))
            try:
                generate(label)
                projected = GENERATED.read_bytes()
                if marker in projected or projected == generated:
                    raise RuntimeError(label + ": mutation did not reach generated production code")
                (EVIDENCE / (label + ".templ")).write_bytes(SOURCE.read_bytes())
                (EVIDENCE / (label + "_templ.go")).write_bytes(projected)
                run_case(label, cases, {"en": english, "zh-Hant": chinese})
            finally:
                SOURCE.write_bytes(original)
                generate(label + "-restored")
                if GENERATED.read_bytes() != generated:
                    raise RuntimeError(label + ": regenerated restoration differs from original bytes")
    except Exception as error:
        report["errors"].append(str(error))
    finally:
        SOURCE.write_bytes(original)
        try:
            generate("final-restored")
            run_case("restored", cases)
        except Exception as error:
            report["errors"].append(str(error))
        report["source_restored_byte_exact"] = SOURCE.read_bytes() == original
        report["generated_restored_byte_exact"] = GENERATED.read_bytes() == generated
        report["inputs_byte_exact"] = all((ROOT / p).read_bytes() == b for p, b in inputs.items())
        report["success"] = not report["errors"] and report["source_restored_byte_exact"] and report["generated_restored_byte_exact"] and report["inputs_byte_exact"] and len(cases) == 5 and all(c["valid"] for c in cases)
        (EVIDENCE / "report.json").write_text(json.dumps(report, indent=2) + "\n")
        (EVIDENCE / "SHA256SUMS").write_text("\n".join(sha(p.read_bytes()) + "  " + p.name for p in sorted(EVIDENCE.iterdir()) if p.is_file()) + "\n")
    if not report["success"]:
        raise RuntimeError("proof failed; inspect bestseller-labels-proof/report.json")


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
