---
paths:
  - "**/*.sql"
  - "**/*migration*"
  - "**/*store*.go"
  - "**/*query*"
---

# Schema Design Rules

Every column, every type, every constraint must be intentional. Ambiguous schema destroys codebase understanding.

## Column Documentation (Non-Negotiable)

- Every column MUST have a `COMMENT ON COLUMN` in the migration
- The comment MUST explain the **semantic meaning**, not restate the type
- If you cannot explain why a column exists, it should not exist

```sql
-- WRONG: restates type
COMMENT ON COLUMN orders.total IS 'integer total';

-- WRONG: no comment at all
CREATE TABLE orders (total INT);

-- CORRECT: explains meaning, unit, and constraints
COMMENT ON COLUMN orders.total_cents IS 'Order total in cents (USD). Always positive. Use cents to avoid floating point.';
COMMENT ON COLUMN orders.status IS 'Lifecycle state: pending → confirmed → shipped → delivered → cancelled. Set by handler, never by trigger.';
COMMENT ON COLUMN orders.user_id IS 'References users(id). The customer who placed this order. NOT NULL — every order must have an owner.';
```

## Type Selection (Every Choice Needs Justification)

| Decision                     | Question to Answer                                                   |
| ---------------------------- | -------------------------------------------------------------------- |
| `UUID` vs `BIGSERIAL`        | Distributed generation needed? URL-safe? Predictability concern?     |
| `TEXT` vs `VARCHAR(n)`       | Is there a real domain maximum? If not, use TEXT                     |
| `INT` vs `BIGINT`            | Will values exceed 2^31? Use BIGINT for IDs and counters             |
| `NUMERIC(p,s)` vs `INT`      | Is there a fractional part? Money → cents as INT or NUMERIC          |
| `TIMESTAMPTZ` vs `TIMESTAMP` | Always use TIMESTAMPTZ unless you have a documented reason not to    |
| `BOOLEAN` vs status column   | Only two states ever? Then BOOLEAN. Otherwise, TEXT with CHECK       |
| `JSONB`                      | Only for truly dynamic/schemaless data. NEVER to avoid schema design |
| `ARRAY`                      | Only for small, atomic sets. NEVER for relationships                 |

## Nullable Rules

- MUST default to `NOT NULL` — nullable is the exception
- Every nullable column MUST have a comment explaining when/why it is null
- Nullable means "absence of value is semantically meaningful"
- If a column has a sensible default, use `DEFAULT` instead of nullable

```sql
-- WRONG: nullable without reason
shipped_at TIMESTAMPTZ,

-- CORRECT: nullable with documented semantics
shipped_at TIMESTAMPTZ,  -- NULL until order ships. Set by shipOrder handler.
```

## Constraint Rules

See: database.md for schema constraints (NOT NULL, FK, CHECK, DEFAULT) and migration constraints.

## Enum/Status Pattern

- MUST use `TEXT` column with `CHECK` constraint
- MUST define all valid values in the CHECK
- MUST document the state machine (transitions) in the column comment

```sql
status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'confirmed', 'shipped', 'delivered', 'cancelled')),

COMMENT ON COLUMN orders.status IS 'Lifecycle: pending → confirmed → shipped → delivered. Can be cancelled from pending or confirmed only.';
```

## Money Pattern

- MUST store money as integer cents (`total_cents INT NOT NULL CHECK (total_cents >= 0)`)
- Or use `NUMERIC(10,2)` if fractional precision is required
- NEVER use `FLOAT` or `DOUBLE PRECISION` for money
- MUST document the currency in the column comment

## Go Struct ↔ Schema Alignment

- Every Go struct field MUST match its SQL column type precisely
- MUST use sqlc overrides for type mapping when defaults are insufficient
- Non-nullable SQL → non-pointer Go type
- Nullable SQL → pointer Go type or `pgtype.*`

| SQL Type        | Go Type                           | Notes               |
| --------------- | --------------------------------- | ------------------- |
| `UUID`          | `string`                          | Via sqlc override   |
| `TIMESTAMPTZ`   | `time.Time`                       | Via sqlc override   |
| `TEXT NOT NULL` | `string`                          | Direct              |
| `TEXT NULL`     | `*string` or `pgtype.Text`        | Must handle nil     |
| `INT NOT NULL`  | `int32` or `int64`                | Match SQL precision |
| `NUMERIC(10,2)` | `pgtype.Numeric`                  | Never float64       |
| `BOOLEAN`       | `bool`                            | Direct              |
| `JSONB`         | `json.RawMessage` or typed struct | Prefer typed        |

## Review Checklist (db-reviewer MUST verify)

For every migration or schema change:

- [ ] Every column has a `COMMENT ON COLUMN`
- [ ] Every type choice is justified (not just default)
- [ ] Nullable columns have documented semantics
- [ ] All references have foreign keys
- [ ] All enums have CHECK constraints
- [ ] Money uses integer cents or NUMERIC, never float
- [ ] Timestamps use TIMESTAMPTZ
- [ ] Go types in store.go match SQL types precisely
- [ ] Index exists for every WHERE clause column in queries
- [ ] 3NF is achieved (no transitive dependencies)
