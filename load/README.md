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

- sold plus held units never exceed sellable stock on the flash-sale variant;
- no idempotency key produced more than one order;
- impaired runs still complete useful work (`DegradedWork`).

Plant an oversell by ignoring active holds in `VariantSnapshot.ConsumedUnits`;
`go test ./load/oracle/ -run Oversell` goes red.

Provider doubles are marked simulated in `topology.json`. External sandbox
acceptance remains #40; this harness does not claim production throughput.
