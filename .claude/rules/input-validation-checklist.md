---
paths:
  - "**/*handler*.go"
  - "**/*middleware*.go"
  - "**/*auth*.go"
  - "**/*oauth*.go"
  - "**/*redirect*.go"
  - "**/*webhook*.go"
---

# Input Validation Checklist

Replaces "validate ALL input" with specific, testable rules. Every handler MUST follow these.

## URL / Redirect Validation

- MUST use `url.Parse` to validate URLs, NEVER `strings.HasPrefix` for URL allowlists
  - `strings.HasPrefix("http://allowed.com@evil.com/", "http://allowed.com")` returns true — attacker bypasses via userinfo
- MUST reject non-http(s) schemes: block `javascript:`, `data:`, `ftp:`, `file:`, `vbscript:`
- MUST reject URLs where `parsed.User != nil` (blocks `http://localhost@evil.com/`)
- MUST check `parsed.Hostname()` against allowlist, not the raw URL string
- MUST block private/internal IPs in any outbound HTTP fetch (SSRF prevention):
  - `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
  - `169.254.169.254` (cloud metadata), `0.0.0.0`, `::1`, `fd00::/8`
  - `metadata.google.internal`, `metadata.internal`

## Path Validation

- MUST URL-decode BEFORE path traversal checks (handle double-encoding: `%252e%252e/` → `%2e%2e/` → `../`)
- MUST apply `path.Clean` AFTER decoding
- MUST reject any path containing `..` as substring after all decoding + Clean
- MUST verify final resolved path stays within allowed base directory (`strings.HasPrefix(resolved, base)`)
- NEVER rely on a single prefix check — always Clean + resolve + recheck

## String Validation

- MUST reject control characters in user-provided strings:
  - ASCII: 0x00-0x1F (C0 controls), 0x7F (DEL)
  - Unicode: 0x80-0x9F (C1 controls — invisible, used for bypass attacks)
- MUST validate at handler boundary, not in store layer
- MUST validate enum values at handler layer — never rely on DB ENUM/CHECK constraint error messages (user gets 500 instead of 400)

## Handler Validation Consistency

- All mutation handlers (Create, Update, Delete) in the same package MUST have identical validation rules
- If Create validates field X, Update MUST also validate field X — inconsistency is a bug
- When adding validation to any mutation handler, check ALL sibling mutation handlers in the same package

## Concurrency Safety (check-then-act)

- Read → decide → write patterns MUST handle `ErrConflict` with retry or use atomic operations
- Bounded resources (maxClients, rate limiter entries) MUST use atomic check-and-insert under a single lock hold
- NEVER release lock between capacity check and insert — this is a TOCTOU race
- Pattern to watch: `mu.Lock(); count := len(m); mu.Unlock(); /* gap */ mu.Lock(); m[k] = v; mu.Unlock()`

## Integer / Duration Safety

- `time.Duration` is `int64` nanoseconds — overflows at ~292 years
- For timestamp comparison over long ranges, use `time.Time.Before()`/`After()`, NOT duration subtraction
- MUST validate `strconv.Atoi` / `strconv.ParseInt` for BOTH parse errors AND bounds
- MUST bound pagination parameters (limit: 1-100, offset: >= 0)
