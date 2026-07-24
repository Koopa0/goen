---
paths:
  - "**/*.go"
---

# Naming Rules

## Package Names

- Lowercase, single-word: `order`, `auth`
- Constructor: `order.New`, not `order.NewOrder`

## Stutter (NEVER)

```go
// WRONG                     // CORRECT
order.OrderStatus            order.Status
order.OrderStore             order.Store
order.NewOrderHandler        order.NewHandler
```

## Get Prefix (NEVER on Getters)

```go
// WRONG                     // CORRECT
func (s *Store) GetOrder()   func (s *Store) Order()
func (u *User) GetName()     func (u *User) Name()
```

For expensive operations, use a verb: `Compute`, `Fetch`, `List`, `Find`.

## Receiver Names

1-2 letters, abbreviation of type. NEVER `this` or `self`.

## Interface Names

- One-method: method + `-er`. Multi-method: descriptive noun.
- NEVER prefix with `I`. NEVER suffix with `Impl`.

## Initialisms

All caps: `HTTPServer`, `serverID`, `URL`. Never `HttpServer`.

## Error Names

- Types: `Error` suffix — `ValidationError`
- Variables: `Err` prefix — `ErrNotFound`

## Store Method Naming

| Pattern                        | Meaning                         | Example                        |
| ------------------------------ | ------------------------------- | ------------------------------ |
| `Order(ctx, id)`               | Single by primary key           | `s.Order(ctx, "abc")`          |
| `OrderByEmail(ctx, email)`     | Single by alternate key         | `s.OrderByEmail(ctx, "a@b.c")` |
| `Orders(ctx, filter)`          | List (returns slice, never nil) | `s.Orders(ctx, f)`             |
| `CreateOrder(ctx, params)`     | Insert                          | `s.CreateOrder(ctx, p)`        |
| `UpdateOrder(ctx, id, params)` | Modify                          | `s.UpdateOrder(ctx, id, p)`    |
| `DeleteOrder(ctx, id)`         | Remove                          | `s.DeleteOrder(ctx, id)`       |

- NEVER `Get`, `Find`, `Fetch`, `Retrieve` for store methods.
