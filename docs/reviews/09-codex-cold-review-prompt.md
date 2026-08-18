# Cold review: everything since PR #22

You are reviewing goen, a Traditional-Chinese 3C storefront in Go. **Read the
diff before you read anything anyone says about it.** `review-process.md` calls
this cold-first acceptance, and the reason is that a builder's report hands you
its own frame: you inherit which things it thought were interesting, and you
stop looking where it stopped.

## Scope

```
git diff bc11c22~1..HEAD
```

85 files, +5283 / −3053, thirteen commits. Everything in it was written by one
session with no second pair of eyes. **That session also wrote every test that
now passes**, which is the specific thing you are here to distrust.

## What you are NOT given

The PR descriptions, the commit bodies, and this repository's own account of
what each change was for. Form your findings first. Read them afterwards, and
only to check whether a finding is already answered — not to decide whether to
raise it.

## Where this codebase's guards are known to be blind

Stated so you spend your effort where the machinery cannot reach, not so you
trust it:

- **Every completeness guard here asks about ABSENCE** — a table with no writer,
  a field nobody assigns, a column nobody reads, a key nobody renders. None of
  them can see **two correct halves that disagree**. Mistakes #13, #30, #31, #34
  and #37 in `CLAUDE.md` are all that shape and all were found by people.
- **A route being linked is not a state being reachable.** #35 shipped a whole
  feature that no path could produce a second column for, with every guard green.
- **A test written from the implementation asserts what the code does.** Three
  findings in round 6 were each locked in by a passing test.

## What to attack, in order

1. **Money and stock.** `internal/payment`, `internal/cart`, the
   `SECURITY DEFINER` functions, `order_amount_owed`. Anything where two places
   compute one number.
2. **Authorisation.** `order_access_grants`, `RequireStaff` / `RequireAdmin` /
   `StaffOnly`, the admin column grants. Ask whether the guard on a route is
   weaker than the thing it protects.
3. **The new schema object.** `categories_position_key` is `NULLS NOT DISTINCT`
   and was added by amending `001` in place. Ask what it made newly reachable,
   what write path can now fail that could not before, and whether every caller
   maps that failure to something a person can act on.
4. **The new tests themselves.** Each is a review target
   (`review-process.md`). For each one ask: *what mutation would this NOT
   catch?* Several are asserted against rendered HTML; at least one guard is
   static and reads templates rather than output. Say where that is too weak.
5. **The claims in prose.** `CLAUDE.md` and `README.md` state numbers, lists and
   limits. One guard now binds four of them to the catalogue. Find a stated
   claim that nothing holds.

## What a useful finding looks like

- The **mechanism**, not the smell. Name the inputs and the wrong outcome.
- **Which existing guard should have caught it and why it did not.**
- If you cannot construct the failing path, say so and mark it uncertain rather
  than dropping it — an unproven mechanism that names a real question is worth
  more than silence, and this project's rules require every finding to reach one
  of three states rather than be waved through.

Do not soften findings. A too-large or skipped notice is answered, never
absorbed.
