-- Brings a restored demo snapshot forward to today. The history
-- seed/demo_history.sql writes ends the day before it ran. Restored k days
-- later, every time in the snapshot moves k days on the shop's calendar, so
-- the history ends yesterday again. Only times and order numbers change:
-- totals, stock and the ledgers stay as they were.
--
-- Run it after the restore and before goen starts, as a superuser (replica
-- mode needs one), naming the database and the day the snapshot was taken:
--
--     psql "$GOEN_DATABASE_URL" -X -v ON_ERROR_STOP=1 -v demo_database=<its name> -v anchor_day=YYYY-MM-DD -f seed/demo_shift.sql
--
-- k counts from the database's own date: the day the seed's home-delivery
-- rate takes effect, which seed/dev_catalog.sql puts at the shop's midnight of
-- the day it ran, seed/demo_history.sql moves to the day it ran, and every
-- shift moves with the rest. anchor_day must be that date. A snapshot taken that day
-- holds nothing written later, so nothing lands after today. One already
-- shifted is dated later than anchor_day; one taken on a later day than its
-- date may hold rows written after it, which k days would carry past today.
-- Both are refused.
--
-- It also refuses a database it was not named for, one the seed's catalogue
-- did not build, and one holding a payment that could be real: a cs_live_
-- session, or a succeeded payment that is neither a Stripe test session
-- (cs_test_) nor one of the history's (cs_demo_). The shift is a single DO
-- block, so a failure leaves the snapshot as it was restored. On the anchor
-- day it changes nothing.
--
-- Times keep their time of day, so a row written on the anchor day later than
-- the hour of the restore lies ahead of the clock until that hour. None of
-- those gates anything: the seed puts what does (the shipping versions and the
-- campaign windows) on the shop's midnight.

\set ON_ERROR_STOP on
\if :{?demo_database}
\else
\set demo_database ''
\endif
\if :{?anchor_day}
\else
\set anchor_day ''
\endif

SET client_min_messages = warning;
-- First, ahead of the column list's temporary table: migrations/001 takes
-- TEMPORARY from PUBLIC, so any other role would stop there on a bare
-- permission error.
DO $$
BEGIN
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        RAISE EXCEPTION 'run this as a superuser: replica mode needs one';
    END IF;
END
$$;

-- psql does not substitute its variables inside a DO block's body.
SET demo_shift.database = :'demo_database';
SET demo_shift.anchor_day = :'anchor_day';

\ir demo_shift_columns.sql

DO $$
DECLARE
    v_named  constant text := current_setting('demo_shift.database');
    v_given  constant text := current_setting('demo_shift.anchor_day');
    v_anchor date;
    v_dated  date;
    v_days   integer;
    v_foreign text;
    v_stmt   text;
    r        record;
BEGIN
    IF v_named = '' THEN
        RAISE EXCEPTION 'pass -v demo_database=<this database''s name> to shift it';
    END IF;
    IF v_named <> current_database() THEN
        RAISE EXCEPTION 'demo_database is %, not this database (%)', v_named, current_database();
    END IF;
    IF v_given = '' THEN
        RAISE EXCEPTION 'pass -v anchor_day=<the day the snapshot was taken, YYYY-MM-DD>';
    END IF;
    IF v_given !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' THEN
        RAISE EXCEPTION 'anchor_day is %, not a day written YYYY-MM-DD', v_given;
    END IF;
    v_anchor := v_given::date;
    IF v_anchor > shop_today() THEN
        RAISE EXCEPTION 'anchor_day % is after today (%)', v_anchor, shop_today();
    END IF;
    SELECT p.provider_ref INTO v_foreign
    FROM payments p
    WHERE p.provider_ref LIKE 'cs\_live\_%'
       OR (p.status = 'succeeded'
           AND p.provider_ref NOT LIKE 'cs\_test\_%' AND p.provider_ref NOT LIKE 'cs\_demo\_%')
    ORDER BY p.created_at, p.id
    LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION 'payment % is not a demo or test payment: this database may hold real money', v_foreign;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM inventory_movements WHERE reason = 'receipt' AND idempotency_key LIKE 'seed:%') THEN
        RAISE EXCEPTION 'this database holds no opening stock from seed/dev_catalog.sql: it is not the seeded demo';
    END IF;
    IF EXISTS (SELECT 1 FROM orders o JOIN payments p ON p.order_id = o.id
               WHERE p.provider_ref LIKE 'cs\_demo\_%' AND shop_day(o.placed_at) >= v_anchor) THEN
        RAISE EXCEPTION 'orders from the history were placed on or after %: this snapshot was already shifted, or anchor_day is not the day it was taken', v_anchor;
    END IF;

    v_days := shop_today() - v_anchor;
    IF v_days = 0 THEN
        RETURN;
    END IF;

    -- Off for this transaction only: the append-only triggers would refuse the
    -- ledgers' times, and updated_at would be stamped now.
    SET LOCAL session_replication_role = replica;
    -- So that k days are k days on the shop's calendar whatever zone the
    -- session is in.
    SET LOCAL TimeZone = 'Asia/Taipei';
    FOR v_stmt IN
        SELECT format('UPDATE %s SET %s', relid,
                      string_agg(format(CASE WHEN typ = 'date'::regtype THEN '%1$I = %1$I + %2$s'
                                             ELSE '%1$I = %1$I + make_interval(days => %2$s)' END,
                                        attname, v_days),
                                 ', '))
        FROM pg_temp.demo_time_column
        GROUP BY relid
    LOOP
        EXECUTE v_stmt;
    END LOOP;

    -- A day's counter and an order's number are unique, and checked row by
    -- row: moved forward latest first, none lands on one not yet moved.
    FOR r IN SELECT business_date FROM order_number_counters ORDER BY business_date DESC LOOP
        UPDATE order_number_counters SET business_date = business_date + v_days
        WHERE business_date = r.business_date;
    END LOOP;
    FOR r IN SELECT id FROM orders ORDER BY order_number DESC LOOP
        UPDATE orders
        SET order_number = 'GO-' || to_char(to_date(substr(order_number, 4, 6), 'YYMMDD') + v_days, 'YYMMDD')
                        || substr(order_number, 10)
        WHERE id = r.id;
    END LOOP;
END
$$;
