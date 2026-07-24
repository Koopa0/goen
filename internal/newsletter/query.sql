-- Re-subscribing an address that previously opted out clears the opt-out
-- rather than failing, so the visitor sees the same confirmation either way.
-- name: SubscribeNewsletter :exec
INSERT INTO newsletter_subscribers (email)
VALUES ($1)
ON CONFLICT (lower(email)) DO UPDATE SET unsubscribed_at = NULL;
