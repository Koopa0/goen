#!/usr/bin/env python3
CASES = [{'name': 'confirmation',
  'path': 'internal/admin/returns_handler.go',
  'before': 'if h.confirmReturnDecision(w, r) {',
  'after': 'if false && h.confirmReturnDecision(w, r) {',
  'test': 'TestReturnDecisionHTTPRequiresConfirmation',
  'marker': 'decision did not render its confirmation: 303',
  'package': './internal/admin',
  'integration': True},
 {'name': 'rejection-reason',
  'path': 'internal/admin/returns_store.go',
  'before': 'if kind == returns.DecisionReject && strings.TrimSpace(resolution) == "" {',
  'after': 'if false && kind == returns.DecisionReject && strings.TrimSpace(resolution) == "" {',
  'test': 'TestReturnRejectionRequiresAReasonAtTheStoreBoundary',
  'marker': 'blank rejection = <nil>',
  'package': './internal/admin',
  'integration': True},
 {'name': 'required-markup',
  'path': 'internal/ui/pages/adminreturnconfirm.templ',
  'before': 'required?={ v.Required }',
  'after': 'required?={ false }',
  'test': 'TestReturnConfirmationOffersOnlyTheSelectedDecision',
  'marker': 'rejected required reason control disagrees with decision',
  'package': './internal/ui/pages',
  'integration': False,
  'generated': 'internal/ui/pages/adminreturnconfirm_templ.go',
  'probe': 'if false {'}]

# Baselines and reds must identify actual running tests; build errors and skipped
# names cannot establish that a production defect reached its assertion.
import json
import os
from pathlib import Path
import signal
import sys
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
os.chdir(repo)
proof = Path(os.environ.get("RUNNER_TEMP", tempfile.gettempdir())) / "goen-issue-88-mutations"
proof.mkdir(parents=True, exist_ok=True)
paths = sorted({c["path"] for c in CASES} | {c["generated"] for c in CASES if "generated" in c})
original = {p: Path(p).read_bytes() for p in paths}


def run(args, name):
    with (proof / name).open("w") as log:
        return subprocess.run(args, stdout=log, stderr=subprocess.STDOUT, check=False).returncode


def events(name):
    result = []
    for line in (proof / name).read_text().splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(event, dict):
            result.append(event)
    return result


def restore():
    for path, content in original.items():
        Path(path).write_bytes(content)
        if Path(path).read_bytes() != content:
            raise RuntimeError("production restore differs: " + path)


def test_command(package, integration, tests):
    args = ["go", "test", "-json", "-race", "-count=1", "-timeout=5m"]
    if integration:
        args.append("-tags=integration")
    return args + [package, "-run", "^(" + "|".join(sorted(tests)) + ")$"]


def green(phase):
    groups = {}
    for case in CASES:
        groups.setdefault((case["package"], case["integration"]), set()).add(case["test"])
    for index, ((package, integration), tests) in enumerate(groups.items()):
        log = f"{phase}-{index}.jsonl"
        if run(test_command(package, integration, tests), log) != 0:
            print((proof / log).read_text()[-16000:], file=sys.stderr)
            raise RuntimeError(phase + " failed: " + log)
        records = events(log)
        for test in tests:
            for action in ("run", "pass"):
                if not any(e.get("Action") == action and e.get("Test") == test for e in records):
                    raise RuntimeError(f"{phase} did not {action} {test}")


def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


signal.signal(signal.SIGTERM, interrupted)
signal.signal(signal.SIGINT, interrupted)
run(["git", "rev-parse", "HEAD"], "commit.txt")
(proof / "pr-head.txt").write_text(os.environ.get("GOEN_PROOF_HEAD", "unknown") + "\n")
try:
    green("baseline")
    for case in CASES:
        restore()
        path = Path(case["path"])
        source = path.read_text()
        if source.count(case["before"]) != 1:
            raise RuntimeError("mutation target is not unique: " + case["name"])
        path.write_text(source.replace(case["before"], case["after"], 1))
        if "generated" in case:
            command = ["make", "sqlc"] if path.suffix == ".sql" else ["go", "tool", "templ", "generate", "-path", "internal/ui"]
            if run(command, case["name"] + "-generation.log") != 0:
                raise RuntimeError("mutation generation failed: " + case["name"])
            if run(["grep", "-F", case["probe"], case["generated"]], case["name"] + "-generated-target.txt") != 0:
                raise RuntimeError("mutation absent from generated production code")
        run(["git", "diff", "--"] + paths, case["name"] + ".patch")
        log = case["name"] + ".jsonl"
        code = run(test_command(case["package"], case["integration"], [case["test"]]), log)
        records = events(log)
        failed = {e.get("Test") for e in records if e.get("Action") == "fail" and e.get("Test")}
        ran = {e.get("Test") for e in records if e.get("Action") == "run"}
        assertion = [e.get("Output", "").strip() for e in records
                     if e.get("Test") in failed and e.get("Test") in ran
                     and (e.get("Test") == case["test"] or e.get("Test", "").startswith(case["test"] + "/"))
                     and case["marker"] in e.get("Output", "")]
        if code == 0 or case["test"] not in failed or case["test"] not in ran or not assertion:
            print((proof / log).read_text()[-16000:], file=sys.stderr)
            raise RuntimeError("no matching runtime red: " + case["name"])
        message = "Observed runtime red: " + assertion[0]
        print(message, flush=True)
        (proof / (case["name"] + "-red.txt")).write_text(message + "\n")
    restore()
    green("restored")
    print("Production sources restored; every named scoped test ran and passed.", flush=True)
finally:
    restore()
