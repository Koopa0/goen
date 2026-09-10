# goen — agent entry point

This file governs any agent that opens a pull request here.

## What you may work on

- Work from an issue that carries the `grok` label. An issue labeled
  `needs-owner`, `needs-repro` or `blocked` is not yours until that label
  changes.
- Treat the issue body as the specification. When the issue also carries a
  ruling comment, that comment wins: it names the behaviour today, the
  behaviour wanted, the test that locks it, and the scope you stay inside.
  Build that and nothing else.
- Check the premise before you write code. If the behaviour the issue describes
  no longer occurs on current `main`, say so on the issue and stop. Do not
  redefine the issue into a change you can make.

## The pull request

1. Start from `main`.
2. One issue per pull request, plus any issue the ruling bundles with it. Write
   `Closes #<number>` for each, one per line.
3. Give the description four headings, in this order: What changed, How it was
   verified, What was left out, Needs a ruling. Keep a heading you have nothing
   for, and write `None.` under it.
4. Prove each behaviour change under "How it was verified". Break the production
   code the change relies on — revert one branch, drop one guard — run the test
   that locks it, quote the failure you watched, then restore it. Confirm the
   mutation reached the code the test renders: a `replace` that hit an earlier
   identical line proves nothing. A build error is not a red test.
5. Name the gate you ran under the same heading. Run `make verify` unpiped and
   report its exit status. A scoped `go test ./internal/cart/` is worth
   reporting, so long as you call it what it is.
6. No AI attribution trailer in the commit or the description.

Never merge a pull request; opening it ends your work.

## The gate

- `make verify` passes on the exact commit you push. CI runs it on every pull
  request together with the integration suite and the vulnerability scan, and
  `main` accepts nothing that fails any of the three.
- Never weaken a gate, a golden file or a test oracle to reach green. If you
  believe a gate is wrong, leave it red and say so under "Needs a ruling".
- `make verify` needs `golangci-lint` and `squawk` on `PATH` at the versions
  the Makefile pins; every other tool is fetched by `go run`. `make
  test-integration` needs Docker. Run both before calling schema work done.
- `internal/db` and every `*_templ.go` are generated. Edit the `.sql` or
  `.templ` source and run `make sqlc` or `make gen`; the gate compares.
- After pulling a change to `migrations/001`, run `make db-reset` before you
  believe a 500 from the back office. The file is amended in place while
  nothing is deployed, and an older local database is missing what the code
  reads.

## What you may not touch

- A `SECURITY DEFINER` function's privilege, a role's grant, or the `DO` block
  at the end of `migrations/001` that pins `search_path` and revokes `PUBLIC`.
  A grant one column too wide is a storefront request writing a payment.
- The Stripe session's `payment_method_types` pin, the `ExpiresAt` bound to the
  stock hold, or the rule that a webhook is attributed from goen's own payment
  row and never from the event.
- The statutory terms in `internal/site/policies.go`: seven days from receipt,
  return postage on the shop, no exception claimed. These are 消保法 §19 and
  are not the shop's to change.
- Product names, descriptions, FAQ entries and policy prose. Those are the
  shop's content; goen translates chrome, never content.
- `assets/css/ds/`, which is vendored. Style its classes from `app.css`.

Stop if your change needs to cross one of these. If the ruling authorises the
crossing for this issue, do what it authorises and nothing more. Otherwise
write `NEEDS-KOOPA` under "Needs a ruling", name the boundary and why the
change needs it, and open the pull request anyway.

## House style, in the order it is enforced

- Package by feature. No `services`, `repositories`, `handlers`, `models`,
  `util` or `common` directory; a hook refuses to create one.
- An interface exists only where a second production implementation already
  does, or where a consumer in another package needs a subset of a concrete
  type. Tests are never a reason to add one.
- Bind to `PgError.ConstraintName` or `Code`, never to the error's text. Map
  `pgx.ErrNoRows` to the feature's own sentinel; never hand the driver's
  sentinel across a package boundary.
- A closed set is a type with constants, not a string compared to literals.
- A deferred rollback takes `context.WithoutCancel(ctx)`; a guard refuses the
  bare context.
- A timestamp is rendered through `internal/shoptime`, never `t.Format`
  directly; a guard refuses the bare form.
- Money is rendered through `internal/money`; there is one formatter.
- Comments carry the reason a reader needs to not turn the line into a defect.
  No history, no "this used to be", no "proven by mutation".
