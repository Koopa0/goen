-- Reverses 001. Development only: goen has no production database to roll back.
--
-- Every table (except golang-migrate's own schema_migrations) and every
-- function in public is dropped by iterating the catalog. Listing them by hand
-- grew a bug each time a function was added — DROP TABLE ... CASCADE removes a
-- table's triggers but not the standalone functions beside it, and one was
-- always forgotten. `DROP SCHEMA public CASCADE` would be simpler still but
-- takes schema_migrations with it and breaks migrate's bookkeeping.
--
-- pg_trgm and the two cluster-global roles are left in place; the up migration
-- creates all three idempotently, and dropping the tables removes every grant
-- the roles held.

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
          -- Skip functions an extension owns (pg_trgm's operators); those go
          -- when the extension does, and dropping them by hand errors.
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = p.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE 'DROP FUNCTION IF EXISTS ' || obj.signature || ' CASCADE';
    END LOOP;
END
$$;
