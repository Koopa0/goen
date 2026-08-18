# Round 9 — codex whole-project cold review: dispositions

`review-process.md`: every finding reaches exactly one of three states before
merge — **fixed here**, **queued by name**, or **refused in writing**. Triage is
line by line; nothing is waved through in either direction.

Each finding below was re-verified independently before disposition. A report is
a claim; re-running it is what turns the claim into a fact.

## Verified by hand before the fan-out

### F6 — 折讓 is claimed complete and no path reaches it — CONFIRMED
`invoice.Store.Allowance` exists and calls the gateway. But `admin.Invoicer` —
the consumer interface the back office is built against — declares only
`Documents`, `Issue` and `Void`. **Allowance is not in the interface at all**, so
no admin handler could call it even if a route existed; the single "allowance" in
`cmd/goen/server.go` is an unrelated English word in a comment about store
credit. `README.md:96` states the 折讓 document is issued through 綠界's API.

This is recorded mistake #35's exact shape — a feature every guard reports as
wired that no path can reach — and the guard blind to it is the same one:
`TestEveryHardCodedLinkResolvesToARoute` asks whether a link resolves, never
whether a capability has a door.

### F12(b) — a product with no reviews can never receive its first — CONFIRMED
`productReviews` is rendered only `if v.HasRating()`, which is `RatingCount > 0`,
and the review FORM is inside it. Proven by render:

```
HasRating()=false
review form present: false
reviews section present: false
with 1 review -> form present: true
```

Nothing caught it because **the seed gives all 15 active products reviews**, so
the state does not exist in any fixture or in `check-layout`. The sharper
consequence is not the seeded catalogue but a NEW product: the back office can
create one, and it is unreviewable forever.
