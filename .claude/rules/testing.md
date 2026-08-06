---
paths:
  - "**/*_test.go"
---

# Testing Rules

## No Testify — Use go-cmp

```go
// FORBIDDEN
assert.Equal(t, want, got)

// REQUIRED
if diff := cmp.Diff(want, got); diff != "" {
    t.Errorf("Order() mismatch (-want +got):\n%s", diff)
}
```

NEVER compare field-by-field. Construct expected struct, compare with `cmp.Diff`.

## Failure Messages

`FuncName(input) = got, want expected`. Use `%q` for strings. Got before want.

## Table-Driven Tests (Mandatory for 2+ Cases)

```go
tests := []struct {
    name    string
    input   string
    want    Status
    wantErr bool
}{
    {name: "valid", input: "pending", want: StatusPending},
    {name: "empty", input: "", wantErr: true},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := ParseStatus(tt.input)
        // ...cmp.Diff...
    })
}
```

ALWAYS use field names in struct literals. NEVER positional.

## t.Fatal from Goroutine — NEVER

`t.Fatal` calls `runtime.Goexit` on the wrong goroutine — undefined behavior:

```go
// WRONG — undefined behavior
go func() { t.Fatal("failed") }()

// CORRECT — safe from goroutines
go func() { t.Error("failed") }()
```

## t.Helper() in Test Helpers

Any function calling `t.Fatal`/`t.Error` MUST call `t.Helper()` first.

## Benchmarks (Go 1.24+)

Use `b.Loop()`, NEVER `for i := 0; i < b.N; i++`:

```go
func BenchmarkParseStatus(b *testing.B) {
    for b.Loop() { ParseStatus("pending") }
}
```

### Benchmark Discipline

A noisy local machine (browsers, IDE, laptop thermal throttling) skews
results — single runs are NOT comparable.

- Performance claims MUST cite `benchstat` output over `-count=10` runs of
  BOTH old and new code: `make bench-baseline` → apply change → `make bench-compare`.
- `~` or p ≥ 0.05 from benchstat = no proven difference. NEVER report it as a win.
- NEVER compare numbers across machines, Go versions, sessions, or thermal states.
- Shared CI runners: benchmarks are smoke tests only — never gate on absolute numbers.

## HTTP Handler Tests

```go
handler := getOrder(store, slog.Default())
req := httptest.NewRequest("GET", "/orders/abc", nil)
req.SetPathValue("id", "abc")  // Go 1.22+
w := httptest.NewRecorder()
handler.ServeHTTP(w, req)
if w.Code != http.StatusOK {
    t.Fatalf("getOrder(%q) status = %d, want %d", "abc", w.Code, http.StatusOK)
}
```

- Use `SetPathValue` for path params — NEVER parse URLs manually
- Test both success and error paths
- POST: set `Content-Type: application/json`, use `strings.NewReader` for body

## Integration Tests

- Exactly ONE `integration_test.go` per feature package. First line: `//go:build integration`.
- testcontainers-go with real PostgreSQL/NATS. NEVER mock the database or the broker.
- `t.Cleanup()` for teardown. `t.Parallel()` for independent tests.

## Test Packages

- Unit/handler/fuzz/bench tests: `package <feature>` in `<feature>_test.go`
  (white-box — handlers are unexported closures).
- `integration_test.go`: `package <feature>_test` (black-box, through the public surface).

## Test Doubles — Real First, Fakes Last, Mocks Never

1. **Real first**: PostgreSQL/NATS → testcontainers-go. HTTP dependencies → `httptest.Server`.
2. **Fakes last**: a hand-written fake is allowed ONLY for a
   non-containerizable external (paid API, third-party SaaS). It implements
   an EXISTING consumer interface, lives in the same file as the test using
   it, is a plain struct (5-15 lines), and is asserted on OUTPUTS only —
   NEVER call order or call counts (see Low-Value Tests #5).
3. **Mocks never**: mock frameworks are forbidden — depguard blocks the
   imports, check-anti-patterns.sh blocks the codegen.

Tests are never a reason to introduce an interface — a test that "needs"
an interface needs an integration test (rules/interfaces.md).

## Low-Value Tests — NEVER

Every test must be able to catch a real implementation bug. The eight ways
AI generates tests that cannot:

1. NEVER assert a value the test itself constructed and passed through
   (want and got from the same expression — a tautology).
2. NEVER compute the expected value by calling the function under test or
   re-implementing its logic inline — expected values are hand-computed literals.
3. NEVER mirror the implementation structure line-by-line — test the
   observable contract: input → output / error / DB rows / HTTP response.
4. NEVER commit a test that cannot fail. For each test, name the
   implementation bug it would catch (mental mutation testing). Can't name
   one → delete the test.
5. NEVER write mock-verification-only tests (asserting a dependency was
   called N times with given args, with no output or state assertion).
6. NEVER write coverage-padding tests (getters/setters, struct-literal
   field echo, asserting only `err == nil`).
7. NEVER assert unexported internal state when an observable contract exists.
8. NEVER bless current behavior as the spec without derivation — golden
   file `-update` requires reviewing `git diff testdata/` and stating why
   the new output is correct; NEVER regenerate just to go green.

### Rationalization | Reality

These rules are violated by an agent rationalizing under pressure. Pre-empt the excuse:

| The rationalization                                        | The reality                                                                                                                                                                    |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| "More tests = safer / better coverage."                    | A test that cannot fail adds maintenance cost and _false_ confidence. Coverage from tautologies is fake green (#4, #6).                                                        |
| "Asserting it returns what I passed in proves it works."   | Tautology — it still passes if the body is `return input` and the real logic is broken (#1).                                                                                   |
| "I'll compute the expected value by calling the function." | Then the test asserts the function equals itself and can never fail (#2). Expected values are hand-computed literals.                                                          |
| "Verifying the mock was called proves the integration."    | It proves a call happened, not that the result is correct. Use a real dependency (testcontainers) and assert state/output (#5).                                                |
| "It's hard to hand-compute the expected output."           | If you can't state the expected output, you don't understand the contract well enough to test it — that's the signal to stop and think, not to mirror the implementation (#3). |
| "Golden file changed — just regenerate to go green."       | Regenerating without reviewing `git diff testdata/` blesses a possibly-wrong output as the spec (#8).                                                                          |
| "Re-run the suite once more to be sure."                   | After a clean run on unchanged code, re-running asserts nothing new. Stop.                                                                                                     |

## Locks Are Proven by Mutation

A test that gates a guarantee (wire format, security invariant, hygiene sweep, drift check) is a lock.
A lock that has never been red is a hope, not a lock.

- A new test is not done until it has been WATCHED failing for the intended reason. The TDD red phase
  counts; for a post-hoc test, plant the defect it exists to catch, watch red, revert.
- The mutation MUST compile — `[build failed]` is not a red test. It proves the symbol is referenced,
  not that the behavior is asserted. Change behavior, not the symbol table.
- The mutation MUST provably apply — confirm the target changed before the run. A pattern that rewrote
  nothing produces a green run that means nothing.
- Wire, byte, and protocol expectations use independent literals, NEVER the code's own constant.
  Flip the constant to a wrong value — the test must go red.
- Any allowlist, map, or table a guard reads gets one control fixture per row, plus a completeness
  assertion that every row has one. A guard proven against seven of its eight rows is unproven.
- Record the transcript (mutation, red output, restore, green) where the reviewer can re-run it.

See `/self-review` for the full falsification passes and the incident register behind these rules.

## Modern Patterns

- Go 1.24+: `t.Context()`, `b.Loop()`
- Go 1.25+: `sync.WaitGroup.Go()`
- Timeout/debounce/ticker/rate-limiter tests MUST use `testing/synctest.Test`
  (Go 1.25+) — fake clock, no real waiting; goroutines still blocked when the
  bubble ends FAIL the test (built-in leak detection, no third-party detector
  needed). NEVER `time.Sleep` to wait for goroutines or timers.
- Go 1.26+: `t.ArtifactDir()`, `errors.AsType[T](err)`
- NEVER `t.Setenv` or `t.Chdir` (Go 1.24+) in a parallel test — both panic

## Fuzz Tests

Required for parsing, deserialization, validation functions. Must not panic.

## Conscious Deviations

- **Example functions: not required for private applications.** Effective Go and
  the Google style guide value testable `Example*` functions, but their payoff is
  godoc for external readers of an importable library. A private application has
  no external godoc audience — unit, benchmark, fuzz, and testcontainers
  integration tests carry the coverage instead. Record the waiver in the
  project's rules so reviewers know it is a decision, not an omission. Examples
  remain expected for any package published as a public module.

See: `/go-testing-advanced` skill for golden files, fixtures, coverage, go-cmp advanced, synctest.
