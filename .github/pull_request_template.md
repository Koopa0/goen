<!--
The description has four parts, one per heading below. Fill each one in and
delete these notes as you go. Where a part has nothing under it, write `None.`
rather than deleting the heading.

Link every issue this change closes on its own line: `Closes #<number>`.
Delete the line below if it closes none.
-->

Closes #

## What changed

<!--
What a customer or a staff member can now do, or stop doing, and where. Name the
packages you touched.

`internal/db` and every `*_templ.go` are generated. If you edited a `.sql` file
or a `.templ` file, run `make sqlc` or `make gen` and commit what they produce;
the gate rebuilds both and fails on the difference.
-->

## How it was verified

<!--
If your change alters behaviour, name each test that now locks it and quote the
failure you watched before the fix. A test you have never seen fail is not a
lock, and a build error is not a red test. One line each:

    TestAFullyFundedOrderIsNotAskedToPay: red on main — an order that owes
    nothing is asked to pay; green here.

If your change alters no behaviour — a rename, a comment — skip the locks.

Then name the gate you ran, precisely. `make verify` is what CI runs; report its
exit status unpiped. `make test-integration` needs Docker. A scoped
`go test ./internal/cart/` is worth reporting, but it is not a verify exit code.

Do not weaken a gate, a golden file, or a test oracle to reach green.
-->

## What was left out

<!--
What you noticed and did not do, and why. A defect you found but were not asked
to fix belongs in a new issue, linked here.
-->

## Needs a ruling

<!--
A question only the maintainer can answer: a boundary this change would cross,
a commercial decision, a wording choice. Write `None.` if there is none.
-->
