# goen — agent entry point

This file governs any agent that opens a pull request here. `CONTRIBUTING.md`
says what goen is and how to build it; read it first.

## What you may work on

- An issue that carries the `grok` label. `needs-owner`, `needs-repro` and
  `blocked` are not yours until the label changes.
- The issue body is the specification. A ruling comment on it wins over the
  body: it names the behaviour today, the behaviour wanted, the test that locks
  it, and the scope. Build that and nothing else.
- Check the premise first. If the behaviour the issue describes no longer occurs
  on current `main`, say so on the issue and stop. Do not redefine the issue
  into a change you can make.

## The pull request

1. Branch from `main`. One issue per pull request; `Closes #<n>` per issue, one
   per line.
2. Four headings, in order: What changed · How it was verified · What was left
   out · Needs a ruling. Keep a heading you have nothing for; write `None.`
3. Under "How it was verified", prove each behaviour change: break the
   production code the change relies on, run the test that locks it, quote the
   red you watched, restore. Confirm the mutation reached the code the test
   exercises — for a `.templ` change, grep the generated `_templ.go`. A build
   error is not a red test.
4. Under the same heading, name the gate. `make verify` unpiped, exit status
   quoted. `make test-integration` if you touched a `.sql` file or
   `migrations/`. A scoped `go test ./internal/cart/` is worth reporting as
   what it is.
5. Commits and GitHub text carry no agent identity. See below.
6. Opening the pull request ends your work. Never merge.

### Commit and GitHub text — no agent identity

Do not put any of the following in commit messages, PR titles/bodies, or Issue/PR comments:

- `Co-authored-by: Cursor Agent <…>` or any Cursor Co-authored-by trailer
- Self-identification as Cursor / Cursor Agent / app/cursor / "written by Cursor"

## The gate

- `make verify` on the exact commit you push. CI runs it plus the integration
  suite plus the vulnerability scan; `main` accepts nothing that fails any.
- Never weaken a gate, a golden file or a test oracle to reach green. If a gate
  is wrong, leave it red and say so under "Needs a ruling".
- `golangci-lint` and `squawk` on `PATH` at the Makefile's pins; everything
  else is `go run`. Docker for the integration suite.
- `internal/db` and `*_templ.go` are generated: edit the `.sql` or `.templ`,
  run `make sqlc` or `make gen`, commit the output.
- After pulling a change to `migrations/001`, `make db-reset` before you
  believe a 500.

## What you may not touch

- A `SECURITY DEFINER` function's privilege, a role's grant, or the `DO` block
  that ends `migrations/001`. One column too wide is a storefront request
  writing a payment.
- The Stripe session's `payment_method_types` pin, its `ExpiresAt` bound to the
  stock hold, or webhook attribution from goen's own payment row.
- The statutory terms in `internal/site/policies.go`. They are 消保法 §19.
- Product names, descriptions, FAQ and policy prose: the shop's content.
- `assets/css/ds/`: vendored. Style its classes from `app.css`.

Stop if the change needs to cross one. If the ruling authorises it, do what it
authorises and no more. Otherwise write `NEEDS-KOOPA` under "Needs a ruling",
name the boundary, and open the pull request anyway.

## House style, each enforced by a test named in `CLAUDE.md`

- Package by feature; no `services` / `models` / `util` directory.
- An interface only where a second production implementation exists or a
  consumer in another package needs a subset. Never for a test.
- `PgError.ConstraintName` or `Code`, never the error's text.
- A closed set is a type with constants.
- Rollback with `context.WithoutCancel(ctx)`; render time through
  `internal/shoptime`; render money through `internal/money`.
- A sentence goen says lives in `internal/i18n`, both languages, one line.
- A comment carries the reason a reader needs to not break the line. No
  history, no "used to be", no "proven by mutation".
