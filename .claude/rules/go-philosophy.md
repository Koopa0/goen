---
paths:
  - "**/*.go"
---

# Go Philosophy — Project Constraints

## Style Authority — Priority Order

1. **Project rules** (`.claude/rules/*.md`) — always override external guidance
2. **Effective Go** + **Go Code Review Comments**
3. **Go Proverbs** (Rob Pike)
4. **uber-go/guide**
5. **golangci-lint** project config (`.golangci.yml`)

- If a project rule contradicts Effective Go, the project rule wins
- NEVER cite "best practice" without tracing to one of these sources
- NEVER cite Medium articles, blog posts, or Stack Overflow as authority

## Decision Checklist

1. Can the standard library do this? → Use it.
2. Does this type/function already exist? → Use it.
3. Is this abstraction needed TODAY? → If no, don't build it.
4. Does the package name describe what it PROVIDES? → `order` yes, `service` no.
5. Would a new reader understand this in 30 seconds? → If no, simplify.

## Semantic Design

- Interface Questions (in order): (0) Is this a consumer-boundary subset of ANOTHER feature's concrete type? → legitimate discovery (rules/interfaces.md), proceed. (1) Otherwise: how many production impls? 1 → no interface. (2) Delete it — what breaks? Only tests → use testcontainers. (3) Would stdlib define it?
- Adapter Two Questions: (1) Delete it — what breaks? (2) Real constraint or fixable defect?
- Handler Consistency: All mutation handlers (Create/Update/Delete) in same package MUST have identical input validation.

## Dependencies

**Approved**: pgx/v5, go-cmp, testcontainers-go, errgroup, jwt/v5, nats.go, ristretto, genkit, otel.
**Forbidden**: HTTP frameworks (chi/gin/echo), ORMs (gorm/ent), testify, loggers (zap/zerolog), config libs (viper), DI frameworks (wire/dig).

- MUST justify new deps. MUST run `go mod tidy`. MUST NOT use `go get -u` without changelog review.
- MUST NOT use `replace` directives in committed go.mod.

## Concrete Prohibitions

- `MixedCaps` for constants, NEVER `SCREAMING_SNAKE_CASE`. NEVER `K` prefix.
- `any` not `interface{}`. `crypto/rand` not `math/rand` for security.
- No redundant `break` in switch. No Yoda conditions.
- NEVER shadow stdlib package names. Use `urlStr` not `url`.
- `x := value` not `var x = value` inside functions.
- NEVER custom context type. NEVER `panic` for error handling. Recovery ONLY in middleware/main.
- NEVER copy struct with `sync.Mutex` or `bytes.Buffer`.
- NEVER defer in a loop without extracting body to function.
- NEVER `init()` except for codec/driver registration.
- No naked returns in functions >5 lines. ≤5 parameters per function.
- Happy path left-aligned — error checks early return.
- Functional options only for 3+ optional params. NEVER builder pattern.
- Constructor (`New`) MUST validate invariants — `panic` for nil required deps.
- Doc comments: every exported symbol, starting with symbol name.
- `log/slog` only. `snake_case` keys. NEVER log errors you also return. For patterns: `/go-slog` and `/otel-guide` skills.
- NEVER `unsafe` or `cgo` without all-reviewer approval. NEVER `reflect` except `StructTag` and `DeepEqual`.
- Prefer `slices`, `maps`, `cmp.Or` over hand-written loops.
- NEVER `os.Getenv` outside `cmd/app/main.go` — pass config as struct fields.
