-- What an order's customer may ask to return: what SHIPPED, minus what an
-- unrejected request already claims. return_within_shipment enforces the same.
-- name: ReturnableLines :many
SELECT ol.id,
       ol.sku,
       ol.product_name,
       ol.variant_label,
       ol.unit_price_cents,
       (coalesce((SELECT sum(sl.quantity) FROM order_shipment_lines sl
                  WHERE sl.order_line_id = ol.id), 0)
        - coalesce((SELECT sum(rl.quantity) FROM return_request_lines rl
                    JOIN return_requests r ON r.id = rl.return_request_id
                    WHERE rl.order_line_id = ol.id AND r.status <> 'rejected'), 0)
       )::integer AS returnable
FROM order_lines ol
WHERE ol.order_id = $1
ORDER BY ol.position, ol.id;

-- The order a return is being asked for.
-- name: OrderForReturn :one
SELECT o.id, o.order_number, o.fulfillment_status
FROM orders o WHERE o.order_number = $1;

-- name: CreateReturnRequest :one
INSERT INTO return_requests (order_id, requested_by_user_id, reason)
VALUES (@order_id, @requested_by_user_id, @reason::text)
RETURNING id;

-- name: CreateReturnRequestLine :exec
INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
VALUES (@order_id, @return_request_id, @order_line_id, @quantity::integer);

-- An order's return requests, for the customer's own page. No actor: the page is
-- reachable by anyone holding the number, so it must not name staff.
-- name: ReturnsForOrder :many
SELECT r.id, r.status, r.reason, r.resolution, r.created_at, r.decided_at
FROM return_requests r WHERE r.order_id = $1
ORDER BY r.created_at DESC, r.id;

-- Whether this order already has a request nobody has decided yet.
-- name: HasOpenReturn :one
SELECT EXISTS (
    SELECT 1 FROM return_requests WHERE order_id = $1 AND status = 'requested'
);
