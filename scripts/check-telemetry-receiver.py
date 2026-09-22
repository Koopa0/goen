#!/usr/bin/env python3
"""Prove the supplied receiver accepts OTLP and exposes both local views."""

import json
import subprocess
import time
import urllib.error
import urllib.request


def read_metrics():
    with urllib.request.urlopen("http://127.0.0.1:8889/metrics", timeout=2) as response:
        return response.read().decode()


def send(signal, payload):
    request = urllib.request.Request(
        "http://127.0.0.1:4318/v1/" + signal,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=3) as response:
        result = json.loads(response.read())
    partial = result.get("partialSuccess", {})
    if int(partial.get("rejectedSpans", 0)) or int(partial.get("rejectedDataPoints", 0)):
        raise RuntimeError(f"receiver rejected {signal}: {partial}")


def main():
    ready_by = time.monotonic() + 30
    while True:
        try:
            read_metrics()
            break
        except (urllib.error.URLError, TimeoutError):
            if time.monotonic() >= ready_by:
                raise
            time.sleep(0.25)

    now = time.time_ns()
    resource = {"attributes": [{"key": "service.name", "value": {"stringValue": "goen-receiver-check"}}]}
    trace_id = "1234567890abcdef1234567890abcdef"
    send("traces", {"resourceSpans": [{
        "resource": resource,
        "scopeSpans": [{"scope": {"name": "receiver-check"}, "spans": [{
            "traceId": trace_id,
            "spanId": "1234567890abcdef",
            "name": "goen-receiver-acceptance",
            "kind": 2,
            "startTimeUnixNano": str(now),
            "endTimeUnixNano": str(now + 1_000_000),
        }]}],
    }]})
    send("metrics", {"resourceMetrics": [{
        "resource": resource,
        "scopeMetrics": [{"scope": {"name": "receiver-check"}, "metrics": [{
            "name": "goen.receiver.check",
            "gauge": {"dataPoints": [{"timeUnixNano": str(now), "asInt": "7"}]},
        }]}],
    }]})

    visible_by = time.monotonic() + 15
    while True:
        metrics = read_metrics()
        logs = subprocess.check_output([
            "docker", "compose", "-f", "docker-compose.telemetry.yml",
            "logs", "--no-color", "collector",
        ], text=True, timeout=5)
        metric_visible = any(
            line.startswith("goen_receiver_check") and line.split()[-1] == "7"
            for line in metrics.splitlines() if line and not line.startswith("#")
        )
        if metric_visible and trace_id in logs and "goen-receiver-acceptance" in logs:
            print("OTLP metric value and trace identity are visible in the supplied local views")
            return
        if time.monotonic() >= visible_by:
            raise RuntimeError("accepted OTLP did not reach both the metric and trace views")
        time.sleep(0.25)


if __name__ == "__main__":
    main()
