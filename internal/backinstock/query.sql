-- Idempotent through the partial unique index stock_notifications_pending_key, so
-- somebody notified about one restock may ask again for the next.
-- Lock the variant while deciding it is out of stock. A concurrent restock then
-- either waits and claims this row, or commits first and makes PostgreSQL recheck
-- the predicate so no permanently-late pending notice is inserted.
-- name: RequestStockNotice :one
WITH eligible AS MATERIALIZED (
    SELECT lock_stock_notice_variant(@variant_id, @slug::text) AS id
), inserted AS (
    INSERT INTO stock_notifications (variant_id, user_id, email, locale)
    SELECT id, @user_id, @email::text, @locale FROM eligible WHERE id IS NOT NULL
    ON CONFLICT (variant_id, lower(email)) WHERE notified_at IS NULL DO NOTHING
    RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM eligible WHERE id IS NOT NULL)::boolean AS eligible;

-- How many alerts one call deleted. The function lists only alerts whose notice
-- already went out, and a batch at a time.
-- name: SweepNotifiedStockNotifications :one
SELECT sweep_notified_stock_notifications(@retain::interval, @batch::integer)::bigint AS deleted;
