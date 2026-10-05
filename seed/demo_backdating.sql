-- The statements seed/demo_history.sql runs after each step to move what the
-- step wrote back to its simulated moment. They run in replica mode, with
-- foreign keys and the append-only triggers off, so they may change times and
-- nothing else; internal/db/demohistory_integration_test.go holds them to that.
--
-- now() is fixed for a transaction, so every step runs at the real clock. Per
-- table, one UPDATE of the rows this transaction wrote (xmin $5):
--   * a row it created (uuidv7 id past the transaction's marker, $4) has every
--     time from the transaction's start ($1) on moved back by $3, expiries
--     included;
--   * a row it only changed has the times it stamped, between $1 and $2, moved
--     back; times already in the future, such as a campaign's end, stay.
-- The one date counted from today is a points lot's expiry, from the day it was
-- earned ($1 - $3 is the simulated moment). An entry against a lot copies the
-- lot's, which has moved already.
CREATE TEMP TABLE demo_backdating AS
SELECT format('UPDATE %s SET %s WHERE xmin = $5::xid',
              t.relid::regclass,
              string_agg(format('%1$I = CASE WHEN %1$I >= $1 AND (%1$I <= $2 OR %2$s) THEN %1$I - $3 ELSE %1$I END',
                                a.attname, t.fresh),
                         ', ' ORDER BY a.attnum)) AS stmt
FROM (
    SELECT c.oid AS relid,
           CASE WHEN EXISTS (
               SELECT 1
               FROM pg_attribute i
               JOIN pg_attrdef d ON d.adrelid = i.attrelid AND d.adnum = i.attnum
               WHERE i.attrelid = c.oid AND i.attname = 'id' AND i.atttypid = 'uuid'::regtype
                 AND pg_get_expr(d.adbin, d.adrelid) = 'uuidv7()'
           ) THEN '(id > $4 AND uuid_extract_version(id) = 7)' ELSE 'false' END AS fresh
    FROM pg_class c
    WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
) t
JOIN pg_attribute a ON a.attrelid = t.relid
WHERE a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
  AND a.atttypid IN ('timestamptz'::regtype, 'date'::regtype)
GROUP BY t.relid, t.fresh
UNION ALL
SELECT 'UPDATE loyalty_entries SET expires_on = expires_on - (shop_day($1) - shop_day($1 - $3)) '
    || 'WHERE xmin = $5::xid AND id > $4 AND lot_id IS NULL';
