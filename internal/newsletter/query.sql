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

-- name: CreateNewsletterIssue :one
INSERT INTO newsletter_issues (subject, body)
VALUES (@subject, @body)
RETURNING id;

-- Read inside the send's own transaction, so somebody who unsubscribes during a
-- send is either in the list or not, never half.
-- name: ActiveSubscribers :many
SELECT email, locale, unsubscribe_token
FROM newsletter_subscribers
WHERE unsubscribed_at IS NULL
ORDER BY confirmed_at;

-- The WHERE clause is the guard: an issue that has already gone out matches
-- nothing, so a double-submitted form sends once.
-- name: MarkNewsletterIssueSent :execrows
UPDATE newsletter_issues
SET sent_at = now(), recipients = @recipients, sent_by = @sent_by
WHERE id = @id AND sent_at IS NULL;

-- name: NewsletterIssues :many
SELECT i.id, i.subject, i.body, i.sent_at, i.recipients,
       coalesce(u.email, '') AS sent_by_email
FROM newsletter_issues i
LEFT JOIN users u ON u.id = i.sent_by
ORDER BY i.created_at DESC
LIMIT $1;

-- name: NewsletterIssue :one
SELECT id, subject, body, sent_at FROM newsletter_issues WHERE id = $1;

-- name: NewsletterCounts :one
SELECT
    count(*) FILTER (WHERE unsubscribed_at IS NULL)     AS active,
    count(*) FILTER (WHERE unsubscribed_at IS NOT NULL) AS unsubscribed,
    (SELECT count(*) FROM newsletter_confirmations WHERE expires_at > now()) AS awaiting
FROM newsletter_subscribers;

-- record_audit_event is granted to `admin` and to nobody else, which is why the
-- back office's newsletter store runs on the admin pool. The subject travels,
-- the BODY does not: audit_events is append-only and erase_user does not reach it.
-- name: RecordNewsletterSend :exec
SELECT record_audit_event(
    sqlc.narg(actor)::uuid, @action::text, 'newsletter_issues', @issue_id::uuid,
    NULL, @after, NULL
);