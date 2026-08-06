-- Claim a batch of due messages, taking a LEASE on each.
--
-- FOR UPDATE SKIP LOCKED alone is not enough, and this is the part that is easy
-- to get wrong: the row lock lives only for the duration of THIS statement.
-- The moment it returns, delivered_at is still NULL and a second worker's
-- `available_at <= now()` matches the same rows — measured, not theorised: two
-- concurrent drains delivered five messages twice.
--
-- So the claim also pushes available_at into the future. That is the lease: for
-- as long as it lasts the message is invisible to every other worker, and if
-- this one dies mid-delivery the lease expires and the message comes back. It
-- is what makes "at least once" a recovery rather than a leak.
--
-- SKIP LOCKED still earns its place, but it is now a THROUGHPUT property rather
-- than a correctness one: without it a second worker blocks until the first
-- claim's statement finishes, then finds the lease and takes nothing. Removing
-- it leaves every test green, which is recorded here rather than dressed up —
-- the lease is the guarantee, and this is what stops the two contending.
--
-- attempts rises on the CLAIM, not on success. A message whose handler panics
-- has still been attempted, and a counter that only rises on success counts
-- nothing about failures.
-- name: ClaimOutbox :many
WITH due AS (
    SELECT id FROM outbox_messages
    WHERE delivered_at IS NULL AND available_at <= now()
    -- Priority first, then age. A bulk send sits behind every transactional
    -- message written after it, which is the whole point: a receipt must not
    -- wait for a newsletter.
    ORDER BY priority, available_at
    LIMIT @batch_size::integer
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_messages m
SET attempts = m.attempts + 1,
    available_at = now() + @lease::interval
FROM due
WHERE m.id = due.id
RETURNING m.id, m.topic, m.payload, m.attempts;

-- name: MarkOutboxDelivered :exec
UPDATE outbox_messages SET delivered_at = now(), last_error = NULL WHERE id = $1;

-- Push a failed message into the future.
--
-- The backoff is computed by the caller and passed as a timestamp, because the
-- schedule is a policy decision and the query should not be the place somebody
-- has to look for it.
-- name: RescheduleOutbox :exec
UPDATE outbox_messages
SET available_at = @available_at, last_error = @last_error::text
WHERE id = $1;

-- Messages that have failed too many times, for a human to look at. A queue
-- with no way to see what is stuck is a queue that quietly stops working.
-- name: StuckOutbox :many
SELECT id, topic, dedupe_key, attempts, coalesce(last_error, '') AS last_error, available_at
FROM outbox_messages
WHERE delivered_at IS NULL AND attempts >= @min_attempts::integer
ORDER BY attempts DESC, available_at
LIMIT $1;

-- Delete delivered messages past their retention window.
--
-- DELIVERED only. A message that has exhausted MaxAttempts is not delivered, so
-- it is kept forever and /admin/health goes on listing it: sweeping a failure
-- would make the queue look healthy by forgetting what went wrong.
--
-- available_at rather than any other column, because the row has none that says
-- when it was DELIVERED past delivered_at itself — and delivered_at is the honest
-- clock here: retention is measured from when goen stopped needing the row.
-- name: SweepDeliveredMessages :execrows
DELETE FROM outbox_messages
WHERE delivered_at IS NOT NULL
  AND delivered_at < now() - sqlc.arg(retain)::interval;
