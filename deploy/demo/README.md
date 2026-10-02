# Reference deployment

A small, self-contained way to run one goen process against one PostgreSQL with a
nightly restore of the sample catalogue, under systemd:
[goen-restore.timer](goen-restore.timer) runs
[restore-demo-db.sh](restore-demo-db.sh), configured by
[restore.env.example](restore.env.example); [manifest.env](manifest.env) is the
sanitised application configuration.

The restore runs `pg_restore --role=goen` in one transaction, so every table and
`SECURITY DEFINER` function keeps its owner, and a failed restore leaves the old
data in place. `scripts/demo-restore-test.sh`, part of `make verify`, proves both
against a throwaway PostgreSQL.
