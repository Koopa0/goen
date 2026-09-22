# Proposed demo restore contract

This is a deployment template awaiting operator approval and execution evidence
for #374. It is not a statement of the configuration currently on the demo host.
The historical operator attestation in `restore-last-run.json` does not prove
this revision or resolve the storefront-role failure reproduced in #374.

The oneshot reads `/etc/goen/restore.env`, separately from the storefront's
`/etc/goen/demo.env`. Start from [restore.env.example](restore.env.example) and
supply a separately authorized database owner/restore login. Do not give the
storefront, admin, reporting or maintenance roles DDL privileges. This proposal
creates no deployment database role and changes no application grants.

The schema CI job runs `scripts/check-demo-restore-database.py` in its own
PostgreSQL container, with no published port or shared database. It loads this
revision's migration, builds custom-format snapshots, and runs the production
restore script using a fixture-only `restore_owner` login. That login owns the
destination database, is not a superuser, and has no role memberships. The drill
checks successful data replacement, then a real CHECK failure during COPY and
the rollback of earlier changes. Removing the production transaction flag must
fail the same data-preservation assertion; the original script must pass again.
The artifact records source and dump hashes, identity, ownership, before/after
rows, command exits and PostgreSQL logs. Only `systemctl` is simulated, and its
record proves requested stop/start ordering, not the state of a real service.
CI fixture execution does not authorize or attest a deployment identity.

Before stopping `goen.service`, the script verifies the database connection's
session identity and the custom-format snapshot's table of contents. Restore
runs in a single transaction. On success it starts the service; on restore
failure it reports that the service remains stopped and requires operator
recovery. A successful restore followed by a failed start is a distinct error.
The failure policy is proposed, not an operator-approved availability promise.

Before enabling the timer, the operator must approve that failure policy, verify
the actual service/environment files and snapshot provenance, run an owned
PostgreSQL restore drill with the intended identity, and inject a failed restore.
Record the exact source revision, image/schema/snapshot digest, effective session
identity, command exit status, data checks and final service state. Keep fixture
execution separate from live deployment attestation; a reachable homepage proves
neither restored business data nor the scheduled job's success.

If restore fails, keep traffic stopped while inspecting its exit/journal and the
destination with the operator identity. Establish database consistency and the
matching application revision before explicitly starting the service. Do not
restart merely because the restore process has exited or copy an old successful
attestation into a new record.
