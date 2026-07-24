# Package Organization (Google Style Guide + Project-Specific)

## Package-by-Feature Structure

Organize by what the code DOES, not what pattern it follows:

```
internal/
  order/                  ← feature: all order-related code
    order.go              ← types, sentinel errors
    handler.go            ← HTTP handlers
    store.go              ← database operations
    query.sql             ← sqlc queries
    order_test.go         ← unit/handler/fuzz/bench tests
    integration_test.go   ← integration tests (//go:build integration)
  user/
    user.go
    handler.go
    store.go
    user_test.go
```

## Forbidden Package Names (Hard Block via Hook)

The PreToolUse hook will BLOCK file creation in these directories:

`services`, `service`, `repositories`, `repository`, `handlers`, `handler`,
`controllers`, `controller`, `models`, `model`, `entities`, `entity`,
`dto`, `dtos`, `mappers`, `mapper`, `factory`, `factories`, `domain`,
`infrastructure`, `application`, `presentation`, `util`, `utils`,
`helper`, `helpers`, `common`, `shared`, `base`, `types`, `src`, `pkg`

## Generic Package Names Forbidden (Google Style Guide)

"Avoid generic package names like `util`, `helper`, `common`."

If you have shared code, name the package by what it provides:

```go
// WRONG
import "github.com/koopa0/go-spec/internal/util"
util.FormatTime(t)

// CORRECT
import "github.com/koopa0/go-spec/internal/timeutil"
timeutil.Format(t)
```

## Package Name in Exported Identifiers (Effective Go)

Callers always type `pkg.Name`. Don't stutter:

```go
// WRONG
order.NewOrderStore    // caller sees: order.NewOrderStore
order.OrderStatus      // caller sees: order.OrderStatus

// CORRECT
order.NewStore         // caller sees: order.NewStore
order.Status           // caller sees: order.Status
```

## Constructor Convention (Google Style Guide)

Single constructor: `New`. Multiple constructors: `NewX`:

```go
order.New(pool)                  // single constructor
order.NewFromCSV(reader)         // variant constructor
```

NEVER `order.NewOrder(pool)`.

## Single-File Packages

If a package has only one `.go` file (excluding tests), it should probably not be a separate package. Merge it into the parent or consuming package.

## Cross-Feature Dependencies

Features import each other only through interfaces defined by the consumer:

```go
// internal/notification/handler.go — consumer defines interface
type OrderReader interface {
    Order(ctx context.Context, id string) (*order.Order, error)
}
```

## Import Grouping (Google Style Guide)

Three groups, separated by blank lines:

```go
import (
    "context"
    "fmt"
    "net/http"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/google/go-cmp/cmp"

    "github.com/koopa0/go-spec/internal/order"
)
```

1. Standard library
2. Third-party
3. Local project (this module)

## Dot Imports Forbidden (Google Style Guide)

NEVER use `import . "package"`. Makes code origin unclear.
