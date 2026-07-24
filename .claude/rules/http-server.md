---
paths:
  - "**/*handler*.go"
  - "**/*server*.go"
  - "**/*middleware*.go"
  - "**/*route*.go"
  - "**/cmd/**/main.go"
---

# HTTP Server Rules

## Framework Constraint

- MUST use `net/http` with Go 1.22+ routing
- NEVER use chi, gin, echo, fiber, or any HTTP framework

## Handler Constraints

- MUST use closure-based or struct-based DI pattern
- NEVER put business logic in handlers — handlers parse, call, encode only
- NEVER put SQL in handlers
- NEVER return HTML unless building a web UI

## Path Parameters

- MUST use `r.PathValue("id")` for path parameters
- MUST validate path parameters before use

## Response Constraints

- MUST set `Content-Type: application/json` for JSON responses
- MUST set `X-Content-Type-Options: nosniff`
- SHOULD check or explicitly discard `w.Write` return value: `_, _ = w.Write(...)` — `.golangci.yml` excludes this; the error is always broken pipe, handling adds noise

## Error Response Constraints

- 4xx errors: MUST be specific and actionable
- 5xx errors: MUST be generic "internal error", NEVER expose `err.Error()`
- 5xx errors: MUST log the real error with `slog.Error`

## Middleware Constraints

- MUST order middleware: Recovery → RequestID → CORS → Logging → Auth → Handler
- Recovery middleware is ONLY allowed in HTTP layer, NEVER in business logic
- MUST generate request IDs with `crypto/rand`, NEVER `math/rand`

## Server Configuration

- MUST set `ReadTimeout`, `WriteTimeout`, `IdleTimeout` on `http.Server`
- MUST implement graceful shutdown with `srv.Shutdown(ctx)`

## Timeout and Cancellation

- Request context (`r.Context()`) is cancelled when client disconnects — MUST propagate to store calls
- For long-running handlers, SHOULD use `context.WithTimeout(r.Context(), duration)`
- MUST check `ctx.Err()` or `ctx.Done()` before expensive operations
- On context cancellation, return early — do not complete partial work

## Outgoing HTTP Calls

- MUST reuse a single `*http.Client` (create at startup, not per-request)
- MUST set `http.Client{Timeout: ...}` (e.g., 10s-30s depending on downstream)
- MUST pass request context: `http.NewRequestWithContext(ctx, ...)`
- NEVER use `http.Get`, `http.Post`, or `http.DefaultClient` — no timeout by default

## Health Endpoints

- MUST have `/healthz` (liveness) and `/readyz` (readiness)
- Health endpoints MUST NOT go through auth middleware
- NEVER expose detailed dependency status to external callers

## Middleware Location

- General middleware (logging, recovery, request ID) → `cmd/app/`
- Feature-specific middleware (auth) → `internal/<feature>/`

## Reference

For implementation patterns and code examples, use `/http-server` skill.
