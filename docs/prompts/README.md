# Investigation prompts

Nine self-contained briefs, one per file. Each is written to be handed to an agent that
has **no context from the conversation that produced it** — it must be able to start
from that one file plus the repository.

| # | File | Subject | For |
|---|---|---|---|
| P1 | [p1-customer-journey.md](p1-customer-journey.md) | Customer journey walk + account information architecture + address reuse + Google sign-in | Codex |
| P2 | [p2-back-office.md](p2-back-office.md) | Back-office walk (run the shop as the person who runs it) | Codex |
| P3 | [p3-ecpay-logistics.md](p3-ecpay-logistics.md) | 綠界 / 超商 API: integrate or not, evidence-led | Codex |
| P4 | [p4-visual-design.md](p4-visual-design.md) | Visual design direction (Apple / Google Store references) + image asset spec | Claude Design |
| P5 | [p5-what-is-not-built.md](p5-what-is-not-built.md) | What is not built, and what should be | Codex |
| P6 | [p6-readme-and-licence.md](p6-readme-and-licence.md) | README, licence, open-source presentation | Codex |
| P7 | [p7-build-ci-observability.md](p7-build-ci-observability.md) | Packaging, CI/CD, linting, observability | Codex |
| P8 | [p8-concurrency-flash-sale.md](p8-concurrency-flash-sale.md) | Concurrency under a promotional spike: PostgreSQL + Go | Codex |
| P9 | [p9-ai-genkit.md](p9-ai-genkit.md) | AI features worth building, on Genkit | Codex |

## Suggested order

**P1 and P2 first** — they are the only two that can find defects in what already
exists; everything else is about what to build next. **P8 next**, because it is the one
whose answer could invalidate a schema decision. **P4 and P9** change the product rather
than the code. **P3, P5, P6, P7** can run any time.

## The convention every brief shares

Each brief ends by asking for the same three lists, because that is what made the last
acceptance round's scope obvious:

1. **Verified by running** — executed, result observed.
2. **Read but could not verify** — a view formed from the code or the documentation,
   without running it.
3. **Did not look at** — in scope on paper, not examined.

P4 is the exception: it is a design brief for Claude Design, not an investigation, so it
delivers artefacts rather than the three lists.
