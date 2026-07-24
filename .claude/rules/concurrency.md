---
paths:
  - "**/*.go"
---

# Concurrency Rules

## Default: Synchronous

Write synchronous functions. Let the CALLER add concurrency. NEVER fire-and-forget goroutines.

## Context Rules

- `context.Context` ALWAYS first parameter. NEVER store in struct. NEVER custom context types.
- In tests: `t.Context()` (Go 1.24+). HTTP handlers: `r.Context()`.
- Context values: unexported struct key (`type requestIDKey struct{}`), cross-cutting only (request ID, auth, trace). NEVER string/int keys.
- NEVER store request body, db conn, config, or logger in context.
- NEVER read auth user in store — handler extracts and passes as function param.

## errgroup as Default

```go
g, ctx := errgroup.WithContext(ctx)
g.Go(func() error { return fetch(ctx, url) })
if err := g.Wait(); err != nil { return err }
```

## Channel Ownership

- Sender closes. NEVER the receiver.
- Buffer size is DERIVED, never guessed: 0 = synchronization point, 1 = mailbox / coalesced signal, N = a named domain bound. NEVER an arbitrary "big enough" number.
- Signal-only channels: `chan struct{}`.

## Concurrency Decision Framework

Before introducing concurrency: **"Why can't this be synchronous?"**

| Situation                                                                 | Primitive                                                    |
| ------------------------------------------------------------------------- | ------------------------------------------------------------ |
| Single I/O                                                                | Synchronous — no concurrency                                 |
| Multiple independent I/O, latency matters                                 | `errgroup`                                                   |
| Single-word independent state (counter, flag, lazily-published pointer)   | `atomic.Int64` / `atomic.Bool` / `atomic.Pointer[T]`         |
| Compound invariant (2+ fields change together, or a read decides a write) | `sync.Mutex` — atomics cannot protect multi-field invariants |
| Read-heavy shared state (>10:1)                                           | `sync.RWMutex`                                               |
| Write-heavy shared state                                                  | `sync.Mutex`                                                 |
| Bounded batch processing                                                  | `errgroup` + `SetLimit()`                                    |
| Background task                                                           | `context` + `wg.Go()` (Go 1.25+)                             |
| Fan-out/fan-in                                                            | `chan` + `wg.Go()` (Go 1.25+)                                |

### Evaluation Checklist (comprehend MUST verify)

- [ ] Synchronous version is measurably too slow (benchmarked)
- [ ] Goroutine lifetime explicitly managed
- [ ] Errors propagated, not dropped
- [ ] Context cancellation respected
- [ ] Shared state access protected
- [ ] No goroutine leak under any path
- [ ] Bounded concurrency (no unbounded `go func()` in loop)

## NEVER

- NEVER `time.Sleep` for synchronization
- NEVER `t.Fatal` from goroutine (see testing.md)
- NEVER goroutine without exit plan
- NEVER `sync.Map` unless profiling proves mutex is bottleneck
- NEVER use atomics to guard a compound invariant — that is a mutex
- NEVER hold mutex during I/O — acquire, copy, release, then I/O
- NEVER unbounded `go func()` in loop — use `errgroup.SetLimit()`
