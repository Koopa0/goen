-- Claim a batch of due messages, taking a LEASE on each. FOR UPDATE SKIP LOCKED
-- is not enough — that lock lives only for THIS statement — so pushing
-- available_at forward is what makes the claim exclusive.
-- name: ClaimOutbox :many
WITH due AS (
    SELECT id FROM outbox_messages
    WHERE delivered_at IS NULL AND available_at <= now()
    -- Priority first, then age: a receipt must not wait for a newsletter.
    ORDER BY priority, available_at
    LIMIT @batch_size::integer
    FOR UPDATE SKIP LOCKED
)
-- attempts rises on the CLAIM, or it counts nothing about failures.
UPDATE outbox_messages m
SET attempts = m.attempts + 1,
    available_at = now() + @lease::interval
FROM due
WHERE m.id = due.id
RETURNING m.id, m.topic, m.payload, m.attempts;

-- name: MarkOutboxDelivered :exec
UPDATE outbox_messages SET delivered_at = now(), last_error = NULL WHERE id = $1;

-- Push a failed message relative to the same database clock ClaimOutbox uses.
-- name: RescheduleOutbox :exec
UPDATE outbox_messages
SET available_at = now() + @backoff::interval, last_error = @last_error::text
WHERE id = $1;

-- Messages that have failed too many times, for a human to look at.
-- name: StuckOutbox :many
SELECT id, topic, dedupe_key, attempts, coalesce(last_error, '') AS last_error, available_at
FROM outbox_messages
WHERE delivered_at IS NULL AND attempts >= @min_attempts::integer
ORDER BY attempts DESC, available_at
LIMIT $1;

-- DELIVERED only, and keyed on delivered_at: a message that exhausted its
-- attempts is kept so /admin/health lists it, and available_at moves forward on
-- every claim, so keying on that would delete unsent mail.
-- name: SweepDeliveredMessages :execrows
DELETE FROM outbox_messages
WHERE delivered_at IS NOT NULL
  AND delivered_at < now() - sqlc.arg(retain)::interval;
