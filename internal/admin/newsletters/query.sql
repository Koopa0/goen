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
    count(*) FILTER (WHERE unsubscribed_at IS NULL AND locale = 'en') AS english_active,
    count(*) FILTER (WHERE unsubscribed_at IS NOT NULL) AS unsubscribed,
    (SELECT count(*) FROM newsletter_confirmations WHERE expires_at > now()) AS awaiting
FROM newsletter_subscribers;

-- record_audit_event is granted to `admin` and to nobody else, which is why
-- issues are composed and sent on the admin pool. The subject travels, the
-- BODY does not: audit_events is append-only and erase_user does not reach it.
-- name: RecordNewsletterSend :exec
SELECT record_audit_event(
    sqlc.narg(actor)::uuid, @action::text, 'newsletter_issues', @issue_id::uuid,
    NULL, @after, NULL
);
