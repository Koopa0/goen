-- Claim a batch of due messages, taking a LEASE on each. FOR UPDATE SKIP LOCKED
-- is not enough — that lock lives only for THIS statement — so pushing
-- available_at forward is what makes the claim exclusive.
-- name: ClaimOutbox :many
WITH due AS (
    SELECT id FROM outbox_messages
    WHERE delivered_at IS NULL
      AND dropped_at IS NULL
      AND blocked_at IS NULL
      AND available_at <= now()
      AND created_at > now() - @retain::interval
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

-- A serial batch can cross the payload deadline after the claim. Use the
-- database clock again before giving a handler its remaining send budget.
-- name: OutboxDeliveryWindow :one
SELECT date_part('epoch', created_at + sqlc.arg(retain)::interval - clock_timestamp())::float8 AS seconds_remaining
FROM outbox_messages
WHERE id = @id::uuid
  AND delivered_at IS NULL
  AND dropped_at IS NULL
  AND blocked_at IS NULL;

-- name: MarkOutboxDelivered :exec
UPDATE outbox_messages SET delivered_at = now(), last_error = NULL WHERE id = $1;

-- Push a failed message relative to the same database clock ClaimOutbox uses.
-- name: RescheduleOutbox :exec
UPDATE outbox_messages
SET available_at = now() + @backoff::interval, last_error = @last_error::text
WHERE id = $1;

-- Stop automatic retries for a repairable failure that exhausted its budget.
-- name: BlockOutbox :exec
UPDATE outbox_messages
SET blocked_at = now(),
    available_at = now(),
    last_error = @last_error::text
WHERE id = $1;

-- Messages blocked for operator action: poison redaction or exhausted attempts.
-- name: StuckOutbox :many
SELECT id, topic, dedupe_key, attempts, coalesce(last_error, '') AS last_error,
       blocked_at AS available_at,
       (payload <> '{}'::jsonb
        AND created_at > now() - @retain::interval) AS recoverable
FROM outbox_messages
WHERE delivered_at IS NULL
  AND dropped_at IS NULL
  AND blocked_at IS NOT NULL
ORDER BY blocked_at DESC
LIMIT $1;

-- Poison a message-local failure: stop automatic retries, purge payload to eliminate
-- secret retention, and record the fatal error. blocked_at is the terminal marker;
-- delivered_at stays NULL so /admin/health lists it for operator action.
-- name: PoisonOutbox :exec
UPDATE outbox_messages
SET attempts = greatest(attempts, @max_attempts::integer),
    blocked_at = now(),
    available_at = now(),
    payload = '{}'::jsonb,
    last_error = @last_error::text
WHERE id = $1;

-- Redact undelivered payloads past Retain from creation time. Never marks sent.
-- name: ExpireOutboxPayloads :execrows
UPDATE outbox_messages
SET payload = '{}'::jsonb,
    blocked_at = coalesce(blocked_at, now()),
    available_at = now(),
    last_error = CASE
        WHEN blocked_at IS NULL THEN coalesce(last_error, '') || ' [payload expired]'
        ELSE last_error
    END
WHERE delivered_at IS NULL
  AND dropped_at IS NULL
  AND payload <> '{}'::jsonb
  AND created_at <= now() - sqlc.arg(retain)::interval;

-- DELIVERED messages past retain, plus undelivered terminal rows past twice retain
-- from created_at (payload lifetime plus metadata retention).
-- name: SweepDeliveredMessages :execrows
DELETE FROM outbox_messages
WHERE (delivered_at IS NOT NULL AND delivered_at < now() - sqlc.arg(retain)::interval)
   OR (delivered_at IS NULL
       AND created_at < now() - sqlc.arg(retain)::interval * 2);
