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
    available_at = now() + @lease::interval,
    lease_owner = @lease_owner::uuid
FROM due
WHERE m.id = due.id
RETURNING m.id, m.topic, m.payload, m.attempts;

-- name: MarkOutboxDelivered :execrows
UPDATE outbox_messages SET delivered_at = now(), last_error = NULL, lease_owner = NULL
WHERE id = @id AND lease_owner = @lease_owner::uuid AND delivered_at IS NULL;

-- Push a failed message relative to the same database clock ClaimOutbox uses.
-- name: RescheduleOutbox :execrows
UPDATE outbox_messages
SET available_at = now() + @backoff::interval, last_error = @last_error::text,
    lease_owner = NULL
WHERE id = @id AND lease_owner = @lease_owner::uuid AND delivered_at IS NULL;

-- Messages that have failed too many times, for a human to look at.
-- name: StuckOutbox :many
SELECT id, topic, dedupe_key, attempts, coalesce(last_error, '') AS last_error, available_at
FROM outbox_messages
WHERE delivered_at IS NULL AND attempts >= @min_attempts::integer
ORDER BY attempts DESC, available_at
LIMIT $1;

-- Keyed on delivered_at, not available_at, which moves forward on every claim.
-- name: SweepDeliveredMessages :execrows
DELETE FROM outbox_messages
WHERE delivered_at IS NOT NULL
  AND delivered_at < now() - sqlc.arg(retain)::interval;

-- An undelivered message past the same window goes too: its payload can carry a
-- token that nothing will ever mail, and it may not outlive that token. Keyed on
-- created_at because available_at moves on every claim.
-- name: SweepUndeliveredMessages :many
DELETE FROM outbox_messages
WHERE delivered_at IS NULL
  AND created_at < now() - sqlc.arg(retain)::interval
RETURNING id, topic, attempts, created_at;
