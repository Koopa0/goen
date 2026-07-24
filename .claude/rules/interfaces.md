---
paths:
  - "**/*.go"
---

# Interface Rules

Interfaces are DISCOVERED, not designed. An interface is legitimate only at
the moment a real dependency reveals it — never before.

## Consumer Defines Interface

The CONSUMER defines what it needs. The PRODUCER returns concrete.
Cross-feature example — `internal/notification` depends on a subset of
`internal/order`'s concrete store:

```go
// Consumer (internal/notification/handler.go)
type OrderReader interface {
    Order(ctx context.Context, id string) (*order.Order, error)
}

// Producer (internal/order/store.go) — returns *Store, NOT OrderReader
func NewStore(dbtx db.DBTX) *Store { return &Store{dbtx: dbtx} }
```

## The Only Two Discovery Cases

An interface may exist when — and only when — one of these is true:

1. A second PRODUCTION implementation already exists.
2. A consumer in ANOTHER package depends on a subset of an existing
   concrete type (the cross-feature boundary above).

- Tests are NEVER a reason to introduce an interface. A test that "needs"
  an interface needs an integration test (testcontainers). For
  non-containerizable externals only: a hand-written fake implementing an
  EXISTING consumer interface (rules/testing.md § Test Doubles).
- NEVER define an interface for "future flexibility". The second
  implementation defines the interface when it actually arrives.

## Core Rules

- NEVER return an interface from a function. Return concrete types.
  (Mechanized by the `iface` linter's `opaque` check; rare factory
  exceptions need `//nolint:iface // reason`.)
- 1-3 methods max. Split larger interfaces.
- Compile-time verify: `var _ OrderReader = (*Store)(nil)`

## Embedding Pitfall

NEVER embed an interface in a struct — zero value panics. Use explicit fields.
