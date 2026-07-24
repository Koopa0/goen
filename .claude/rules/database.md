---
paths:
  - "**/*store*.go"
  - "**/*.sql"
  - "**/sqlc*"
  - "**/*migration*"
  - "**/*query*"
---

# Database Rules

## Technology Stack (Non-Negotiable)

| Component  | MUST Use                            | NEVER Use                                 |
| ---------- | ----------------------------------- | ----------------------------------------- |
| Driver     | pgx/v5 (`pgxpool.Pool`)             | database/sql                              |
| Queries    | sqlc (generated code)               | raw SQL strings in Go, ORM (gorm/ent/bun) |
| Testing    | testcontainers-go (real PostgreSQL) | mocks, sqlmock, in-memory DB              |
| Migrations | numbered SQL files                  | ORM migrations, Go-based migrations       |

## Store Constraints

- Store constructor MUST accept `db.DBTX`, NEVER `*pgxpool.Pool` directly
- Store MUST provide `WithTx(tx pgx.Tx) *Store` for transaction support
- Store MUST map `pgx.ErrNoRows` to feature-specific `ErrNotFound`
- Store MUST NOT expose `db.*` generated types — convert to feature types
- NEVER store `*pgxpool.Pool` inside a Store struct

## sqlc Constraints

- ALL queries MUST be in `.sql` files with sqlc annotations
- MUST run `sqlc generate` after any `.sql` or migration change
- MUST run `go build ./...` after `sqlc generate`
- NEVER modify files in generated `internal/db/` by hand
- MUST use `emit_empty_slices: true` in sqlc.yaml

## Transaction Constraints

- Transaction boundary MUST be controlled by handler, not store
- MUST use `defer tx.Rollback(ctx)` pattern — no-op after commit
- NEVER rely on implicit transaction auto-commit for multi-step operations
- Most handlers do NOT need explicit transactions — single INSERT/SELECT auto-commits

## Migration Constraints

- Migration files: `NNN_description.up.sql` and `NNN_description.down.sql`
- MUST be reversible — every `.up.sql` has a `.down.sql`
- NEVER use `DROP COLUMN` without data backup strategy
- NEVER modify existing migrations — create new ones

## Schema Constraints

- MUST achieve Third Normal Form (3NF) unless documented exception
- MUST use `gen_random_uuid()` for UUID generation in database
- MUST set `created_at` with `DEFAULT now()` in CREATE TABLE
- MUST set `updated_at` explicitly in UPDATE queries, NEVER via triggers
- MUST use foreign key constraints for referential integrity
- MUST use CHECK constraints for enum validation
- NEVER use JSONB to avoid proper schema design
- NEVER use triggers for business logic

## Timeout and Cancellation

- MUST configure pool-level timeouts in `pgxpool.Config`:
  - `ConnConfig.ConnectTimeout` — connection establishment (e.g., 5s)
  - `MaxConnIdleTime` — idle connection cleanup (e.g., 30m)
  - `MaxConnLifetime` — connection recycling (e.g., 1h)
- MUST pass request context to all store methods — enables per-request cancellation
- SHOULD use `context.WithTimeout` for long-running queries (e.g., reports)
- Store methods MUST respect `ctx.Done()` — never ignore cancellation
- NEVER use `context.Background()` in store methods — always propagate request context

## Error Mapping

- `pgx.ErrNoRows` → `ErrNotFound`
- `context.DeadlineExceeded` → `ErrTimeout` or appropriate 504/408 response
- PostgreSQL error code `23505` (unique_violation) → `ErrConflict`
- PostgreSQL error code `23503` (foreign_key_violation) → descriptive error

## Reference

For implementation patterns:

- pgx patterns: `/pgx-patterns` skill
- sqlc patterns: `/sqlc-guide` skill
- PostgreSQL schema: `/postgres-patterns` skill
- Migrations: `/migrations` skill
