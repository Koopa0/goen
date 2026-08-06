-- Rebuild the co-purchase projection.
--
-- Returns how many pairs it wrote, so the worker can log something a person can
-- sanity-check rather than "done".
-- name: RefreshCopurchases :one
SELECT refresh_copurchases();
