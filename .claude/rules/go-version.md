---
paths:
  - "**/*.go"
---

# Go Version Idiom Rules

## Project Version: Go 1.27+

This project targets Go 1.27+. MUST use the latest idioms **the `go.mod` `go`
directive supports** — check it for the exact version. This harness is portable:
in a consumer repo on an older toolchain the "mandatory" rows below are gated by
the `Since` column — never emit an idiom newer than the target can compile (the
SessionStart hook surfaces the ceiling). A 1.26-only form (`new(expr)`,
`errors.AsType[T]`) in a 1.23 repo is a build break, not a style nit.

## Mandatory Modern Patterns

| Old Pattern (NEVER)                                                      | Modern Pattern (MUST)                                                         | Since |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------------- | ----- |
| `for i := 0; i < b.N; i++`                                               | `for b.Loop()`                                                                | 1.24  |
| `context.Background()` in tests                                          | `t.Context()`                                                                 | 1.24  |
| `mux.HandleFunc("/path", h)`                                             | `mux.HandleFunc("GET /path", h)`                                              | 1.22  |
| manual path parsing                                                      | `r.PathValue("id")`                                                           | 1.22  |
| custom iterator with channel                                             | `iter.Seq[T]` / `iter.Seq2[K,V]`                                              | 1.23  |
| `for i := 0; i < n; i++` (simple range)                                  | `for i := range n`                                                            | 1.22  |
| `ptr[T](v)` helper for pointers                                          | `new(v)` with initial value                                                   | 1.26  |
| `var target *MyErr; errors.As(err, &target)`                             | `errors.AsType[*MyErr](err)`                                                  | 1.26  |
| `var wg sync.WaitGroup; wg.Add(1); go func() { defer wg.Done(); ... }()` | `wg.Go(func() { ... })`                                                       | 1.25  |
| package-level `sync.Once` + var + getter                                 | `sync.OnceValue` / `sync.OnceValues` (struct-field `sync.Once` stays correct) | 1.21  |

## Feature Version Reference

| Feature                             | Version | Example                                                       |
| ----------------------------------- | ------- | ------------------------------------------------------------- |
| ServeMux method routing             | 1.22    | `"GET /users/{id}"`                                           |
| range over integer                  | 1.22    | `for i := range 10`                                           |
| range over function                 | 1.23    | `for v := range seq`                                          |
| iter package                        | 1.23    | `iter.Seq[V]`, `iter.Seq2[K,V]`                               |
| sync.OnceFunc/OnceValue/OnceValues  | 1.21    | `var loadConfig = sync.OnceValues(parseConfig)`               |
| t.Context()                         | 1.24    | `ctx := t.Context()`                                          |
| b.Loop()                            | 1.24    | `for b.Loop() { }`                                            |
| testing/synctest                    | 1.25    | `synctest.Test(t, func(t *testing.T) { ... })`                |
| sync.WaitGroup.Go()                 | 1.25    | `wg.Go(func() { ... })`                                       |
| net/http.NewCrossOriginProtection() | 1.25    | CSRF — returns `*CrossOriginProtection`, call `.Handler(mux)` |
| slog.GroupAttrs()                   | 1.25    | `slog.GroupAttrs("key", attrs...)`                            |
| go vet: waitgroup, hostport         | 1.25    | catches misplaced Add, bad host:port                          |
| new(expr) initial value             | 1.26    | `new(computeValue())`                                         |
| self-referential generics           | 1.26    | `type Adder[A Adder[A]]`                                      |
| errors.AsType[T]()                  | 1.26    | `errors.AsType[*MyErr](err)`                                  |
| slog.NewMultiHandler()              | 1.26    | fan-out to multiple handlers                                  |
| testing.T.ArtifactDir()             | 1.26    | test output directory                                         |
| Green Tea GC (default)              | 1.26    | 10-40% less GC overhead                                       |
| go fix modernizers                  | 1.26    | `go fix ./...` updates idioms                                 |
| method-specific type parameters     | 1.27    | `store.HandleJSON[Message](topic, handler)`                   |

## Rules

- MUST prefer latest idioms over older equivalents (see table above)
- MUST check `go.mod` `go` directive matches project version
- MUST NOT use features from unreleased Go versions
- When reviewing code, flag old patterns as IMPORTANT and suggest the modern replacement

NOTE: SA1019 (deprecated APIs) is ENABLED in `.golangci.yml` — the harness ships strict defaults. Brownfield consumers may add `"-SA1019"` locally and record the waiver in their CLAUDE.md. The blacklist below is additionally mechanized by depguard (imports) and forbidigo (calls).

## Deprecated Patterns Blacklist

AI training data contains outdated patterns. NEVER use these:

| NEVER                                        | MUST Use Instead                              | Since  |
| -------------------------------------------- | --------------------------------------------- | ------ |
| `interface{}`                                | `any`                                         | 1.18   |
| `io/ioutil` (entire package)                 | `io` + `os`                                   | 1.16   |
| `golang.org/x/exp/slices`                    | `slices` (stdlib)                             | 1.21   |
| `golang.org/x/exp/maps`                      | `maps` (stdlib)                               | 1.21   |
| `sort.Slice` / `sort.SliceStable`            | `slices.SortFunc` / `slices.SortStableFunc`   | 1.21   |
| `http.ListenAndServe` directly               | `http.Server{}` + graceful shutdown           | always |
| `log.Fatal` / `log.Fatalf` in handlers       | return error                                  | always |
| `log.Printf` / `fmt.Println` for logging     | `slog.Info` / `slog.Error`                    | 1.21   |
| `sync.Map` for typed maps                    | generic wrapper or `sync.RWMutex` + `map[K]V` | 1.18   |
| `iota` for non-sequential constants          | explicit values                               | style  |
| manual HTTP path parsing                     | `r.PathValue()`                               | 1.22   |
| `var t *Err; errors.As(err, &t)`             | `errors.AsType[*Err](err)`                    | 1.26   |
| `wg.Add(1); go func() { defer wg.Done() }()` | `wg.Go(func() { ... })`                       | 1.25   |
| `httputil.ReverseProxy{Director: ...}`       | `ReverseProxy{Rewrite: ...}`                  | 1.26   |
| `go/parser.ParseDir`                         | use `go/ast` or `go/packages`                 | 1.25   |
| `ptr[T](v)` pointer helper                   | `new(v)` built-in                             | 1.26   |
