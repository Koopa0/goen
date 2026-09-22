# Multi-instance commerce load harness (#333)

Repeatable HTTP workloads against an isolated reference topology: two app
instances behind nginx, shared PostgreSQL, shared Valkey (for #330), and
simulated mail. k6 runs outside the Go module graph.

## Topology

| Component | Purpose | Default host port |
|-----------|---------|-------------------|
| PostgreSQL 18 | Authoritative commerce data | `15433` |
| Valkey 8 | Shared cache dependency (#330) | `16379` |
| goen-a / goen-b | Same commit binary, host processes on 19701/19702 | `127.0.0.1` |
| nginx | Round-robin storefront entry | `19700` |
| Mailpit | Captured SMTP (simulated delivery) | `18026` |

Per-process pool sizes come from `cmd/goen/main.go` (store 25, admin 10,
maintenance 2). Two instances therefore expose 50 storefront connections.
CPU and memory caps are declared in `load/compose.yml`.

Copy `load/topology.env.example` to `load/topology.env` to pin versions,
fixture identity (`LOAD_FIXTURE_ID=load-catalog-v1`), and k6 bounds.

## Commands

```sh
# Provision fixtures only — does not touch the dev database on 5433.
make load-up

# Run one profile (browse | cache-stress | stock-contention | mixed-ops | dependency-failure)
make load-profile-browse
make load-profile-stock-contention

# Post-run oracles (oversell, duplicate checkout)
make load-oracle

# Remove the isolated topology
make load-down
```

Each profile writes machine-readable k6 output and a human-readable summary
under `load/evidence/`. `topology.json` records commit hash, image tag, seed,
pool sizes, and provider simulation flags.

## Profiles

1. **browse** — anonymous home/search/product traffic with skewed hot-product
   reads; supports `LOAD_CACHE_MODE=cold|warm|disabled`.
2. **cache-stress** — promotion spike, bogus slugs, deals/search churn.
3. **stock-contention** — competing buyers on `LOAD-FLASH-001` (three units)
   plus repeated checkout submissions.
4. **mixed-ops** — storefront checkout while staff hit `/admin` with a seeded
   session token.
5. **dependency-failure** — load during Valkey outage or app restart; requires
   some successful degraded responses, not total rejection.

All profiles report offered vs achieved work (`http_reqs`, `dropped_iterations`,
threshold breaches). Tune `LOAD_K6_VUS_MAX`, `LOAD_K6_DURATION`, and
`LOAD_K6_ARRIVAL_RATE` before acceptance runs.

## Oracles

`load/oracle` checks:

- shelf stock plus committed order lines never exceed received inventory on the
  flash-sale variant (holds are not double-counted against already-decremented
  stock);
- no idempotency key produced more than one order;
- impaired runs still complete useful work (`DegradedWork`) using k6 check counts
  carried in from the profile summary.

Plant a conservation violation by adding committed units without matching stock;
`go test ./load/oracle/ -run Conservation` goes red.

Provider doubles are marked simulated in `topology.json`. External sandbox
acceptance remains #40; this harness does not claim production throughput.

### Stock replay evidence

`stock-contention` requires a freshly provisioned fixture. The runner generates
`LOAD_RUN_ID` (or accepts an explicit unique value). Setup prepares independent
buyer carts before consuming stock, places one anchor order, and passes only
JSON body/cookie snapshots to VUs. `competingBuyer` places distinct orders;
`repeatSubmit` restores the anchor cookies and submits the identical encoded
body/key six times, requiring the original order redirect. Redirects are not
followed into provider payment routes. DOM selectors read actual form controls,
including the invoice type; missing fields and unsuccessful placement fail.

The summary and JSONL console evidence use the run ID in their filenames.
Cookies and checkout bodies are hashed, never included in evidence. The database
oracle requires the same run ID, six identical replays and at least one successful
competing placement. It ties each observed order/key/email to timestamps from this
run, one line, one live hold and one inventory debit, the expected TWD amount,
and no payment or credit entry. Global inventory conservation is checked as well.
Old healthy database rows alone cannot satisfy this gate. The runner collects
evidence and runs the oracle even when k6 fails, preserving its nonzero exit.

The `load-replay` CI job provisions two app processes and a fresh database for
both controls. Its negative control removes the actual replay function and must
still produce fresh anchor/competing orders before the oracle rejects zero
replays. This is pending-checkout acceptance only. It does not exercise Stripe
or ECPay, provider doubles, capture/refund delivery, or dependency recovery.
