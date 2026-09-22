"""Verify that unknown expressions and broken production methods fail in CI."""

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
        raise SystemExit("form action mutations require a disposable CI checkout")
    method = Path("internal/ui/pages/pay.go")
    template = Path("internal/ui/pages/pay.templ")
    generated = Path("internal/ui/pages/pay_templ.go")
    originals = {p: p.read_text() for p in (method, template, generated)}
    test = "TestEveryFormActionResolvesToAPostRoute"
    command = ["go", "test", "./internal/ui/pages", "-count=1", "-json", "-timeout=5m", "-run", "^" + test + "$"]
    with Path("form-action-mutations.log").open("w") as log:
        def record(value):
            print(value, flush=True)
            log.write(value + "\n")
            log.flush()

        def run(label, args):
            result = subprocess.run(args, capture_output=True, text=True, check=False)
            record(f"{label}: exit {result.returncode}\n{result.stdout}{result.stderr}")
            return result

        def restore():
            for path, contents in originals.items():
                path.write_text(contents)

        record("checkout=" + subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip())
        record("pr_head=" + os.environ.get("PR_HEAD_SHA", "unknown"))
        baseline = run("baseline", command)
        if baseline.returncode != 0 or not observed(baseline, test, "pass"):
            raise SystemExit("baseline must pass before mutation")

        cases = [
            (method, 'return "/orders/" + v.Number + "/pay"', 'return "/orders/" + v.Number + "/no-payment-handler"', 'form action "/orders/order/no-payment-handler" has no post route'),
            (template, "templ.SafeURL(v.Action())", "templ.SafeURL(v.Number)", "unresolved form action templ.SafeURL(v.Number)"),
        ]
        def interrupted(signum, _frame):
            raise SystemExit(128 + signum)

        signal.signal(signal.SIGTERM, interrupted)
        signal.signal(signal.SIGINT, interrupted)
        try:
            for path, before, after, reason in cases:
                if originals[path].count(before) != 1:
                    raise SystemExit(f"mutation anchor is not unique: {path}")
                mutated = originals[path].replace(before, after, 1)
                path.write_text(mutated)
                if path.read_text() != mutated:
                    raise SystemExit("production mutation did not reach the source")
                record(f"production mutant {path}: {before} => {after}")
                if path == template:
                    if run("generate mutated template", ["go", "tool", "templ", "generate", "-f", str(template)]).returncode:
                        raise SystemExit("template generation failed; not a watched red")
                    matches = [line for line in generated.read_text().splitlines() if after in line]
                    if not matches:
                        raise SystemExit("mutation did not reach generated production code")
                    record("generated production match:\n" + "\n".join(matches))
                result = run(str(path), command)
                if result.returncode != 1 or not observed(result, test, "fail", reason):
                    raise SystemExit("missing expected runtime red")
                restore()
        finally:
            restore()
        restored = run("restored", command)
        if restored.returncode != 0 or not observed(restored, test, "pass"):
            raise SystemExit("restored production source must pass")
        record("both form-action mutations failed the production route guard")


if __name__ == "__main__":
    main()
