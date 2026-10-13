-- name: OrderBelongsTo :one
SELECT EXISTS (
    SELECT 1 FROM orders WHERE order_number = $1 AND user_id = $2
);

-- Give a browser access to an order it just placed, or just proved the email for.
-- :execrows, because the INSERT ... SELECT writes NO ROWS when the order number
-- matches nothing and a cookie no grant backs is a silent lockout.
-- name: GrantOrderAccess :execrows
INSERT INTO order_access_grants (digest, order_id)
SELECT @digest, id FROM orders WHERE order_number = @order_number::text
ON CONFLICT (digest) DO NOTHING;

-- The cookie is RE-ISSUED with a fresh MaxAge on every order, carrying older
-- tokens forward, so their grants' retention clock restarts on the same event or
-- one dies under a live cookie. Scoped to the digests actually presented and
-- still inside the retention window: a copied stale token must not be revived here.
-- name: TouchOrderAccessGrants :exec
UPDATE order_access_grants SET created_at = now()
WHERE digest = ANY(@digests::bytea[])
AND created_at > now() - @retain::interval;

-- Compared IN the database and answered as a boolean: which token matched is not
-- something any page needs to disclose.
-- name: OrderAccessibleWith :one
SELECT EXISTS (
    SELECT 1 FROM order_access_grants g
    JOIN orders o ON o.id = g.order_id
    WHERE o.order_number = @order_number::text AND g.digest = ANY(@digests::bytea[])
    AND g.created_at > now() - @retain::interval
);

-- The grants a browser presents, gone when it signs out. Expiring the cookie is
-- not enough: a client can ignore an expiry and present the tokens again.
-- name: RevokeOrderAccess :exec
DELETE FROM order_access_grants WHERE digest = ANY(@digests::bytea[]);

-- Drop access grants nobody can present any more: older than the cookie's
-- MaxAge, they are live bearer credentials kept forever for nobody.
-- name: DeleteOldOrderAccessGrants :exec
DELETE FROM order_access_grants WHERE created_at < now() - @retain::interval;
