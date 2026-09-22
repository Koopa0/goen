# Commerce scenario acceptance (#331 / #337)

This directory publishes the executable composition layer for the C01–C16
scenario matrix in [#331](https://github.com/Koopa0/goen/issues/331).

`manifest.json` is the machine-readable projection of that matrix. It maps each
scenario to canonical issues, production entry points, fixtures, executable
`go test` assertions, browser evidence hooks and blocked extensions. The epic
remains the requirement index; this file is not a second task tracker.

## Commands

From a fresh clone with Go, Docker and the usual toolchain:

```sh
make commerce-acceptance-list
make commerce-acceptance              # all ready scenarios
make commerce-acceptance C05          # one scenario
make commerce-acceptance-all          # all scenarios; nonzero for blocked/failed
```

Equivalent direct entry point:

```sh
go run ./cmd/commerce-acceptance list
go run ./cmd/commerce-acceptance run --ready-only C05
go run ./cmd/commerce-acceptance run --all
```

Browser surface evidence reuses the existing `make check-layout` runner from #318/#321.
It runs the general layout suite, not a scenario-specific journey or the manifest
`pages` selection. A passing layout check does not prove a shopping journey.
Pass `--with-browser` only when Chrome and a running server are available:

```sh
make run   # in another shell
go run ./cmd/commerce-acceptance run C01 --with-browser
```

## Status semantics

- `ready` selects available assertions; it does not certify a complete journey.
  The manifest currently includes both component tests and handler compositions.
- Every assertion on a selected scenario is required. Blocked assertions and
  extensions produce explicit `BLOCKED` results and a nonzero exit, including
  single-scenario runs. Browser assertions without `--with-browser` are also
  blocked rather than silently omitted.
- `--ready-only` skips blocked scenarios during suite selection. Explicitly
  selecting a blocked scenario still returns its blocked evidence. An empty
  selection fails. Missing, skipped or failing required Go tests fail.
- Provider sandbox evidence under [#40](https://github.com/Koopa0/goen/issues/40)
  stays separate; available composition tests use local provider doubles.

## Assertion artifacts

Each invocation creates `tmp/commerce-acceptance/run-*/` in the repository.
Each result prints its `evidence=` JSON path. Numbered subdirectories contain:

- `result.json`: schema version, scenario and complete manifest assertion,
  checkout commit SHA and dirty flag, command, whether execution was attempted,
  UTC start/end, duration, `PASS`/`FAIL`/`BLOCKED`, and any error.
- `stdout.log` and `stderr.log`: separate unmodified command streams. JSON's
  `stdout_path` and `stderr_path` are relative to `result.json`, so the directory
  remains readable after downloading the CI artifact. Go test JSON parsing
  uses only stdout; dependency download diagnostics on stderr cannot corrupt it.

Blocked assertions save metadata and empty streams with `executed: false`.
Evidence-directory or write failures fail the run. A dirty checkout records its
base commit and `dirty: true`; that is not immutable-commit acceptance evidence.
Browser records have `surface_only: true`: their result describes the general
layout command, not a complete C01-C16 journey or proof of the running server's
binary identity. Scenario-specific browser flows remain an explicit gap.

The integration-tagged C06 runner test invokes the real PostgreSQL-backed
checkout replay assertion and saves this contract under the same directory.
The schema CI job uploads it as `commerce-assertions-<checkout SHA>`, including
failed runs when artifacts were produced. Protocol regression fixtures use
separate temporary directories; they are not shopping-journey artifacts.
