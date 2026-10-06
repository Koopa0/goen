-- Reverses 001. Development only.
-- DROP TABLE CASCADE removes a table's triggers but not the functions beside it,
-- and DROP SCHEMA public CASCADE would take schema_migrations with it.

SET lock_timeout = '3s';
SET statement_timeout = '120s';

DO $$
DECLARE
    obj record;
BEGIN
    FOR obj IN
        SELECT tablename FROM pg_tables
        WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
    LOOP
        EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(obj.tablename) || ' CASCADE';
    END LOOP;

    FOR obj IN
        SELECT p.oid::regprocedure AS signature FROM pg_proc p
        WHERE p.pronamespace = 'public'::regnamespace
          -- Extension-owned functions go when the extension does, and dropping
          -- them by hand errors.
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = p.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE 'DROP FUNCTION IF EXISTS ' || obj.signature || ' CASCADE';
    END LOOP;

    FOR obj IN
        SELECT t.oid::regtype AS name FROM pg_type t
        WHERE t.typnamespace = 'public'::regnamespace AND t.typtype = 'd'
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = t.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE 'DROP DOMAIN IF EXISTS ' || obj.name || ' CASCADE';
    END LOOP;
END
$$;
