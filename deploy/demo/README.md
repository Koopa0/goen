# Demo deployment

[goen.koopa0.dev](https://goen.koopa0.dev) demonstrates the forty-product sample
catalogue and Stripe sandbox checkout. It is not a shop that sells or ships goods.

On Stripe's sandbox page use **4242 4242 4242 4242**, any future expiry and any
three-digit CVC; no real money moves ([test cards](https://docs.stripe.com/testing)).
Never enter real card details.

The demo is one goen process against one PostgreSQL, the only supported shape
(see "What goen assumes" in [CONTRIBUTING.md](../../CONTRIBUTING.md)). Uploaded
images live in the database, so the nightly restore resets them too.

[manifest.env](manifest.env) is the sanitized configuration; the sandbox Stripe
key and its webhook signing secret go in `/etc/goen/demo.env`, never in Git.

## Nightly restore

[goen-restore.timer](goen-restore.timer) runs [goen-restore.service](goen-restore.service),
which runs [restore-demo-db.sh](restore-demo-db.sh) against
`/var/lib/goen/snapshots/catalog.dump` (the `make db-seed` catalogue) from a
checkout at `/opt/goen`. It reads `/etc/goen/restore.env`
([template](restore.env.example)), not `demo.env`.

- The login must be `goen`, the schema owner, or a LOGIN member of `goen`; the
  script restores with `pg_restore --role=goen` so every table and `SECURITY
  DEFINER` function keeps its owner. It never passes `--no-owner`, and no
  application role is accepted.
- It stops `goen.service` and restores in one transaction. On failure nothing
  changed: it restarts `goen.service` on the previous data and exits non-zero,
  leaving the unit `failed`; there is no other alert. If the start after a
  successful restore, or the restart after a failed one, also fails, the service
  stays down for an operator.

`scripts/demo-restore-test.sh`, part of `make verify`, locks this against a
throwaway PostgreSQL: owners are unchanged after a restore, and a corrupt
snapshot leaves the old data and restarts the service.
