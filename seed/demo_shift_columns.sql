-- The columns seed/demo_shift.sql moves, in replica mode with foreign keys and
-- the append-only triggers off: every timestamptz, timestamp and date column
-- in public, read from the catalogue so that a column added later moves too.
--
-- Left out are the tables whose times bound a credential or a retention sweep
-- (sessions, the password-reset, email and newsletter links, carts, checkout
-- attempts, guest order access), so that nothing which expired comes back to
-- life, and order_number_counters, whose days the script moves one at a time.
-- internal/db/demoshift_integration_test.go holds the file to that.
--
-- Each table moves in one UPDATE, and PostgreSQL checks a unique index row by
-- row, so two rows exactly k days apart under a unique index on a moved column
-- collide and the shift rolls back. The only such index today is
-- shipping_method_versions (method_id, effective_at), with one version per
-- method in the seed; a date-keyed one would need moving like the counters.
CREATE TEMP TABLE demo_time_column AS
SELECT c.oid::regclass AS relid, a.attname, a.atttypid::regtype AS typ
FROM pg_class c
JOIN pg_attribute a ON a.attrelid = c.oid
WHERE c.relnamespace = 'public'::regnamespace AND c.relkind = 'r'
  AND a.attnum > 0 AND NOT a.attisdropped AND a.attgenerated = ''
  AND a.atttypid IN ('timestamptz'::regtype, 'timestamp'::regtype, 'date'::regtype)
  AND c.relname NOT IN ('sessions', 'password_reset_tokens', 'email_verifications', 'newsletter_confirmations',
                        'carts', 'cart_items', 'checkout_attempts', 'order_access_grants',
                        'order_number_counters');
