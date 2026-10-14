<!--
Draft replacement for the "### Test files" section of CONTRIBUTING.md (lines
190-209 at the base of the tooling pull request), delivered with the rename tooling
and applied by the pull request that adds the name-length guard. Do not apply it
earlier: it says internal/repocheck refuses long names, and until that guard
exists the sentence is false.

It follows test-naming.md section 5 with three changes. The external-test-package
default stays (decided: the standard library uses both). The helper-package and
browser-test bullets stay, which the section 5 draft dropped. The file-mirroring
bullet keeps its rule and adds the standard library as the source.

The test names in the first bullet are examples and do not exist, so
TestEveryNamedTestExists would refuse them in CONTRIBUTING.md; the pull request
that applies this text must swap in names that exist, or end those lines with the
marker named-test-exempt:.
-->

### Tests

- Name a test after the unit it exercises: `TestParse`, `TestCart`, `TestStore_Decide` ([Google decisions: underscores](https://google.github.io/styleguide/go/decisions#underscores)). Cases that share logic are `t.Run` subtests or table rows with short identifier-like names and no `/` ([subtest names](https://google.github.io/styleguide/go/decisions#subtest-names), [blog](https://go.dev/blog/subtests)). Cases needing different logic are separate functions, `TestStore_DecideSplitReturn` ([table tests](https://go.dev/wiki/TestComments#table-driven-tests-vs-multiple-test-functions)). The sentence explaining why goes in the doc comment or a `desc` field, not the name. `internal/repocheck` refuses names over 40 characters after `Test` (about the standard library's 99th percentile).
- Fail with `Func(input) = got, want want`, and keep going with `t.Error` ([useful test failures](https://google.github.io/styleguide/go/decisions#useful-test-failures)).
- Use the external test package (`package foo_test`) unless the test needs
  unexported names or an import cycle forbids it ([test package](https://google.github.io/styleguide/go/decisions#test-package); the standard library uses both).
- Like the standard library, put tests for `foo.go` in `foo_test.go` and `foo_integration_test.go`. A topic file covers behavior spanning several source files and uses the package's domain words, never an issue or PR. Keep files small enough to find things in ([package size](https://google.github.io/styleguide/go/best-practices#package-size)).
- Every integration test file has both `//go:build integration` and the
  `_integration_test.go` suffix (a goen rule). Keep `TestMain` and shared setup in
  `main_integration_test.go`, with no tests; `internal/db` (`schema_integration_test.go`),
  `internal/contact` and `internal/home` (`handler_integration_test.go`) keep
  theirs beside the tests it serves.
- Put a helper shared by several packages in the area's helper package, such
  as `admintest` or `dbtest`.
- A test that drives a real browser has the `_browser_test.go` suffix
  (`_browser_integration_test.go` when it also needs the `integration` tag),
  learns that it may run only through `internal/ui/uitest` (`Browser`,
  `LocalChrome`), and is run by name from the Makefile's `check-layout-run` or
  a workflow. A test may read CSS or JS text only where the bytes are the
  contract, and `internal/repocheck/assetreaders_test.go` lists each one.
