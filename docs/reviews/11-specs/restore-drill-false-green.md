# restore-drill-false-green

**Verdict** CONFIRMED · **Severity** high · **Origin** found in this round's own sweep (already established)

**Files** Makefile, CLAUDE.md

> **RESOLVED — `ACL_SQL` as prescribed below is itself incomplete.** Reported by
> the implementing agent, verified here, and decided. Where this block and the
> body disagree, this block wins.
>
> The prescribed query reads `pg_class.relacl` (tables and views) and
> `pg_proc.proacl` (functions) and nothing else. Measured on this cluster, the
> dump also carries privileges the query cannot see:
>
> ```
> $ pg_dump -s | grep -cE '^GRANT [A-Z]+\('     →  133   column GRANTs
> $ pg_dump -s | grep -c 'GRANT USAGE ON SCHEMA' →    4   schema GRANTs
> GRANT INSERT(id),UPDATE(id) ON TABLE public.orders TO store;
> GRANT USAGE ON SCHEMA public TO store;
> ```
>
> 112 columns in `public` carry an explicit `pg_attribute.attacl`, and
> `pg_namespace.nspacl` carries `USAGE` for all four application roles. **A
> restore that lost every column grant, or schema usage for `store`, would pass
> the drill this spec repairs.**
>
> That is not a marginal class here. CLAUDE.md's own account of those grants:
> *"Fourteen column grants over eight tables close it, and the sharpest is
> `product_reviews.hidden_at` — hiding takes a review out of the SCORE, so that
> was a product's public rating movable from a storefront request."* Losing them
> does not break the site; it silently WIDENS `store` to whole-table UPDATE on
> the columns that are the shop's statements about what a customer wrote. Nothing
> would go red, because every suite connects as the owner — which is the same
> reason this whole finding exists.
>
> **Extend `ACL_SQL` with two more arms**, in the same shape as the two it has:
>
> - **Columns** — `pg_attribute.attacl`, joined to `pg_class`/`pg_namespace`,
>   `nspname='public'`, `attacl IS NOT NULL`, `LATERAL unnest(attacl)`, and the
>   same `split_part(a::text,'=',1) <> pg_get_userbyid(c.relowner)` owner filter.
>   Emit `'column'||chr(9)||relname||'.'||attname||chr(9)||a::text`. Do **not**
>   reach for `acldefault` here as the table arm does: a column with no explicit
>   ACL inherits the table's, so only explicit ones are the subject, and a lost
>   grant shows as a line present live and absent in the copy — which `diff`
>   already catches.
> - **Schema** — `pg_namespace.nspacl` for `nspname='public'`, same unnest and
>   same owner filter, emitted as `'schema'||chr(9)||nspname||chr(9)||a::text`.
>
> **Two more mutations, one per new arm**, each recorded RED before the drill is
> believed — the existing `pg_dump -s` and dropped-index mutations do not exercise
> either:
>
> - `REVOKE INSERT(hidden_at) ON product_reviews FROM admin` on the restored copy
>   → the drill must FAIL and name that column. **From `admin`, not `store`** —
>   the live ACL is `product_reviews.hidden_at -> {admin=aw/goen}` and `store`
>   holds nothing on it. The CLAUDE.md sentence quoted above says a public rating
>   "WAS movable from a storefront request"; the past tense is the point, and the
>   narrowing to `admin` is what closed it. A mutation revoking from a role that
>   holds no grant changes nothing and would be recorded GREEN — a false mutation
>   proof, in the proof of an instrument.
> - `REVOKE USAGE ON SCHEMA public FROM store` on the restored copy → the drill
>   must FAIL and name the schema.
>
> **And correct the claim in the other direction.** `pg_dump` does not carry
> cluster-global objects: roles, their attributes, and database-level `GRANT`s
> live in `pg_dumpall --globals-only`. The drill must not describe itself as
> proving "the privilege model round-trips" without qualification — it proves
> that every privilege THE DUMP CARRIES round-trips, and that a restore into a
> cluster whose roles are absent is a different failure this recipe does not
> cover. Say which, in the comment. A gate that overstates its subject is the
> defect this finding is about, and it would be re-committed in the act of fixing
> it.


> **RESOLVED (2) — `CATALOG_SQL` must change, and the spec's "do not touch it"
> is lifted for this one substitution.** Reported by the implementing agent,
> verified here.
>
> `pg_get_constraintdef(oid)` is **not round-trip stable**. `BETWEEN` deparses to
> a nested `AND` tree that the restore reparses flat, so the drill fails on a
> clean database with no mutation at all:
>
> ```
> -CHECK ((((width >= 1) AND (width <= 8000)) AND ((height >= 1) AND (height <= 8000))))
> +CHECK (((width >= 1) AND (width <= 8000) AND ((height >= 1) AND (height <= 8000))))
> ```
>
> The source is `CHECK (width BETWEEN 1 AND 8000 AND height BETWEEN 1 AND 8000)`
> (`migrations/001_initial_schema.up.sql:4387`). An instrument that cries wolf on
> an untouched database gets switched off, which is this finding's own subject.
>
> **Change the shared `CATALOG_SQL` to `pg_get_constraintdef(c.oid, true)`.** Do
> not fork a restore-only catalog: two instruments answering one question in two
> dialects is the drift this repository keeps finding, and `schema-drift` reads
> the same constant (`Makefile:520,524`).
>
> Three things were measured before accepting it, because the fix touches the
> comparison the whole finding rests on — a normaliser that hides a real
> difference would re-commit the false-green in the act of repairing it:
>
> - **Meaning-bearing parentheses survive.** `CHECK ((a OR b) AND c)` prints with
>   its parens; `CHECK (a OR (b AND c))` prints as `a OR b AND c`, because `AND`
>   binds tighter and those parens say nothing. Pretty mode normalises
>   REPRESENTATION and preserves MEANING, which is the comparison this drill
>   actually wants.
> - **A genuine change is still caught.** Widening the same constraint from 8000
>   to 9999 shows as a plain textual difference.
> - **No new whitespace noise.** Exactly one constraint of 762 in `public`
>   contains a newline, and it does under BOTH representations — pretty mode adds
>   none.
>
> **State the version caveat in the Makefile comment.** PostgreSQL documents
> pretty-printed output as intended for display and not guaranteed stable across
> major versions. That is safe HERE and the reason must be written down, or the
> next reader will either panic or, worse, reuse this comparison across versions:
> both instruments compare two databases **in the same cluster** — `restore-drill`
> restores into a throwaway created by `docker compose exec db createdb`, and
> `schema-drift` builds its reference the same way — so both sides are always
> deparsed by one server binary. A comparison spanning two server versions is a
> different question this recipe does not answer.

## Root cause

The instrument was written to the shape of its own comment rather than to its question. The recipe's question is "does a dump of this database come back as this database", and it answers only "did the tables come back with roughly the row counts the statistics collector reports". Three separate design errors compound: (a) the schema half was described in the comment and never implemented, even though `CATALOG_SQL` — the exact text-form catalog diff it needed — already existed twenty lines above it for `schema-drift`; (b) `n_live_tup` was reached for because it is a one-line query, and it is an estimate derived from `reltuples`, which cannot answer a question about whether every row came back; (c) `|| true` and `>/dev/null 2>&1` removed the strongest signal in the whole recipe — the restore tool's own verdict — leaving a "has at least one table" smoke test as the sole proof anything was restored. This is CLAUDE.md's own #31 (a comment stating the rule its own code does not implement) applied to a gate, and `.claude/rules/review-process.md`'s "Verification instruments are review targets in their own right" is the rule that says an instrument is not exempt.


## Reproduction — the evidence this rests on

EXECUTED, not argued. All line numbers verified against the clean tree (37d20e3).

The cited lines say what the summary says. `Makefile:503-531`:
- 516: `pg_restore -d "$$copyurl" --no-owner --no-privileges /tmp/goen-drill.dump >/dev/null 2>&1 || true;`
- 517-518: the only structural assertion — `SELECT count(*) FROM pg_class ... relkind='r'` piped to `grep -qv '^0$$'`, i.e. "at least one table".
- 520: `counts="SELECT relname||' '||n_live_tup FROM pg_stat_user_tables ORDER BY relname"` — an ESTIMATE.
- 521: ANALYZE is run against the LIVE database as a side effect of the check.
- 525: on a clean diff it prints `restore-drill: PASS — the dump restores to the same schema and the same rows`.
- No use of `$(CATALOG_SQL)` (defined at Makefile:435 and used only by `schema-drift`, 476/480) anywhere in the recipe. The schema is never compared.
- The Makefile's own comment at 500-502 makes the same false claim as CLAUDE.md: "asks two questions: does the restored SCHEMA match migrations/, and did every table come back with the same number of rows."
- CLAUDE.md:1764-1768 states it, and CLAUDE.md:1267 tabulates it.

REPRODUCTION OF THE FALSE GREEN (ran the recipe's own logic by hand against the dev cluster; both throwaways dropped afterwards, tree clean):
  pg_dump goen -Fc; createdb goen_drill_probe; pg_restore --no-owner --no-privileges  → then dumped that quiesced copy into goen_drill_probe2 and mutated the restored copy:
    psql copy -c "DROP INDEX addresses_user_id_idx"                                  → DROP INDEX
    psql copy -c "ALTER TABLE order_lines DROP CONSTRAINT order_lines_quantity_in_range" → ALTER TABLE
  then ran the drill's exact comparison (ANALYZE both; `relname||' '||n_live_tup` diff):
    DRILL VERDICT: PASS — "the dump restores to the same schema and the same rows"
  while the catalog genuinely differed:
    < constraint order_lines order_lines_quantity_in_range
    < index addresses_user_id_idx
  A restored database missing a CHECK and an index is declared identical. In a schema whose entire enforcement model is 245 CHECKs / 62 unique indexes / 39 triggers, that is the whole failure mode the drill exists to exclude.

THREE THINGS THE SUMMARY UNDERSTATES OR GETS SLIGHTLY WRONG:
1. `|| true` is guarding nothing here. Measured: `pg_restore -d ... --no-owner --no-privileges` exits 0 with ZERO stderr lines against this cluster, and `pg_restore -d ... --no-owner` (privileges KEPT) also exits 0 with zero stderr lines — the roles are cluster-level and already exist. So the swallow costs the drill its exit status and buys nothing measurable.
2. `--no-privileges` is a second, unnamed hole: the drill deliberately discards every GRANT before comparing, so a restore that lost `store`'s privileges passes. That is CLAUDE.md trap #21 from the backup side ("nothing in the test suite reached it, because every suite connects as the OWNER"). Measured that the ACL question is askable and noise-free: comparing ACL entries normalised with `acldefault()` and excluding grantee = owner gives NORMALISED ACLs IDENTICAL between live and a privileges-restored copy, and `REVOKE SELECT ON orders FROM store` on the copy produces exactly one diff line (`- tableacl orders store=r/goen`).
3. The row half is also RACY, which I hit accidentally: the first run of the real comparison against the live `goen` (dev server running) went red purely because two carts were created between the dump and the count — `- carts 7 / + carts 6`. The live counts are read AFTER the dump, outside its snapshot. A drill that goes red on ordinary traffic is a drill that gets re-run until it is green, which is how a real red gets waved through.
4. At dev-seed size `n_live_tup` and exact `count(*)` are byte-identical (measured: `diff` empty over all tables), so the wrong instrument is currently invisible — it is wrong by construction, not by symptom today. See tests_required for what can and cannot be shown red.

Also verified the replacement machinery works before specifying it: a REPEATABLE READ session held open on a FIFO exported snapshot `00000039-0000001C-1`, `pg_dump --snapshot=` succeeded against it, and the exact-count query (`query_to_xml` per table) ran in the same session and returned per-table counts. `git status --porcelain` empty at finish; the three probe databases are dropped.


## Blast radius

Nobody is harmed on the site — this is a verification instrument, and its blast radius is a belief. `make restore-drill` is the only thing in the repository that claims goen's backups come back, and goen's product photography lives in PostgreSQL, so the dump is the only copy of the catalogue's images as well as its data. Today the drill prints PASS for a restored copy that is missing constraints, indexes, triggers and every GRANT — i.e. exactly the classes of loss a restore actually suffers (a `--section=data` dump, a restore into a cluster whose roles do not exist, a pg_restore that failed halfway and was swallowed by `|| true`, `--no-privileges` baked into somebody's runbook because that is what the drill does). On the day a restore is needed, the operator would promote a database whose enforcement layer is partly absent: the 245 CHECKs and 39 rule triggers are where money, stock and history are actually protected, and none of goen's Go-side guards would notice their absence — every integration suite connects as the OWNER, and the app never re-reads the catalogue. Silent in both directions: the drill cannot go red for a schema loss, and it goes red for ordinary concurrent traffic, which trains its readers to re-run it.


## Fix

Two files: `Makefile` and `CLAUDE.md`. No Go, no SQL migration, nothing generated.

DECISION FIRST — WHAT THE DRILL COMPARES AGAINST, AND WHY NOT schema-drift's REFERENCE.
Do NOT build a reference database from `migrations/` inside `restore-drill`. The drill's question is "does a dump of THIS database come back as this database"; whether this database still matches `migrations/` is `make schema-drift`'s question (Makefile:464-490), and it is a different failure with a different meaning (CLAUDE.md: "A difference is not automatically a defect — it is the signal that 001 has stopped being the whole truth"). Building a second reference inside the drill would duplicate schema-drift's machinery AND make the backup drill red for a drift that has nothing to do with the backup. Compare the restored COPY against the LIVE database, and let the two targets compose: schema-drift proves live == migrations/, restore-drill proves copy == live. The reuse is the shared `$(CATALOG_SQL)` macro (Makefile:435-447), which needs no change and must not be forked.

1) TWO NEW MACROS, beside `CATALOG_SQL` (insert after Makefile:447).

# Exact per-table row counts, as text a diff can read.
#
# NOT n_live_tup: that is an ESTIMATE derived from reltuples, and a backup
# drill may not answer "did every row come back" with an estimate. It also
# needed an ANALYZE, which made the instrument write to the database it audits.
EXACT_COUNTS_SQL := \
  "SELECT c.relname||' '||(xpath('/row/c/text()', \
        query_to_xml(format('select count(*) as c from public.%I', c.relname), false, true, '')))[1]::text::bigint \
     FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace \
    WHERE n.nspname='public' AND c.relkind='r' \
    ORDER BY 1"

# Who may do what, normalised so an owner's implicit ACL and an explicit one
# compare equal. A restore that loses its GRANTs is trap #21 from the backup
# side: the storefront 500s on every page and no suite can see it, because
# every suite connects as the OWNER.
ACL_SQL := \
  "SELECT 'table'||chr(9)||c.relname||chr(9)||a::text \
     FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, \
          LATERAL unnest(coalesce(c.relacl, acldefault('r', c.relowner))) a \
    WHERE n.nspname='public' AND c.relkind IN ('r','v') \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(c.relowner) \
   UNION ALL \
   SELECT 'function'||chr(9)||p.proname||chr(9)||a::text \
     FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, \
          LATERAL unnest(coalesce(p.proacl, acldefault('f', p.proowner))) a \
    WHERE n.nspname='public' \
      AND split_part(a::text,'=',1) <> pg_get_userbyid(p.proowner) \
   ORDER BY 1"

Both are measured working against PostgreSQL 18 on this cluster. The `split_part(...) <> owner` filter is what makes `--no-owner` safe to keep: ownership of a throwaway is not the question, and excluding the owner's own ACL entry is also what removes the one line of noise I measured (`schema_migrations goen=arwdDxtm/goen` vs a NULL relacl meaning the same thing).

2) REPLACE Makefile:492-531 ENTIRELY.

Comment (492-502) — say the three questions and name both mutations:

# Prove the backup can be restored. Not that one exists — that a dump of this
# database comes back as this database.
#
# goen's images live IN PostgreSQL (the recorded internal/media decision), so
# the database is the only copy of the catalogue's photography as well as its
# data. A backup nobody has restored is a belief.
#
# It dumps, restores into a throwaway, and asks THREE questions of the copy
# against the database it was dumped from: does the CATALOG agree (the same
# $(CATALOG_SQL) schema-drift reads — constraints, indexes, columns,
# triggers), do the GRANTs agree, and did every table come back with the same
# number of rows. Whether the live schema matches migrations/ is
# schema-drift's question; the two compose.
#
# Rows alone passes on a restore that lost an index, a CHECK or a GRANT.
# Schema alone passes on a dump that lost every row — the mutation is
# `pg_dump -s`. Each half is recorded red in CLAUDE.md.
#
# The counts are EXACT and are read inside the DUMP'S OWN exported snapshot,
# so a write landing during the drill cannot make it red. A drill that goes
# red on ordinary traffic is a drill people re-run until it is green.

Recipe — the important properties, in order:

a. Keep the `GOEN_DATABASE_URL` guard (505) and the `base`/`query`/`copyurl` URL splitting (509-511) verbatim.

b. Hold the live snapshot open, and take the live counts INSIDE it. Verified prototype (psql 18 client):

	work=$$(mktemp -d); mkfifo "$$work/hold"; \
	psql "$$GOEN_DATABASE_URL" -At -q -v ON_ERROR_STOP=1 -f "$$work/hold" > "$$work/held.txt" & \
	snapper=$$!; \
	exec 9>"$$work/hold"; \
	printf "SET idle_in_transaction_session_timeout='10min';\nBEGIN ISOLATION LEVEL REPEATABLE READ;\nSELECT pg_export_snapshot();\n" >&9; \
	snap=; for i in $$(seq 1 100); do snap=$$(head -1 "$$work/held.txt"); [ -n "$$snap" ] && break; sleep 0.1; done; \
	test -n "$$snap" || { echo 'restore-drill: could not open a snapshot on the live database' >&2; exit 3; }; \
	pg_dump "$$GOEN_DATABASE_URL" --snapshot="$$snap" -Fc -f "$$work/goen.dump"; \
	printf '%s;\nCOMMIT;\n' $(EXACT_COUNTS_SQL) >&9; \
	exec 9>&-; wait "$$snapper"; snapper=; \
	tail -n +2 "$$work/held.txt" | sort > "$$work/rows-live.txt"; \
	test -s "$$work/rows-live.txt" || { echo 'restore-drill: no live row counts were read' >&2; exit 3; };

	Notes an implementer needs: `-q` is what suppresses the SET/BEGIN/COMMIT tags, so line 1 of held.txt is the snapshot id and the rest are counts — do not drop it. `printf '%s;\n' $(EXACT_COUNTS_SQL)` is safe even though the query contains `%I`: only the FORMAT string is interpreted, the macro is an argument. `SET idle_in_transaction_session_timeout` is not decoration — the held transaction pins xmin on the live database and an abandoned drill must not block vacuum forever.

c. Extend the existing trap (508) so it also kills the holder and removes the workdir, and use $$work instead of the fixed /tmp/goen-drill-*.txt paths (two concurrent drills currently share those files):

	trap 'test -n "$$snapper" && kill "$$snapper" 2>/dev/null || true; \
	      docker compose exec -T db dropdb -U goen --if-exists --force "$$copy" >/dev/null 2>&1 || true; \
	      rm -rf "$$work"' 0 HUP INT TERM;

d. Restore, and BELIEVE pg_restore. Delete `>/dev/null 2>&1 || true` and delete `--no-privileges`; keep `--no-owner`; add `--exit-on-error`:

	pg_restore -d "$$copyurl" --no-owner --exit-on-error "$$work/goen.dump" > "$$work/restore.log" 2>&1 \
	  || { echo 'restore-drill: FAIL — pg_restore refused the dump.' >&2; cat "$$work/restore.log" >&2; exit 1; }; \
	if [ -s "$$work/restore.log" ]; then echo 'restore-drill: pg_restore said:'; cat "$$work/restore.log"; fi;

	On the benign-noise question the `|| true` presumably existed for: measured, there is none — exit 0 and zero stderr lines both with and without `--no-privileges`, because the roles are cluster-level and migration 001 has already created them. If a foreign cluster ever does produce owner/role noise, the honest handling is to create the roles before restoring (or run 001's role DO block), NOT to swallow the status. If a filter is ever unavoidable, it must be fail-CLOSED: grep the log for the one allowed message, and fail on any line that is not it. Never `|| true`.

e. Delete the "has at least one table" check (517-518). It is subsumed: an empty copy diffs against ~1,100 catalog lines. Replace it with schema-drift's two proven guards (Makefile:477-479 — the ones that caught that target's own false-green): `test -s` on the live catalog file, and

	psql "$$copyurl" -At -c "SELECT current_database()" | grep -qx "$$copy" \
	  || { echo 'restore-drill: the copy URL does not point at the throwaway, so this would compare the live database with itself' >&2; exit 3; };

f. Delete both ANALYZE calls (521). Nothing needs them now, and the instrument stops writing to the database it audits.

g. Three comparisons, all three run and all three reported, then one exit. This deliberately differs from `verify`'s stop-at-first: these are three questions about ONE artefact, and an operator holding a broken dump should see everything wrong with it at once.

	fail=0; \
	psql "$$GOEN_DATABASE_URL" -At -c $(CATALOG_SQL) | sort > "$$work/schema-live.txt"; \
	psql "$$copyurl"           -At -c $(CATALOG_SQL) | sort > "$$work/schema-copy.txt"; \
	test -s "$$work/schema-live.txt" || { echo 'restore-drill: the live catalog came back EMPTY; nothing was compared' >&2; exit 3; }; \
	diff -u "$$work/schema-live.txt" "$$work/schema-copy.txt" > "$$work/schema.diff" \
	  || { fail=1; echo 'restore-drill: FAIL — the restored SCHEMA is not the schema that was dumped.'; \
	       echo '  -  is the live database; +  is what came back.'; cat "$$work/schema.diff"; }; \
	psql "$$GOEN_DATABASE_URL" -At -c $(ACL_SQL) | sort > "$$work/acl-live.txt"; \
	psql "$$copyurl"           -At -c $(ACL_SQL) | sort > "$$work/acl-copy.txt"; \
	test -s "$$work/acl-live.txt" || { echo 'restore-drill: no live GRANTs were read; nothing was compared' >&2; exit 3; }; \
	diff -u "$$work/acl-live.txt" "$$work/acl-copy.txt" > "$$work/acl.diff" \
	  || { fail=1; echo 'restore-drill: FAIL — the restored copy does not carry the same GRANTs.'; \
	       echo '  A restore that loses store'"'"'s privileges 500s on every storefront page,'; \
	       echo '  and no suite can see it because every suite connects as the OWNER.'; cat "$$work/acl.diff"; }; \
	psql "$$copyurl" -At -c $(EXACT_COUNTS_SQL) | sort > "$$work/rows-copy.txt"; \
	diff -u "$$work/rows-live.txt" "$$work/rows-copy.txt" > "$$work/rows.diff" \
	  || { fail=1; echo 'restore-drill: FAIL — the restored copy does not hold the rows that were dumped.'; \
	       echo '  -  is the live database at the dump'"'"'s snapshot; +  is what came back.'; cat "$$work/rows.diff"; }; \
	test "$$fail" -eq 0 || exit 1; \
	echo 'restore-drill: PASS — same catalog, same grants, same rows'

	The PASS line must name what was actually asked. The current one ("the same schema and the same rows") is the sentence this finding is about.

3) CLAUDE.md — correct the two places that vouch for the old behaviour.

Line 1267 (commands table), replace with:
| `make restore-drill` | Dump, restore into a throwaway, and compare catalog, grants and exact row counts |

Lines 1762-1768, replace the paragraph with:

**`make restore-drill` proves the backup comes back.** goen's images live IN
PostgreSQL, so the database is the only copy of the catalogue's photography as well
as its data, and a backup nobody has restored is a belief. It dumps, restores into a
throwaway, and asks THREE questions of the copy against the database it was dumped
from: does the CATALOG agree — constraints, indexes, columns and triggers, through
the same `CATALOG_SQL` `make schema-drift` reads — do the GRANTs agree, and did every
table come back with the same number of rows. Whether the live schema still matches
`migrations/` is `schema-drift`'s question, and the two compose. Schema alone passes
on a dump that lost every row, and the mutation that proved that half is `pg_dump -s`;
ROWS ALONE passes on a restore that lost an index, a CHECK or a GRANT, which is what
this drill did for as long as it existed — it compared `n_live_tup` and never once
read the catalog, so a copy missing `addresses_user_id_idx` and
`order_lines_quantity_in_range` printed PASS. The counts are EXACT `count(*)`, read
inside the dump's own exported snapshot: `n_live_tup` is an estimate, and an estimate
cannot answer "did every row come back", while counting the live database after the
dump made the drill red for ordinary traffic. `pg_restore`'s exit status is a failure,
never `|| true`. The GRANT question is trap #21 from the backup side: a restore that
loses `store`'s privileges is a database every suite passes and every storefront page
500s on.

4) DO NOT add restore-drill to `verify` or `verify-all`. It needs Docker, a live `GOEN_DATABASE_URL` and quiet-enough traffic; it stays alongside `schema-drift` for the reason that target states.


## The lock, and how to see it fail first

There is no Go test here — the lock IS the target, and `.claude/rules/testing.md`'s rule applies to it: each half must be SEEN red, and each mutation must be seen to APPLY (CLAUDE.md false-green mode #3: "The mutation must be seen to apply, not assumed"). Run every mutation against the RESTORED COPY, from a shell that has sourced `.env`, and paste the output into the PR thread.

Set-up used for the mutations (measured working): let the drill run to the point where the copy exists, or reproduce it by hand — `pg_dump "$GOEN_DATABASE_URL" -Fc -f /tmp/d.dump; docker compose exec -T db createdb -U goen goen_drill_mut; pg_restore -d "postgres://goen:goen@127.0.0.1:5433/goen_drill_mut?sslmode=disable" --no-owner --exit-on-error /tmp/d.dump`. Drop the database afterwards.

M1 — SCHEMA half, index. On the copy: `DROP INDEX addresses_user_id_idx;` (proven to exist; `DROP INDEX` is the tag that proves it applied). Re-run the comparison. REQUIRED: FAIL naming the schema question, with `-index<TAB>addresses<TAB>addresses_user_id_idx<TAB>CREATE INDEX ...` in the diff. Proof the mutation is not vacuous: the OLD recipe prints PASS for exactly this — I ran it and it did.

M2 — SCHEMA half, CHECK. On the copy: `ALTER TABLE order_lines DROP CONSTRAINT order_lines_quantity_in_range;` (proven to exist; `ALTER TABLE` proves it applied — a `NOTICE ... does not exist, skipping` means you mutated nothing, which is how M1's first attempt in this review failed silently). REQUIRED: FAIL with `-constraint<TAB>order_lines<TAB>order_lines_quantity_in_range<TAB>CHECK ...`.

M3 — SCHEMA half, TRIGGER. Pick a real one: `SELECT trigger_name, event_object_table FROM information_schema.triggers WHERE trigger_schema='public' LIMIT 1;` then `DROP TRIGGER <name> ON <table>;` on the copy. REQUIRED: FAIL with that trigger's line missing. This one exists because `CATALOG_SQL`'s trigger arm reads `information_schema.triggers` and the drill has never exercised it; do not skip it on the assumption the index case covers it.

M4 — GRANT half. On the copy: `REVOKE SELECT ON orders FROM store;`. REQUIRED: FAIL on the GRANT question, and the diff must be exactly `-table<TAB>orders<TAB>store=r/goen` (measured). Proof the mutation is not vacuous: with `--no-privileges` on the restore, this question cannot be asked at all, because the copy carries no GRANTs to lose.

M5 — ROW half, kept from the record. Restore a `pg_dump -s` dump into the copy. REQUIRED: FAIL on the row question with every table at 0 — and, importantly, PASS on the schema and GRANT questions, which is what proves the three questions are independent rather than one question reported three times.

M6 — EXIT-STATUS half. `head -c 4096 /tmp/d.dump > /tmp/bad.dump` and point the restore at it. REQUIRED: the drill stops AT the restore step, prints `pg_restore refused the dump` and pg_restore's own stderr, and exits 1 — it must not reach any comparison. Proof it is not vacuous: the old line answers 0 for this input and walks straight into the comparisons.

M7 — SNAPSHOT half (the race I reproduced). With the dev server running, place an order through the site (or `INSERT INTO carts` in a separate session) DURING the dump — easiest deterministic version: hold the drill after `pg_export_snapshot` (add a temporary `sleep 20`), write a row from another psql in that window, then let it continue. REQUIRED: PASS. The pre-fix recipe goes red here — I hit it by accident (`- carts 7 / + carts 6`) — and a check that is red for a reason unrelated to what it audits is a check that gets re-run rather than read.

M8 — the one that CANNOT be shown red, recorded honestly rather than dressed up (the call `internal/twofactor`'s constant-time compare and `/admin/messages`' clock already get in this repository). At dev-seed size `n_live_tup` and exact `count(*)` are byte-identical over every table — I diffed them and the diff is empty — so no data mutation distinguishes the two instruments at this scale: any row loss is caught by both. The change from estimate to exact is justified by construction (`n_live_tup` is `reltuples`-derived and ANALYZE samples at most 30,000 pages, so it diverges on any table past ~235MB — which is precisely `media_objects`, the table holding the photography this drill exists for) and by the side effect it removes (the old check ANALYZEd the live database in order to measure it). Record M8 as GREEN/undistinguishable with that reasoning, or run and record the one-off demonstration: in a throwaway, build a table past 30,000 pages, DELETE a fraction, ANALYZE, and show `n_live_tup <> count(*)`.

PROCEDURE, not optional:
- Run `make restore-drill` TWICE on the unmutated tree and require PASS both times (CLAUDE.md #22: run a gate twice before believing it — the FIFO/snapshot machinery is new and is the part most likely to be order-dependent).
- After an INTERRUPTED run (Ctrl-C during the dump), assert the drill left nothing behind: `SELECT datname FROM pg_database WHERE datname LIKE 'goen_restore_drill_%'` returns nothing, and `SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction'` is 0. The held snapshot transaction is the new hazard this fix introduces and the trap plus `idle_in_transaction_session_timeout` are what close it; both must be seen working, not assumed.
- Finish with `git status --porcelain` empty and every `goen_drill_*` / `goen_restore_drill_*` database dropped.
