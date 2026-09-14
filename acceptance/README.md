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

Browser evidence reuses the existing `make check-layout` runner from #318/#321.
Pass `--with-browser` only when Chrome and a running server are available:

```sh
make run   # in another shell
go run ./cmd/commerce-acceptance run C01 --with-browser
```

## Status semantics

- `ready` scenarios run their `go_test` assertions through production handlers,
  a real PostgreSQL testcontainer and seeded catalogue data.
- `blocked` scenarios and manifest extensions stay visible and make
  `run --all` exit nonzero until their dependency issues land.
- Provider sandbox evidence under [#40](https://github.com/Koopa0/goen/issues/40)
  stays separate; this suite uses local provider doubles and composition tests.
