# Demo deployment evidence

[goen.koopa0.dev](https://goen.koopa0.dev) demonstrates the fifteen-product sample
catalogue and Stripe sandbox checkout. It is not a shop that sells or ships goods.

## Trying payment

On Stripe's sandbox page, use **4242 4242 4242 4242**, any future expiry date and
any three-digit CVC. Do not enter real card details. These test payments do not
move real money. See [Stripe's test-card documentation](https://docs.stripe.com/testing).
The goen payment page shows this guidance when its configured API key has an
explicit Stripe test prefix. A live or unrecognized key does not display a
sandbox assurance.

## Intended configuration

[manifest.env](manifest.env) is a sanitized configuration template, not proof of
what currently runs on the host. The operator must configure both a sandbox API
key and the matching webhook endpoint signing secret in `/etc/goen/demo.env`.
Never commit their values. The template also directs mail to an on-host Mailpit
sink and supplies ECPay staging configuration.

[goen-restore.timer](goen-restore.timer) and
[goen-restore.service](goen-restore.service) describe the nightly invocation of
[restore-demo-db.sh](restore-demo-db.sh) against
`/var/lib/goen/snapshots/catalog.dump`. The golden snapshot uses the same sample
catalogue as `make db-seed`. These files describe the setup; they do not establish
that a timer is installed or that today's restore succeeded.

The restore credential and restart fixes are tracked separately in
[issue #374](https://github.com/Koopa0/goen/issues/374). That implementation work
does not establish that a revised service is installed or a live restore passed.

## Observed deployment evidence

| When (UTC) | Observation | What it establishes |
| --- | --- | --- |
| 2026-09-22T04:01:58Z | Empty unsigned `POST /webhooks/stripe` returned `400 signature verification failed` | The endpoint followed the configured-gateway path, not the disabled `503` path. This does not distinguish sandbox from live keys. |
| 2026-09-17 | [Issue #381](https://github.com/Koopa0/goen/issues/381) records hosted Checkout with a `cs_test_` session and sandbox indicator | Historical sandbox observation; not a fresh provider checkout or a current host configuration audit. |
| 2026-09-15T08:36:30Z | `HEAD /` returned `HTTP/2 200` | Historical HTTPS reachability only. |

The September 15 operator attestation that the manifest matched the host while
payments were disabled is superseded by the later configured-gateway observation.
No fresh host attestation is claimed here. The historical
[restore-last-run.json](restore-last-run.json) and previously reported enabled
timer are retained as dated records, not evidence of today's scheduling, mail
confinement, or restore success.

An unsigned webhook probe is safe to repeat without creating an order. A `400`
can check that payments remain configured, but cannot prove the sandbox mode,
webhook delivery, successful order payment, or data restoration. Those require
operator/provider evidence. This revision changes no deployment configuration
and made no live order, card submission, invoice, or outbound delivery.
