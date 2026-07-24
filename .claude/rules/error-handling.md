---
paths:
  - "**/*.go"
---

# Error Handling Rules

## Error Strings

Lowercase, no punctuation, context at end: `fmt.Errorf("querying order %s: %w", id, err)`

## Wrapping

- `%w` for internal code (preserves `errors.Is`/`errors.As` chain)
- `%v` at system boundaries (prevents leaking internal types)

## Handle Exactly Once

Either return OR log. NEVER both.

## Add Non-Redundant Context

Context describes WHAT was happening + input, not the function name.

## errors.AsType (Go 1.26+)

```go
if pathErr, ok := errors.AsType[*os.PathError](err); ok { ... }
```

Prefer over `errors.As` — type-safe, no pointer-to-pointer pattern.

## Enum Switch Defaults — Must Panic

Go has no exhaustiveness check for `switch` over a closed set of constants (Go 1.26 still does not provide one). A missing case silently falls through to `default`, which is a **programming error** — not a runtime condition the caller can handle.

For mapping-style switches where adding a new enum value REQUIRES updating the switch (e.g., `OrderStatus` → HTTP code, `PaymentMethod` → processor, `Role` → permission set), the `default` branch MUST `panic` with the offending value:

```go
// ✅ CORRECT — panic surfaces the missing case the first time the switch
// is exercised (test, smoke run, or first prod call), before the bug
// produces silently wrong output downstream.
func httpCodeFor(s OrderStatus) int {
    switch s {
    case StatusPending:
        return http.StatusAccepted
    case StatusConfirmed:
        return http.StatusOK
    case StatusCancelled:
        return http.StatusGone
    default:
        panic("order: unknown OrderStatus: " + string(s))
    }
}
```

NEVER return a zero value, `nil`, or a silently-incomplete fallback:

```go
// ❌ WRONG — silent fallback. A future status value ships without the
// correct HTTP code and the bug only surfaces in client behavior.
default:
    return http.StatusInternalServerError
```

This is one of the few cases where `panic` is the correct choice, because the "error" is a programmer who forgot to update the switch, not a runtime condition.

## Sentinel Error Design

Define in `<feature>.go`. Each sentinel = a distinct **caller decision**:

| Error             | When                       | HTTP |
| ----------------- | -------------------------- | ---- |
| `ErrNotFound`     | Record doesn't exist       | 404  |
| `ErrConflict`     | Unique constraint violated | 409  |
| `ErrForbidden`    | Lacks permission           | 403  |
| `ErrInvalidInput` | Business validation fails  | 422  |

- Only define sentinels the **handler branches on**
- Map pgx errors in store: `pgx.ErrNoRows`→`ErrNotFound`, unique violation (23505)→`ErrConflict`
- NEVER create sentinels for infrastructure errors (timeouts, connection failures)

## NEVER

- NEVER ignore errors without `// best-effort` comment
- NEVER `strings.Contains(err.Error(), ...)`
- NEVER `log.Fatal` outside `main()` startup helpers
- NEVER `panic` for normal error handling (enum switch defaults are the documented exception above)
