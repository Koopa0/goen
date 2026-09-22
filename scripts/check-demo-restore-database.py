#!/usr/bin/env python3
"""Exercise the production restore script in an owned, CI-only PostgreSQL cluster."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "demo-restore-evidence"
CONTAINER = "goen-restore-" + uuid.uuid4().hex[:12]
IDENTITY = "postgresql:///restore_target?user=restore_owner"


def command(args, *, input=None, check=True):
    result = subprocess.run(args, input=input, text=True, capture_output=True, timeout=180)
    if check and result.returncode:
        raise RuntimeError(f"{args[:4]} exited {result.returncode}\n{result.stdout}\n{result.stderr}")
    return result


def docker(*args, **kwargs):
    return command(["docker", *args], **kwargs)


def sql(statement, database="restore_target", user="restore_owner"):
    return docker("exec", "-i", CONTAINER, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1",
                  "-U", user, "-d", database, input=statement).stdout.strip()


def copy_text(text, destination):
    docker("exec", "-i", CONTAINER, "sh", "-c", 'cat > "$1"', "sh", destination, input=text)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def state():
    # The brand row proves data replacement in the real application schema;
    # the fixture row and constraint distinguish rollback from a partial restore.
    return sql("""
SELECT 'brand|' || name FROM public.brands WHERE slug = 'restore-fixture';
SELECT 'probe|' || value FROM restore_probe.z_failure ORDER BY value;
SELECT 'check|' || conname FROM pg_constraint
 WHERE conrelid = 'restore_probe.z_failure'::regclass ORDER BY conname;
""")


def require_rollback(before, after):
    assert before == after, "rollback must preserve destination data and constraints"


def restore(label, snapshot, script="/tmp/restore-demo-db.sh"):
    docker("exec", CONTAINER, "sh", "-c", ": > /tmp/service-events")
    result = docker("exec", "-e", "PATH=/tmp/restore-bin:/usr/local/bin:/usr/bin:/bin",
                    "-e", f"GOEN_RESTORE_DATABASE_URL={IDENTITY}",
                    "-e", f"GOEN_RESTORE_SNAPSHOT=/tmp/{snapshot}",
                    CONTAINER, "bash", script, check=False)
    events = docker("exec", CONTAINER, "cat", "/tmp/service-events").stdout
    (OUT / f"{label}.log").write_text(result.stdout + result.stderr)
    (OUT / f"{label}.service-stub.log").write_text(events)
    (OUT / f"{label}.exit").write_text(str(result.returncode) + "\n")
    print(f"{label}: pg_restore/script exit={result.returncode}; service stub={events.strip()!r}", flush=True)
    return result, events


def failed_restore(label, script="/tmp/restore-demo-db.sh"):
    sql("UPDATE public.brands SET name = 'destination-before-failure' WHERE slug = 'restore-fixture';"
        "UPDATE restore_probe.z_failure SET value = 'destination-before-failure';")
    before = state()
    result, events = restore(label, "failure.dump", script)
    after = state()
    (OUT / f"{label}.before.txt").write_text(before + "\n")
    (OUT / f"{label}.after.txt").write_text(after + "\n")
    assert result.returncode != 0, "injected SQL constraint failure must fail restore"
    assert 'violates check constraint "restore_destination_rejected"' in result.stderr, result.stderr
    assert "COPY z_failure" in result.stderr, "failure must occur during real data COPY"
    assert events == "stop goen.service\n", "failed restore must not restart the service stub"
    assert "remains stopped pending operator recovery" in result.stderr
    require_rollback(before, after)


def main():
    if os.environ.get("CI") != "true":
        raise SystemExit("this disposable database drill runs only in CI")
    OUT.mkdir(exist_ok=True)
    image = next(line.split("image:", 1)[1].strip() for line in
                 (ROOT / "docker-compose.yml").read_text().splitlines() if "image:" in line)
    source = ROOT / "deploy/demo/restore-demo-db.sh"
    original = source.read_text()
    migration = ROOT / "migrations/001_initial_schema.up.sql"
    record = {"checkout": command(["git", "rev-parse", "HEAD"]).stdout.strip(),
              "pr_head": os.environ.get("PR_HEAD_SHA", ""), "image": image,
              "migration_sha256": digest(migration), "script_sha256": digest(source),
              "service_state": "systemctl stub only; no live service or deployment attestation"}
    try:
        docker("run", "--detach", "--name", CONTAINER, "--network", "none",
               "-e", "POSTGRES_HOST_AUTH_METHOD=trust", image)
        for _ in range(60):
            if docker("exec", CONTAINER, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", check=False).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("owned PostgreSQL container did not become ready")
        record["image_id"] = docker("inspect", "--format", "{{.Image}}", CONTAINER).stdout.strip()
        sql("CREATE ROLE restore_owner LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;"
            "ALTER ROLE restore_owner SET log_statement = 'all';"
            "CREATE DATABASE restore_source;", "postgres", "postgres")
        sql("CREATE DATABASE restore_target OWNER restore_owner;", "postgres", "postgres")
        sql(migration.read_text(), "restore_source", "postgres")
        sql("""
INSERT INTO public.brands (slug, name) VALUES ('restore-fixture', 'snapshot-brand');
CREATE SCHEMA restore_probe;
CREATE TABLE restore_probe.z_failure (value text NOT NULL);
INSERT INTO restore_probe.z_failure VALUES ('snapshot-probe');
""", "restore_source", "postgres")
        docker("exec", CONTAINER, "pg_dump", "-U", "postgres", "-d", "restore_source",
               "--format=custom", "--file=/tmp/success.dump")
        # A valid source constraint becomes false only at the destination. This
        # reaches COPY after earlier real DDL/data statements, not archive preflight.
        sql("""
CREATE FUNCTION restore_probe.accept_source_only() RETURNS boolean
 LANGUAGE sql STABLE AS $$ SELECT current_database() = 'restore_source' $$;
ALTER TABLE restore_probe.z_failure ADD CONSTRAINT restore_destination_rejected
 CHECK (restore_probe.accept_source_only());
""", "restore_source", "postgres")
        docker("exec", CONTAINER, "pg_dump", "-U", "postgres", "-d", "restore_source",
               "--format=custom", "--file=/tmp/failure.dump")
        for snapshot in ("success.dump", "failure.dump"):
            docker("cp", f"{CONTAINER}:/tmp/{snapshot}", str(OUT / snapshot))
            record[snapshot + "_sha256"] = digest(OUT / snapshot)
        dump_sql = docker("exec", CONTAINER, "pg_restore", "--no-owner", "--file=-", "/tmp/failure.dump").stdout
        (OUT / "failure-dump.sql").write_text(dump_sql)
        assert 0 <= dump_sql.find("COPY public.brands") < dump_sql.find("COPY restore_probe.z_failure"), "brand COPY must precede the injected failure"
        identity = sql("SELECT session_user, current_user; SELECT rolname, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls FROM pg_roles WHERE rolname = session_user; SELECT count(*) FROM pg_auth_members WHERE member = (SELECT oid FROM pg_roles WHERE rolname = session_user);")
        (OUT / "identity.txt").write_text(identity + "\n")
        assert identity == "restore_owner|restore_owner\nrestore_owner|f|f|f|f|f\n0", identity
        docker("exec", CONTAINER, "mkdir", "-p", "/tmp/restore-bin")
        copy_text(original, "/tmp/restore-demo-db.sh")
        copy_text('#!/bin/sh\nprintf "%s %s\\n" "$1" "$2" >> /tmp/service-events\n', "/tmp/restore-bin/systemctl")
        docker("exec", CONTAINER, "chmod", "+x", "/tmp/restore-bin/systemctl")
        result, events = restore("success", "success.dump")
        assert result.returncode == 0, result.stderr
        assert events == "stop goen.service\nstart goen.service\n"
        assert state() == "brand|snapshot-brand\nprobe|snapshot-probe"
        ownership = sql("SELECT datname, pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database(); SELECT count(*), count(*) FILTER (WHERE pg_get_userbyid(relowner) != session_user) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname IN ('public', 'restore_probe') AND c.relkind IN ('r','p','S','v');")
        (OUT / "ownership.txt").write_text(ownership + "\n")
        assert ownership.splitlines()[0] == "restore_target|restore_owner"
        objects, foreign = ownership.splitlines()[1].split("|")
        assert int(objects) > 60 and foreign == "0", ownership
        failed_restore("transactional-failure")
        print("PASS: real SQL failure rolls back earlier destination changes", flush=True)
        assert original.count("--single-transaction") == 1
        mutant = original.replace("--single-transaction", "")
        (OUT / "mutated-restore-demo-db.sh").write_text(mutant)
        copy_text(mutant, "/tmp/mutated-restore-demo-db.sh")
        try:
            failed_restore("without-transaction", "/tmp/mutated-restore-demo-db.sh")
        except AssertionError as error:
            if str(error) != "rollback must preserve destination data and constraints":
                raise
            (OUT / "watched-red.txt").write_text("FAIL: " + str(error) + "\n")
            print("watched-red: " + str(error), flush=True)
        else:
            raise AssertionError("removing production transaction flag did not break rollback assertion")
        result, events = restore("restored-success", "success.dump")
        assert result.returncode == 0 and events == "stop goen.service\nstart goen.service\n"
        assert state() == "brand|snapshot-brand\nprobe|snapshot-probe"
        failed_restore("restored-transactional-failure")
        record["result"] = "PASS: success, SQL-error rollback, transaction mutation red, restored pass"
        print(record["result"], flush=True)
    finally:
        record["finished_script_sha256"] = digest(source)
        (OUT / "record.json").write_text(json.dumps(record, indent=2) + "\n")
        logs = docker("logs", CONTAINER, check=False)
        (OUT / "postgres.log").write_text(logs.stdout + logs.stderr)
        docker("rm", "--force", "--volumes", CONTAINER, check=False)
    assert digest(source) == record["script_sha256"], "production source bytes changed"


if __name__ == "__main__":
    main()
