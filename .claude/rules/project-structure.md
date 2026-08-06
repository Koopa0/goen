# Project Structure Rules

## Layout

```
cmd/app/main.go       → Wiring ONLY: parse config, create deps, register routes, start server
internal/<feature>/   → ALL application code, by feature
migrations/           → Numbered SQL: 001_desc.up.sql / 001_desc.down.sql
prompts/<feature>/    → Genkit dotprompt files (*.prompt)
sqlc.yaml             → sqlc configuration
```

## cmd/ Rules

`main.go` does:

1. Parse configuration from environment
2. Create dependencies (pool, logger)
3. Wire handlers to routes
4. Start server with graceful shutdown

NOTHING ELSE. No types (except config struct). No business logic. No tests.

## internal/ Rules

- ALL application code lives here
- One directory per feature: `internal/order/`, `internal/user/`
- Each feature is self-contained: types + handlers + store + tests
- Features MAY import each other's domain types (structs in `<feature>.go`), but MUST use consumer-defined interfaces for store/handler operations — never import another feature's `*Store` or handler directly

## Forbidden Directories

The PreToolUse hook enforces these. Files in these directories WILL BE BLOCKED.

See `.claude/hooks/check-anti-patterns.sh` for the authoritative list (30+ blocked directory names including services, repository, models, domain, util, etc.).

## File Naming Within a Feature

| File                  | Contains                                                                                         |
| --------------------- | ------------------------------------------------------------------------------------------------ |
| `<feature>.go`        | Types, constants, sentinel errors                                                                |
| `handler.go`          | HTTP handler closure functions                                                                   |
| `store.go`            | Database operations (pgx)                                                                        |
| `query.sql`           | sqlc query definitions                                                                           |
| `flow.go`             | Genkit AI flow definitions (if feature uses AI)                                                  |
| `tool.go`             | Genkit tool definitions (if feature uses AI)                                                     |
| `<feature>_test.go`   | Unit/handler/fuzz/bench tests (package `<feature>`)                                              |
| `integration_test.go` | Integration tests — exactly one per feature (`//go:build integration`, package `<feature>_test`) |

## When to Create a New Package

- The code serves a distinct feature/domain
- It has its own types, storage, and/or handlers
- It would have 3+ files

If a "package" is one file, merge it into the consuming package.

## Generated Code

- `internal/db/` contains sqlc-generated code — NEVER edit by hand
- MUST run `sqlc generate` after any `.sql` or migration change
- MUST run `go build ./...` after generation to verify
- All tools (linter, reviewer, hooks) MUST skip files with `// Code generated` header
- `//go:generate` directives MUST be documented with purpose
