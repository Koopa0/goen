# Reference snapshot restore

A reference for restoring a fixed demo snapshot under systemd:
[goen-restore.timer](goen-restore.timer) runs
[restore-demo-db.sh](restore-demo-db.sh), configured by
[restore.env.example](restore.env.example); [manifest.env](manifest.env) is the
sanitised application configuration template, not an attestation of the live host.

The timer runs at 03:00 UTC and also catches a missed run when activated. The
script checks the restore identity and archive before stopping `goen.service`,
restores the snapshot, then starts the service. It does not create demo history,
run `seed/demo_shift.sql`, or refresh the snapshot's anchor day. Each run restores
the same saved dates; it does not make the history end yesterday.

The restore runs `pg_restore --role=goen` in one transaction, so every table and
`SECURITY DEFINER` function keeps its owner, and a failed restore leaves the old
data in place. `scripts/demo-restore-test.sh`, part of `make verify`, proves both
against a throwaway PostgreSQL in CI. If the restore fails, the script attempts
to restart on the previous data. If starting after a successful restore fails,
operator recovery is required.

## History and calendar dates

[Try the demo](../../README.md#try-the-demo) describes creating 90 days of history
and moving a restored snapshot to today's shop date. The authoritative scripts
are [demo_history.sql](../../seed/demo_history.sql) and
[demo_shift.sql](../../seed/demo_shift.sql); install
[demo_shift_columns.sql](../../seed/demo_shift_columns.sql) alongside the latter.
They require a superuser connection to the named, seeded demo database, with
goen stopped. The application service logins in `manifest.env` are not suitable.

Keep the snapshot's database name and anchor day with it. The anchor must match
the database's own date, read from the seed's shipping rates, rather than the
date a file was copied. The shift refuses a mismatched or already shifted date
and databases that could hold real payments. Seeded provider references were
not issued by Stripe or ECPay; the linked recipe explains the sandbox failures
when staff try to refund or amend those orders.

A nightly cycle that restores, shifts, then starts goen needs operator-provided
orchestration for that whole sequence. The supplied timer guarantees only the
fixed-snapshot restore above. `restore-demo-db.sh` starts goen itself, so it is
not a restore-only step to run before a later shift.

## Application and restore settings

[.env.example](../../.env.example) documents application settings;
[`loadConfig`](../../cmd/goen/main.go) reads their names and defaults.
`manifest.env` lists those supported names for this reference, including optional
features left unconfigured. Supply the actual values to your `goen.service`
environment on the host; replace all redactions. Prefer `GOEN_STRIPE_API_KEY`;
`GOEN_STRIPE_SECRET_KEY` remains a compatibility fallback when that name is unset.

`restore.env.example` is a separate operator configuration, loaded by
`goen-restore.service` from `/etc/goen/restore.env`. Its two `GOEN_RESTORE_*` names
belong to the restore script, not `cmd/goen`. Application settings do not belong
in that file, and application database credentials must not be used for restore.

## Shared demo account

Set both `GOEN_DEMO_ACCOUNT_EMAIL` and `GOEN_DEMO_ACCOUNT_PASSWORD` in the
application environment, or leave both unset. At every start, goen creates or
repairs that verified customer account before serving and publishes both values
on the sign-in page. Use an address dedicated to the demonstration: a staff
account is refused. The account cannot change its own shared credentials. The
pair is refused beside a live Stripe key; this reference uses Stripe's sandbox.
See [.env.example](../../.env.example) and
[the account implementation](../../internal/account/demo.go) for the contract.
