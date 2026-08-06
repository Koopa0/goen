# Review Process Rules

Applies to every review channel: L1/L2 review agents, external review bots, and human reviewers.

## Finding Dispositions

Every finding reaches exactly one of three states BEFORE merge:

| Disposition            | What it requires                                                             |
| ---------------------- | ---------------------------------------------------------------------------- |
| **Fixed in this PR**   | The fix commit is on the same branch, and the finding's thread says so       |
| **Queued by name**     | A named work item in the project's plan or tracker — never a mental note     |
| **Refused in writing** | A reply stating the mechanism it refutes                                     |

"Fixed on another branch" is none of these — that is how a defect a reviewer handed over early
still ships. A finding whose mechanism is wrong can still carry a point worth taking; the refusal
says so instead of dismissing the whole finding.

A fix made in response to a finding is new work: run `/self-review` on it again. A fix that closes
one hole routinely opens the next — ask what the fix made newly reachable.

## Too-Large Notices

A PR a reviewer declines to read has not been reviewed.

- A too-large or skipped notice is answered, never absorbed: re-trigger the review by hand or
  split the PR.
- Silence from a reviewer is not a passing verdict.

## Cold-First Acceptance

When one session or agent builds and another accepts:

- Form findings from the raw diff BEFORE reading the builder's report — a report is a warm reading
  and hands you its own frame.
- Review the merge preview against the target branch, not the branch alone — a textually clean
  merge can produce a state neither side ever ran.
- Re-run verification transcripts; never just re-read them. A transcript is a claim; re-running it
  is what turns the claim into a fact.
- Verification instruments (tests, probes, greps, CI jobs) are review targets in their own right —
  ask each new one to show itself failing (rules/testing.md, "Locks Are Proven by Mutation").
- Scale the number of fresh-context review lenses to the blast radius, not to the diff size.

## NEVER

- NEVER wave a batch of findings through in either direction — triage is line by line.
- NEVER let acceptance rest on the builder's report alone — acceptance that only reads the report
  is not acceptance.
- NEVER merge with a finding in no named state.

## See Also

- `/self-review` — falsify your own work before anyone else reviews it
- `development-lifecycle.md` — where L1/L2 reviews sit in each tier
