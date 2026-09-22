"""Exercise provider-posture defects in the disposable CI checkout."""

import os
from pathlib import Path
import subprocess


def main():
    if os.environ.get("GITHUB_ACTIONS") != "true":
        raise SystemExit("provider posture mutations require a disposable CI checkout")
    source = Path("cmd/goen/provider_posture.go")
    original = source.read_text()
    evidence = Path("provider-posture-mutations.log")
    name = "TestProviderModeIsIndependentOfSecureCookies"

    with evidence.open("w") as log:
        def record(value):
            print(value, flush=True)
            log.write(value + "\n")
            log.flush()

        def run(label):
            result = subprocess.run(
                ["go", "test", "./cmd/goen", "-count=1", "-v", "-run", "^" + name + "$"],
                capture_output=True, text=True, check=False,
            )
            record(f"{label}: exit {result.returncode}\n{result.stdout}{result.stderr}")
            return result

        record("checkout=" + subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip())
        record("pr_head=" + os.environ.get("PR_HEAD_SHA", "unknown"))
        if run("baseline").returncode != 0:
            raise SystemExit("baseline must pass before mutation")

        cases = [
            ("unknown_mode", 'return errors.New("GOEN_PROVIDER_MODE must be sandbox or live")', "return nil", "GOEN_PROVIDER_MODE"),
            ("insecure_live", 'return errors.New("GOEN_PROVIDER_MODE=live requires secure cookies; remove GOEN_INSECURE_COOKIES")', "return nil", "secure cookies"),
            ("test_key_in_live", 'return errors.New("GOEN_STRIPE_API_KEY must match GOEN_PROVIDER_MODE (sandbox test key or live key)")', "return nil", "GOEN_STRIPE_API_KEY"),
            ("live_implicit_invoice_endpoint", 'return errors.New("GOEN_ECPAY_BASE_URL must explicitly name the production invoice endpoint in live mode")', "return nil", "GOEN_ECPAY_BASE_URL"),
            ("staging_merchant_with_live_endpoint", 'return errors.New("GOEN_ECPAY_MERCHANT_ID is the published staging merchant, not a live merchant")', "return nil", "GOEN_ECPAY_MERCHANT_ID"),
            ("sandbox_production_endpoint", 'return errors.New("GOEN_ECPAY_BASE_URL names production while GOEN_PROVIDER_MODE is sandbox")', "return nil", "GOEN_ECPAY_BASE_URL"),
            ("malformed_sandbox_sender", 'return errors.New("GOEN_SMTP_FROM must be a valid sender address")', "return nil", "GOEN_SMTP_FROM"),
            ("live_placeholder_sender", 'return errors.New("GOEN_SMTP_FROM must replace the goen.example placeholder in live mode")', "return nil", "GOEN_SMTP_FROM"),
            ("https_sandbox", "cfg.ProviderMode = providerSandbox", "cfg.ProviderMode = providerLive", "GOEN_STRIPE_API_KEY"),
        ]
        try:
            for subtest, before, after, reason in cases:
                if original.count(before) != 1:
                    raise SystemExit(f"mutation anchor is not unique: {subtest}")
                mutated = original.replace(before, after, 1)
                source.write_text(mutated)
                if source.read_text() != mutated:
                    raise SystemExit("production mutation did not reach the source")
                record(f"production mutant {subtest}: {before} => {after}")
                result = run(subtest)
                expected = f"--- FAIL: {name}/{subtest}"
                if result.returncode == 0 or expected not in result.stdout or reason not in result.stdout:
                    raise SystemExit(f"missing expected runtime red: {subtest}")
                source.write_text(original)
        finally:
            source.write_text(original)
        if run("restored").returncode != 0:
            raise SystemExit("restored production source must pass")
        record("all provider-posture mutations reached production and failed the specified test")


if __name__ == "__main__":
    main()
