-- A submission ASKS the address to join; it does not join it. Anybody can type
-- anybody's address into a footer form, so nothing is added to the list until
-- the mailbox answers.
--
-- Zero rows means "send nothing", and the way to get there is the WHERE NOT
-- EXISTS: the address is already an active subscriber. A second submission must
-- not mail them again, or the form is a way to deliver a hundred emails to
-- somebody by pressing a button a hundred times.
--
-- The ON CONFLICT is what keeps one live link per mailbox. Somebody who has
-- submitted three times holds one key, not three.
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

-- The token is SPENT by this statement, not by a check in Go before it. Several
-- requests carrying one token all reach this line and exactly one deletes a row;
-- a read-then-delete in the handler is a race every one of them wins.
--
-- Expiry is judged by the database's clock, the same clock that wrote
-- expires_at. Comparing it to Go's time.Now() compares two clocks.
-- name: SpendNewsletterConfirmation :one
DELETE FROM newsletter_confirmations
WHERE digest = $1 AND expires_at > now()
RETURNING email;

-- Confirming is what puts an address on the list, and the only thing that
-- clears a previous opt-out. A re-subscription therefore costs another trip
-- through the mailbox, which is the point: a form submission by somebody else
-- must not undo "stop emailing me".
--
-- The unsubscribe secret is KEPT when an address rejoins, not rotated.
--
-- It is stored as the token, so every copy of it that has ever been mailed goes
-- on working — which is the promise the link makes. Rotating it would silently
-- break the link in every issue already sitting in somebody's mailbox, and
-- storing a digest instead forces exactly that rotation: the caller cannot read
-- the live token back out of a hash, so it has nothing to mail but a new one.
-- name: AddNewsletterSubscriber :one
INSERT INTO newsletter_subscribers (email, unsubscribe_token, locale)
VALUES ($1, $2, $3)
ON CONFLICT (lower(email)) DO UPDATE
    SET confirmed_at    = now(),
        unsubscribed_at = NULL,
        locale          = EXCLUDED.locale
RETURNING unsubscribe_token;

-- Idempotent in one statement. A mail client that prefetches, a person who
-- clicks twice, a link followed a year later: all of them answer "you are off
-- the list", and coalesce keeps the FIRST time — the truthful one — rather than
-- moving it forward on every click.
--
-- Matching on the digest alone (not `AND unsubscribed_at IS NULL`) is what lets
-- a caller tell "already off the list" from "that link is not ours": the first
-- returns a row, the second returns none. Only the second is worth telling
-- somebody about, because only there is their address still on the list.
-- name: UnsubscribeNewsletter :one
UPDATE newsletter_subscribers
SET unsubscribed_at = coalesce(unsubscribed_at, now())
WHERE unsubscribe_token = $1
RETURNING email;

-- Record the language somebody was reading when they confirmed.
--
-- Set on the subscriber rather than carried in each message's payload, because an
-- ISSUE is enqueued by a back-office click where the subscriber is not present —
-- the same reason orders.locale and stock_notifications.locale exist.
-- Compose a draft. Sending is a separate statement, so a half-written issue is a
-- row nobody has received rather than a mail somebody has.
-- name: CreateNewsletterIssue :one
INSERT INTO newsletter_issues (subject, body)
VALUES (@subject, @body)
RETURNING id;

-- Everybody on the list right now, with the language to write to them in.
--
-- Read inside the send's own transaction. A count taken beforehand is a count
-- that can disagree with what was enqueued: somebody unsubscribing between the
-- two would be sent an issue the shop had already promised not to send them.
-- name: ActiveSubscribers :many
SELECT email, locale, unsubscribe_token
FROM newsletter_subscribers
WHERE unsubscribed_at IS NULL
ORDER BY confirmed_at;

-- Stamp an issue as sent, with what the send actually enqueued.
--
-- The WHERE clause is the guard: an issue that has already gone out matches
-- nothing, so a double-submitted form sends once. Checking it in Go first is a
-- check two concurrent requests both pass.
-- name: MarkNewsletterIssueSent :execrows
UPDATE newsletter_issues
SET sent_at = now(), recipients = @recipients, sent_by = @sent_by
WHERE id = @id AND sent_at IS NULL;

-- The issues, newest first, for the back office.
-- name: NewsletterIssues :many
SELECT i.id, i.subject, i.body, i.sent_at, i.recipients,
       coalesce(u.email, '') AS sent_by_email
FROM newsletter_issues i
LEFT JOIN users u ON u.id = i.sent_by
ORDER BY i.created_at DESC
LIMIT $1;

-- One issue, for the send.
-- name: NewsletterIssue :one
SELECT id, subject, body, sent_at FROM newsletter_issues WHERE id = $1;

-- What the back office needs to see about its own list.
--
-- Counted rather than listed by default: a mailing list is a column of addresses
-- and a page of them is a page nobody reads. The three states are what a person
-- running a newsletter actually asks.
-- name: NewsletterCounts :one
SELECT
    count(*) FILTER (WHERE unsubscribed_at IS NULL)     AS active,
    count(*) FILTER (WHERE unsubscribed_at IS NOT NULL) AS unsubscribed,
    (SELECT count(*) FROM newsletter_confirmations WHERE expires_at > now()) AS awaiting
FROM newsletter_subscribers;

-- The audit row for a send, written in the SEND's own transaction.
--
-- record_audit_event is granted to `admin` and to nobody else, which is why the
-- back office's newsletter store runs on the admin pool. An audit row for work
-- that rolled back is a lie and work that commits without one is a gap; sharing
-- the commit is what makes neither possible.
--
-- The subject travels, the BODY does not. audit_events is append-only and
-- erase_user does not reach it, so a letter copied in there would outlive every
-- other record of it — and newsletter_issues already holds the text.
-- name: RecordNewsletterSend :exec
SELECT record_audit_event(
    sqlc.narg(actor)::uuid, @action::text, 'newsletter_issues', @issue_id::uuid,
    NULL, @after, NULL
);