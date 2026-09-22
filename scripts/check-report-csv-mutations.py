"""Record production CSV and rendered-link mutations in disposable CI."""

import json
import os
import signal
from pathlib import Path
import subprocess


def observed(result, test, action, reason=None):
    events = []
    for line in result.stdout.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict) and event.get("Test") == test:
            events.append(event)
    actions = {event.get("Action") for event in events}
    output = "".join(event.get("Output", "") for event in events)
    return {"run", action}.issubset(actions) and (reason is None or reason in output)


def main():
    if os.environ.get("GITHUB_ACTIONS") != "true":
        raise SystemExit("CSV mutations require a disposable CI checkout")
    csv_source = Path("internal/admin/report_csv.go")
    template = Path("internal/ui/pages/adminreport.templ")
    generated = Path("internal/ui/pages/adminreport_templ.go")
    originals = {p: p.read_text() for p in (csv_source, template, generated)}
    unit = ["go", "test", "./internal/admin", "-count=1", "-json", "-timeout=5m", "-run", "^TestBestSellerCSV"]
    view = ["go", "test", "./internal/ui/pages", "-count=1", "-json", "-timeout=5m", "-run", "^TestReportExportKeepsWindowAndExplainsGrossAmount$"]
    integration = ["go", "test", "-tags=integration", "./internal/admin", "-count=1", "-json", "-timeout=5m", "-run", "^TestReportCSVMatchesTheSelectedQueryWindow$"]
    checks = [
        (unit, ["TestBestSellerCSVPreservesTextAndMoney", "TestBestSellerCSVEmptyWindowKeepsColumns"]),
        (view, ["TestReportExportKeepsWindowAndExplainsGrossAmount"]),
        (integration, ["TestReportCSVMatchesTheSelectedQueryWindow"]),
    ]
    with Path("report-csv-mutations.log").open("w") as log:
        def record(value):
            print(value, flush=True)
            log.write(value + "\n")
            log.flush()

        def run(label, command):
            result = subprocess.run(command, capture_output=True, text=True, check=False)
            record(f"{label}: exit {result.returncode}\n{result.stdout}{result.stderr}")
            return result

        def restore():
            for path, contents in originals.items():
                path.write_text(contents)

        record("checkout=" + subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip())
        record("pr_head=" + os.environ.get("PR_HEAD_SHA", "unknown"))
        for command, names in checks:
            baseline = run("baseline", command)
            if baseline.returncode != 0 or not all(observed(baseline, name, "pass") for name in names):
                raise SystemExit("baseline must pass before mutation")

        cases = [
            ("formula", csv_source, 'return "\'" + value', "return value", unit, "TestBestSellerCSVPreservesTextAndMoney", "CSV exposes a spreadsheet formula"),
            ("integer cents", csv_source, "strconv.FormatInt(seller.RevenueCents, 10)", "strconv.FormatInt(seller.RevenueCents/100, 10)", unit, "TestBestSellerCSVPreservesTextAndMoney", "CSV lost text or integer cents"),
            ("selected window", csv_source, "h.store.Report(r.Context(), int32(days))", "h.store.Report(r.Context(), int32(days)*0+30)", integration, "TestReportCSVMatchesTheSelectedQueryWindow", "differs from HTML"),
            ("cache privacy", csv_source, 'Set("Cache-Control", "no-store")', 'Set("Cache-Control", "public")', integration, "TestReportCSVMatchesTheSelectedQueryWindow", "CSV response"),
            ("export link", template, "templ.SafeURL(v.ExportHref())", 'templ.SafeURL("/admin/reports")', view, "TestReportExportKeepsWindowAndExplainsGrossAmount", "empty report lost selected-window export"),
        ]
        def interrupted(signum, _frame):
            raise SystemExit(128 + signum)

        signal.signal(signal.SIGTERM, interrupted)
        signal.signal(signal.SIGINT, interrupted)
        try:
            for label, path, before, after, command, test, reason in cases:
                original = originals[path]
                if original.count(before) != 1:
                    raise SystemExit(f"mutation anchor is not unique: {label}")
                mutated = original.replace(before, after, 1)
                path.write_text(mutated)
                if path.read_text() != mutated:
                    raise SystemExit("production mutation did not reach the source")
                record(f"production mutant {label}: {before} => {after}")
                if path == template:
                    if run("generate mutated template", ["go", "tool", "templ", "generate", "-f", str(template)]).returncode:
                        raise SystemExit("template generation failed; not a watched red")
                    matches = [line for line in generated.read_text().splitlines() if after in line]
                    if not matches:
                        raise SystemExit("mutation did not reach generated production code")
                    record("generated production match:\n" + "\n".join(matches))
                result = run(label, command)
                if result.returncode != 1 or not observed(result, test, "fail", reason):
                    raise SystemExit(f"missing expected runtime red: {label}")
                restore()
        finally:
            restore()
        for command, names in checks:
            restored = run("restored", command)
            if restored.returncode != 0 or not all(observed(restored, name, "pass") for name in names):
                raise SystemExit("restored production source must pass")
        record("all CSV mutations reached production and failed the specified test")


if __name__ == "__main__":
    main()
