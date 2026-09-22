"""Build and serve focused layout mutants only inside disposable GitHub CI."""

import hashlib
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import tempfile
import time
import urllib.request


def main():
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.environ.get("GOEN_LAYOUT_DISPOSABLE") != "1":
        raise SystemExit("two-factor mutations require disposable GitHub CI")
    proof = Path("twofactor-mutations").resolve()
    proof.mkdir(exist_ok=True)
    template = Path("internal/ui/pages/twofactor.templ")
    generated = Path("internal/ui/pages/twofactor_templ.go")
    coverage = Path("scripts/admin-layout-rows.mjs")
    originals = {path: path.read_bytes() for path in (template, generated, coverage)}
    required = {"real-verification-post", "challenge-375", "challenge-1440", "enrol-375", "enrol-1440", "admin-375", "admin-1440", "focused-layout"}
    expected = {
        "otp": ("challenge-375", "OTP input is absent or not visible"),
        "qr": ("enrol-375", "enrolment QR is absent or unloaded"),
        "coverage": ("admin-coverage", "admin coverage missing required rows"),
    }
    (proof / "checkout.txt").write_text(subprocess.check_output(["git", "rev-parse", "HEAD"], text=True))
    (proof / "pr-head.txt").write_text(os.environ.get("PR_HEAD_SHA", "unknown") + "\n")
    makefile = Path("Makefile").read_text()
    version = re.search(r"^AXE_CORE_VERSION := (.+)$", makefile, re.M).group(1)
    digest = re.search(r"^AXE_CORE_SHA256 := (.+)$", makefile, re.M).group(1)
    with urllib.request.urlopen(f"https://unpkg.com/axe-core@{version}/axe.min.js", timeout=30) as response:
        axe = response.read()
    if hashlib.sha256(axe).hexdigest() != digest:
        raise SystemExit("axe source does not match the repository pin")
    (proof / "axe.min.js").write_bytes(axe)
    chrome = subprocess.check_output(["sh", "scripts/resolve-chrome.sh"], text=True).strip()

    def restore():
        for path, contents in originals.items():
            path.write_bytes(contents)
            if path.read_bytes() != contents:
                raise SystemExit(f"failed to restore {path}")

    def stop(process):
        if process is not None:
            # The browser parent may exit before its renderer children.
            # Kill only this launch's process group, including those children.
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait(timeout=10)

    def port():
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            return sock.getsockname()[1]

    def ready(process, url):
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError(f"owned process {process.pid} exited before readiness")
            try:
                with urllib.request.urlopen(url, timeout=1) as response:
                    if response.status == 200:
                        return
            except OSError:
                pass
            time.sleep(0.1)
        raise RuntimeError(f"owned process {process.pid} did not become ready")

    def build(label, binary):
        with (proof / f"{label}-build.log").open("w") as log:
            result = subprocess.run(["go", "build", "-o", str(binary), "./cmd/goen"], stdout=log, stderr=subprocess.STDOUT, check=False, timeout=300)
        if result.returncode:
            raise RuntimeError(f"{label} failed to build; not mutation evidence")

    def run(label, binary, workspace):
        server_port, cdp_port = port(), port()
        while cdp_port == server_port:
            cdp_port = port()
        env = os.environ.copy()
        env.update(GOEN_ADDR=f"127.0.0.1:{server_port}", GOEN_URL=f"http://127.0.0.1:{server_port}",
                   GOEN_BASE_URL=f"http://127.0.0.1:{server_port}", GOEN_INSECURE_COOKIES="1",
                   CDP_PORT=str(cdp_port), AXE_SOURCE=str(proof / "axe.min.js"))
        server = browser = None
        with (proof / f"{label}-server.log").open("w") as server_log, (proof / f"{label}-chrome.log").open("w") as chrome_log:
            try:
                server = subprocess.Popen([str(binary)], env=env, stdout=server_log, stderr=subprocess.STDOUT, start_new_session=True)
                ready(server, env["GOEN_URL"] + "/readyz")
                browser = subprocess.Popen([chrome, "--headless", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
                    "--remote-debugging-address=127.0.0.1", f"--remote-debugging-port={cdp_port}",
                    f"--user-data-dir={workspace / label}", "about:blank"], stdout=chrome_log, stderr=subprocess.STDOUT, start_new_session=True)
                ready(browser, f"http://127.0.0.1:{cdp_port}/json/version")
                (proof / f"{label}-processes.json").write_text(json.dumps({"server_pid": server.pid, "server_port": server_port, "chrome_pid": browser.pid, "cdp_port": cdp_port}))
                result = subprocess.run(["node", "scripts/check-twofactor-focused.mjs"], env=env, capture_output=True, text=True, check=False, timeout=180)
                (proof / f"{label}.jsonl").write_text(result.stdout)
                (proof / f"{label}-stderr.log").write_text(result.stderr)
                records = []
                for line in result.stdout.splitlines():
                    try:
                        record = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    if isinstance(record, dict):
                        records.append(record)
                print(f"{label}: exit {result.returncode}\n{result.stdout}{result.stderr}", flush=True)
                if label in expected:
                    target, reason = expected[label]
                    if result.returncode != 1 or not any(r.get("event") == "assert-fail" and r.get("target") == target and r.get("reason") == reason for r in records):
                        raise RuntimeError(f"{label} did not fail its specified browser assertion")
                else:
                    passed = {r.get("target") for r in records if r.get("event") == "pass"}
                    if result.returncode != 0 or not required.issubset(passed):
                        raise RuntimeError(f"{label} did not pass every actual target route")
            finally:
                try:
                    stop(browser)
                finally:
                    stop(server)

    def interrupted(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    try:
        with tempfile.TemporaryDirectory(prefix="goen-twofactor-") as directory:
            workspace = Path(directory)
            binary = workspace / "goen"
            build("baseline", binary)
            run("baseline", binary, workspace)
            for label in expected:
                restore()
                text = template.read_text()
                if label == "otp":
                    before, challenge = text.split("} else if v.CanVerify() {", 1)
                    anchor = 'Name:  "code"'
                    if challenge.count(anchor) != 1:
                        raise RuntimeError("challenge OTP mutation anchor is not unique")
                    template.write_text(before + "} else if v.CanVerify() {" + challenge.replace(anchor, 'Name:  "mutation_missing_code"', 1))
                    marker = '"mutation_missing_code"'
                elif label == "qr":
                    anchor = 'if v.QRCode != "" {'
                    if text.count(anchor) != 1:
                        raise RuntimeError("QR mutation anchor is not unique")
                    template.write_text(text.replace(anchor, 'if v.QRCode == "" {', 1))
                    marker = 'if v.QRCode == "" {'
                else:
                    text = coverage.read_text()
                    anchor = "for (const row of rows) {"
                    if text.count(anchor) != 1:
                        raise RuntimeError("admin coverage mutation anchor is not unique")
                    coverage.write_text(text.replace(anchor, "for (const row of []) {", 1))
                if label != "coverage":
                    with (proof / f"{label}-generation.log").open("w") as log:
                        subprocess.run(["go", "tool", "templ", "generate", "-path", "internal/ui"], stdout=log, stderr=subprocess.STDOUT, check=True, timeout=120)
                    matches = [line for line in generated.read_text().splitlines() if marker in line]
                    if not matches:
                        raise RuntimeError("mutation did not reach generated production code")
                    (proof / f"{label}-generated-target.txt").write_text("\n".join(matches) + "\n")
                    build(label, binary)
                else:
                    # Restore the binary after the preceding template mutation.
                    build(label, binary)
                (proof / f"{label}.patch").write_bytes(subprocess.check_output(["git", "diff", "--", str(template), str(generated), str(coverage)]))
                run(label, binary, workspace)
            restore()
            build("restored", binary)
            run("restored", binary, workspace)
    finally:
        restore()
    (proof / "result.txt").write_text("PASS: baseline and restored routes passed; all three mutants failed their specified browser assertion\n")


if __name__ == "__main__":
    main()
