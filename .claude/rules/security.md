---
paths:
  - "**/*handler*.go"
  - "**/*auth*.go"
  - "**/*middleware*.go"
  - "**/*server*.go"
  - "**/*store*.go"
  - "**/*.sql"
  - "**/cmd/**/main.go"
---

# Security Rules

## SQL Injection Prevention (CRITICAL)

- NEVER build SQL with string concatenation or `fmt.Sprintf`
- MUST use parameterized queries (`$1`, `$2`) or sqlc
- MUST allowlist dynamic ORDER BY / LIMIT values, NEVER use user input directly

## Secrets Management (CRITICAL)

- NEVER hardcode secrets, API keys, passwords, or tokens in source
- NEVER commit `.env` files
- MUST load secrets from `os.Getenv()`
- NEVER log secrets — audit all `slog.*` calls
- NEVER return secrets in error messages

## Cryptographic Randomness

- MUST use `crypto/rand` for security-sensitive values (tokens, IDs)
- NEVER use `math/rand` for anything security-related

## Input Validation

- MUST validate ALL external input at HTTP handler boundary
- MUST validate path parameters before use
- MUST validate and bound query parameters (e.g., limit 1-100)
- MUST validate integer conversions for both errors AND bounds
- MUST reject control characters in string inputs: ASCII 0x00-0x1F, 0x7F, Unicode C1 0x80-0x9F
- MUST URL-decode before path traversal checks (handle double-encoding: `%252e%252e/`)
- MUST use `url.Parse` for URL validation, NEVER `strings.HasPrefix` for URL allowlists
- MUST block private/internal IPs in outbound HTTP requests (SSRF): 127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.169.254, ::1

## Path Traversal Prevention

- MUST validate user input before `filepath.Join`
- MUST reject absolute paths and `..` in user input
- MUST verify result stays within allowed directory

## Command Injection Prevention (CRITICAL)

- NEVER pass user input to `exec.Command` through shell (`sh -c`)
- MUST pass arguments directly to `exec.Command`, not through shell
- PREFER avoiding `exec` entirely when possible

## HTTP Security Headers

- MUST set `Content-Type: application/json` for JSON responses
- MUST set `X-Content-Type-Options: nosniff`

## Request Size Limits

- MUST use `http.MaxBytesReader` to limit request body size
- MUST set `MaxHeaderBytes` on `http.Server`

## Timeout Configuration

- MUST set `ReadTimeout`, `WriteTimeout`, `IdleTimeout` on `http.Server`
- Prevents slowloris and resource exhaustion attacks

## CSRF Protection (Go 1.25+)

- MUST use `http.NewCrossOriginProtection().Handler(mux)` for state-changing endpoints (POST, PUT, DELETE)
- Replaces custom CSRF token middleware for browsers that send Fetch metadata headers

## CORS Configuration

- NEVER use `*` as allowed origin in production
- MUST load allowed origins from configuration
- CORS middleware MUST sit before Auth in middleware chain

## Authentication

- MUST use bcrypt or argon2 for password hashing, NEVER MD5/SHA
- MUST use constant-time comparison (`subtle.ConstantTimeCompare`) for tokens
- MUST perform auth checks BEFORE business logic, not after

## Rate Limiting

- SHOULD implement rate limiting on auth endpoints
- SHOULD implement account lockout after failed attempts

## Error Information Leakage

- 5xx errors MUST return generic message, NEVER expose internals
- MUST log detailed errors server-side with `slog.Error`
- NEVER return stack traces, SQL errors, or file paths to clients

## Race Conditions

- MUST run tests with `-race` flag in CI: `go test -race ./...`
- MUST protect shared state with `sync.Mutex`
- MUST handle check-then-act patterns atomically (single lock hold — never release between check and act)
- MUST handle `ErrConflict` with retry for create-after-read patterns (TOCTOU)

## Go-Specific Security Pitfalls

- `time.Duration` is `int64` nanoseconds — overflows at ~292 years. Use `time.Time.Before()`/`After()` for far timestamps, not duration subtraction.
- `url.Parse` does NOT reject all dangerous URLs — always check `parsed.Hostname()`, `parsed.User`, and `parsed.Scheme` explicitly after parsing.
- `strings.HasPrefix` is NOT safe for URL allowlists — attacker uses userinfo: `http://allowed@evil.com/`
- `json.RawMessage` stores raw bytes without validation — call `json.Valid` before persisting to JSONB.
- HMAC-based state tokens need nonce + bidirectional expiry (reject future timestamps, not just past).

## Severity Classification

| Severity | Examples                                              | Action           |
| -------- | ----------------------------------------------------- | ---------------- |
| CRITICAL | SQL injection, hardcoded secrets, command injection   | Block PR         |
| HIGH     | Missing input validation, race condition, weak crypto | Fix before merge |
| MEDIUM   | Missing security headers, no rate limiting            | Should fix       |
| LOW      | Verbose logging, missing audit trail                  | Track in backlog |

## Reference

For OWASP Top 10 details and detection patterns, use `security-reviewer` agent.
