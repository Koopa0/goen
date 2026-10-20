-- waiting_days is computed HERE because created_at is written by the database's
-- clock: taking the difference in Go subtracts two clocks, and a container
-- milliseconds ahead of its host reports a four-day-old message as three.
-- name: AdminMessages :many
SELECT json_build_object('Rank', (handled_at IS NOT NULL), 'At', created_at, 'ID', id)::text AS page_cursor, id, name, email, subject, coalesce(order_ref, '') AS order_ref,
       message, handled_at, created_at,
       floor(extract(epoch FROM now() - created_at) / 86400)::integer AS waiting_days
FROM contact_messages
WHERE (NOT @has_cursor::boolean OR ((handled_at IS NOT NULL) > @after_rank::boolean)
       OR ((handled_at IS NOT NULL) = @after_rank::boolean AND created_at > @after_at::timestamptz)
       OR ((handled_at IS NOT NULL) = @after_rank::boolean AND created_at = @after_at::timestamptz AND id > @after_id::uuid))
ORDER BY (handled_at IS NOT NULL) ASC, created_at ASC, id ASC
LIMIT @row_limit::integer;

-- name: HandleMessage :execrows
UPDATE contact_messages SET handled_at = now()
WHERE id = $1 AND handled_at IS NULL;

-- name: ReopenMessage :execrows
UPDATE contact_messages SET handled_at = NULL
WHERE id = $1 AND handled_at IS NOT NULL;
