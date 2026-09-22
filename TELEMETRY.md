# Commerce telemetry

OpenTelemetry in goen is opt in. Checkout and other request paths keep working when export is disabled or the collector is down.

## Enable locally

```sh
# Terminal 1 — an OTLP/HTTP collector such as the OpenTelemetry Collector contrib image
docker run --rm -p 4318:4318 otel/opentelemetry-collector-contrib:latest

# Terminal 2 — goen
export GOEN_OTEL_ENABLED=1
export GOEN_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export GOEN_OTEL_DIAGNOSTICS=1   # staff-only pprof under /admin/diagnostics/
make run
```

Optional tuning:

| Variable | Default | Meaning |
|----------|---------|---------|
| `GOEN_OTEL_SERVICE_NAME` | `goen` | Service name on exported telemetry |
| `GOEN_OTEL_SHUTDOWN_TIMEOUT` | `5s` | Export shutdown budget |
| `GOEN_OTEL_EXPORT_BATCH_SIZE` | `512` | Trace batch size |
| `GOEN_OTEL_EXPORT_QUEUE_SIZE` | `2048` | Trace queue bound |

## What is exported

| Signal | Name / span | Labels |
|--------|-------------|--------|
| HTTP | route-template spans + `goen.http.server.*` | Route template (`GET /p/{slug}`), status class |
| SQL execution | `postgres.product.*` spans + `goen.db.query.duration` | Fixed product operation, pool role and result; other SQL shares `other` |
| DB pool | `goen.db.pool.*` | Role: `store`, `admin`, `maintenance` |
| Providers | `stripe.*`, `ecpay.*`, `smtp.send` spans + `goen.provider.*` | Bounded operation and outcome only |
| Outbox | `goen.outbox.*` | Same predicates as `/admin/health` (`WorkerHealth`) |
| Cache (#330) | `goen.cache.events` | Domain + outcome (`hit`, `miss`, `fill`, `fallback`) |

Raw slugs, search terms, order numbers, emails and tokens never appear as metric dimensions. Request logs add `trace_id` and `span_id` beside the existing `request_id`. Provider span errors contain only their bounded outcome; raw provider error messages are not recorded as exception events or status descriptions.

## Diagnose common symptoms

### Pool saturation

Look at `goen.db.pool.acquired` against `goen.db.pool.max` for role `store`, plus rising `goen.db.pool.acquire_duration_seconds` and `goen.db.pool.acquire_canceled`. Correlate with an HTTP span whose duration is dominated by waiting, not handler work.

### Hot cache miss

After #330 lands its adapter, `goen.cache.events{outcome=miss}` rising while product page latency grows points at fill pressure, not SQL.

### Slow SQL

Compare the `postgres.product.read`, `product.images`, `product.specs`, `product.variants`, `product.options` and `product.breadcrumb` child spans with the parent HTTP span. `goen.db.query.duration` measures execution and result consumption after pool admission; pool wait remains in the pool metrics. Other SQL uses the fixed `other` operation. SQL text, parameters, database error text and connection strings are never exported. Check PostgreSQL `statement_timeout` (15s store, 30s admin) when query outcome is `timeout`.

### Stalled provider

`goen.provider.duration` for `stripe`, `ecpay`, or `smtp` near timeout with `provider.outcome=timeout` or `error`. HTTP may still finish if the handler degrades gracefully.

### Stalled outbox

`goen.outbox.pending` > 0 with `goen.outbox.oldest_seconds` above ten minutes and `/admin/health` outbox row red — same semantics as the staff dashboard, not a separate definition.

## Staff diagnostics

When `GOEN_OTEL_DIAGNOSTICS=1`, signed-in staff can reach Go profiles at `/admin/diagnostics/debug/pprof/`. These routes are not on the storefront mux.

## Collector loss

Export buffers are bounded. A dead collector must not block responses; shutdown respects `GOEN_OTEL_SHUTDOWN_TIMEOUT`.
