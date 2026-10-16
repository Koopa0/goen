-- Zero rows means "send nothing", and the way to get there is the WHERE NOT
-- EXISTS: the address is already an active subscriber. The ON CONFLICT keeps one
-- live link per mailbox.
-- name: RequestNewsletterConfirm :one
INSERT INTO newsletter_confirmations (email, digest, expires_at)
SELECT sqlc.arg(email)::text,
       sqlc.arg(digest)::bytea,
       now() + sqlc.arg(ttl)::interval
WHERE NOT EXISTS (
    SELECT 1 FROM newsletter_subscribers s
    WHERE lower(s.email) = lower(sqlc.arg(email)::text)
      AND s.unsubscribed_at IS NULL
)
ON CONFLICT (lower(email)) DO UPDATE
    SET digest     = EXCLUDED.digest,
        expires_at = EXCLUDED.expires_at,
        created_at = now()
RETURNING id;

-- The token is SPENT by this statement, not by a check in Go before it: several
-- requests carrying one token all reach this line and exactly one deletes a row.
-- Expiry is judged by the clock that wrote expires_at.
-- name: SpendNewsletterConfirmation :one
DELETE FROM newsletter_confirmations
WHERE digest = $1 AND expires_at > now()
RETURNING email;

-- A request nobody answered. Only newsletter_confirmations is touched: a row in
-- newsletter_subscribers is a confirmed subscription or an opt-out, and the
-- opt-out is the record that stops the list emailing somebody again. Keyed on
-- created_at, which a repeated request moves forward together with expires_at.
-- name: DeleteStaleNewsletterConfirmations :execrows
DELETE FROM newsletter_confirmations
WHERE created_at < now() - sqlc.arg(retain)::interval;

-- Confirming is the only thing that clears a previous opt-out, so a form
-- submission by somebody else cannot undo it. The unsubscribe secret is KEPT
-- when an address rejoins, so every link ever mailed to it goes on working.
-- name: AddNewsletterSubscriber :one
INSERT INTO newsletter_subscribers (email, unsubscribe_token, locale)
VALUES ($1, $2, $3)
ON CONFLICT (lower(email)) DO UPDATE
    SET confirmed_at    = now(),
        unsubscribed_at = NULL,
        locale          = EXCLUDED.locale
RETURNING unsubscribe_token;

-- coalesce keeps the FIRST opt-out rather than moving it forward on every click.
-- Matching on the digest alone lets a caller tell "already off the list" from
-- "that link is not ours": the first returns a row, the second returns none.
-- name: UnsubscribeNewsletter :one
UPDATE newsletter_subscribers
SET unsubscribed_at = coalesce(unsubscribed_at, now())
WHERE unsubscribe_token = $1
RETURNING email;

-- Whether this address still wants the newsletter, asked at DELIVERY.
--
-- The send freezes the recipient list into one outbox row per subscriber, and
-- the queue drains at BulkPriority behind every transactional message — minutes
-- to hours for a real list. Nothing downstream re-read consent, so an
-- unsubscribe committing at any point in that window, or even after the send
-- transaction committed, still had its copy delivered. The read↔enqueue race is
-- the small half of that; the missing check is the whole of it.
-- name: StillSubscribed :one
SELECT EXISTS (
    SELECT 1 FROM newsletter_subscribers
    WHERE lower(email) = lower(@email::text) AND unsubscribed_at IS NULL
)::boolean AS subscribed;
