# P7 — Build, packaging, CI/CD, and observability

> Self-contained brief. Assume no context beyond this file and the repository.
> **For:** Codex · **Order:** any time.

`goen` is a Traditional-Chinese 3C storefront: one Go binary plus one PostgreSQL.
Go 1.27.0, 103 modules in the graph, `net/http` (no framework), `templ` server-rendered
HTML, pgx + sqlc, embedded assets via `go:embed`, no cgo.

Read `CLAUDE.md`, especially **"Build tools stay out of go.mod"** — the project measured
that adding `ko` as a `tool` directive took the module graph from 104 to 528, because
ko carries the AWS, Azure, GCP and Kubernetes clients, none of which reach the binary
but all of which would then sit inside every `govulncheck`, Trivy and Dependabot run.
The rule is: version-pin build tools in the Makefile and invoke them from outside the
module with `go run pkg@version`.

Everything below must respect that rule or argue explicitly against it with numbers.

## Answer these

1. **Packaging.** `ko` is currently used (`make image`, base image pinned by digest).
   Is that still the right choice versus a plain multi-stage `Dockerfile`, `buildpacks`,
   or `bazel`? Give the actual trade for THIS project: one binary, embedded assets
   (`go:embed`), no cgo. **Bazel specifically**: state plainly what it would buy a
   single-module, single-binary Go project, and what it would cost. A recommendation to
   change nothing is a good answer if it is the right one.
2. **CI/CD.** There appears to be no CI configuration in the repository — confirm that,
   then design one. It must run the three gates the project already has
   (`make verify` — and it must run TWICE, because this repository has been bitten by a
   gate that repaired what it checked — `make test-integration` which needs Docker, and
   `make check-layout` which needs Chrome). Add `govulncheck` and container scanning.
   Say where it runs, how long it takes, and what it costs. Include the release path:
   tagging, image publishing, provenance/SBOM, and whether that is over-engineering here.
3. **Linting.** `.golangci.yml` v2.13.2, zero tolerated findings, with `exhaustruct_v5`
   enabled for exactly one type. Read it and say whether the configuration is
   well-chosen or accumulated. Which enabled linters earn their place, which are noise,
   and what is missing? Also `squawk` for SQL migrations — is it configured to catch
   what matters?
4. **Observability — and this is the one to be most careful about.** The project has
   `log/slog` and nothing else. Work out what is actually worth adding for a
   single-binary application:
   - **OpenTelemetry**: what does tracing buy when the entire request is one process and
     one database? Where would a span boundary genuinely explain something a slog line
     cannot — the outbox worker, the Stripe round trip, the recommendation projection?
   - **Prometheus**: which metrics matter here? The project already computes health from
     the WORK rather than a heartbeat (`/admin/health` counts undelivered messages,
     unreleased holds, projection age) — how does that relate to metrics, and does one
     replace the other?
   - **Grafana / Jaeger**: are these worth putting in front of a portfolio project, or
     do they turn "clone and run" into "clone, run a stack, and run"?
   - **Module cost of each.** `go list -m all | wc -l` before and after, and how much of
     it reaches the binary. OTel is not small.
   - A staged recommendation: what to add now, what to add if it were deployed, what
     never.
5. **What "professional" actually means here.** The owner's reference points are
   CockroachDB, Zed and Google open source. Look at what those projects' CI and release
   engineering actually do, and separate what scales down to a one-binary project from
   what does not.

## Report

A recommendation per numbered item, each with the cost in modules, in CI minutes, and
in what a reader has to install to run the thing.

Then end with the same three lists every brief in this set ends with:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.
