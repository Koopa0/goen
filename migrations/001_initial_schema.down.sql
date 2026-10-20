-- Reverses 001, on a database that holds nothing: a shop's orders, customers
-- and catalogue are never undone by a migration, and a development database is
-- rebuilt with `make db-reset`, which drops and recreates it.
--
-- DROP TABLE CASCADE removes a table's triggers but not the functions beside it,
-- and DROP SCHEMA public CASCADE would take schema_migrations with it.

SET lock_timeout = '3s';
SET statement_timeout = '120s';

-- One statement, so a refusal stops every drop whatever runs the file: psql -f
-- carries on after a failed statement unless ON_ERROR_STOP is set.
DO $$
DECLARE
    obj record;
    has_rows boolean;
    holding text[];
BEGIN
    -- 001 inserts no rows, so any row was written by the shop or a seed. The
    -- first pass takes no lock, so a shop in use is refused without its tables
    -- being locked against its own traffic. The second locks each table as the
    -- DROP below would, so no row can arrive between the check and the drop.
    FOR pass IN 1..2 LOOP
        holding := '{}';
        FOR obj IN
            SELECT tablename FROM pg_tables
            WHERE schemaname = 'public' AND tablename <> 'schema_migrations'
            ORDER BY tablename
        LOOP
            IF pass = 2 THEN
                EXECUTE format('LOCK TABLE public.%I IN ACCESS EXCLUSIVE MODE', obj.tablename);
            END IF;
            EXECUTE format('SELECT EXISTS (SELECT FROM public.%I)', obj.tablename) INTO has_rows;
            IF has_rows THEN
                holding := holding || obj.tablename::text;
            END IF;
        END LOOP;
        IF cardinality(holding) > 0 THEN
            RAISE EXCEPTION 'migrations/001 is not reversed on a database that holds data, and nothing was dropped (rows in %). golang-migrate marked the database dirty before running this file: migrate force 1 records it as it is. A development database is rebuilt with make db-reset',
                    array_to_string(holding, ', ')
                USING ERRCODE = 'object_not_in_prerequisite_state';
        END IF;
    END LOOP;

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
        -- ROUTINE: DROP FUNCTION refuses a procedure, and harden_schema is one.
        EXECUTE 'DROP ROUTINE IF EXISTS ' || obj.signature || ' CASCADE';
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

    -- Back to PostgreSQL's built-in default for the owner's new functions. The
    -- database's own privileges (CONNECT, TEMPORARY) belong to the database, not
    -- to the schema, and stay as harden_schema() left them.
    ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO PUBLIC;
END
$$;
