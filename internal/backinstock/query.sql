-- How many alerts one call deleted. The function lists only alerts whose notice
-- already went out, and a batch at a time.
-- name: SweepNotifiedStockNotifications :one
SELECT sweep_notified_stock_notifications(@retain::interval, @batch::integer)::bigint AS deleted;
