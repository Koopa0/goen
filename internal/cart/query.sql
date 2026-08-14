-- The cart a token names. Carts are found by the HASH of the cookie's token,
-- never by the token itself: the column holds a digest so a database leak does
-- not hand over live cart cookies.
-- name: CartByToken :one
SELECT id, user_id FROM carts WHERE token_hash = $1;

-- name: CreateCart :one
INSERT INTO carts (token_hash, user_id) VALUES ($1, $2) RETURNING id;

-- Add to a cart, or raise the quantity of what is already there. Adding the
-- same variant twice is one line with more of it, not two lines — the primary
-- key says so and this makes the write agree.
--
-- The cap is the CHECK's own ceiling, applied with least() so a repeat add
-- stops at the limit rather than raising a constraint violation the visitor did
-- nothing to deserve.
-- name: AddCartItem :exec
INSERT INTO cart_items (cart_id, variant_id, quantity)
VALUES ($1, $2, $3)
ON CONFLICT (cart_id, variant_id) DO UPDATE
SET quantity = least(cart_items.quantity + EXCLUDED.quantity, 999);

-- name: SetCartItemQuantity :exec
UPDATE cart_items SET quantity = $3
WHERE cart_id = $1 AND variant_id = $2;

-- name: RemoveCartItem :exec
DELETE FROM cart_items WHERE cart_id = $1 AND variant_id = $2;

-- name: ClearCart :exec
DELETE FROM cart_items WHERE cart_id = $1;

-- Everything in a cart, with what it costs and whether it can still be bought.
--
-- A cart line is not a promise: a variant can sell out or change price while it
-- sits there. The page shows the CURRENT price and the CURRENT availability, so
-- a visitor is never quoted a total the checkout will refuse. sellable_quantity
-- is what may actually be taken — stock above safety_stock, the floor
-- record_inventory_movement enforces.
-- name: CartLines :many
SELECT
    pv.id AS variant_id,
    pv.sku,
    pv.price_cents,
    pv.compare_at_price_cents,
    ci.quantity,
    (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
    pv.is_active,
    p.slug,
    localized_name(p.name, p.name_en, @locale::text) AS name,
    b.name AS brand,
    -- The cart line SHOWS the selection (「星霧藍 · 512GB」) rather than matching on
    -- it — the line names its variant by id — so these are localized. The PDP's own
    -- variant query is the opposite case and is exempt for exactly that reason.
    coalesce(
        (SELECT array_agg(localized_name(o.name, o.name_en, @locale::text)
                          ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_names,
    coalesce(
        (SELECT array_agg(localized_name(v.value, v.value_en, @locale::text)
                          ORDER BY o.position, o.id)
         FROM variant_option_values vov
         JOIN product_options o ON o.id = vov.option_id
         JOIN product_option_values v ON v.id = vov.option_value_id
         WHERE vov.variant_id = pv.id),
        ARRAY[]::text[]
    )::text[] AS option_values,
    coalesce(img.storage_key, '') AS image_key,
    coalesce(img.alt_text, '') AS image_alt
FROM cart_items ci
JOIN product_variants pv ON pv.id = ci.variant_id
JOIN products p ON p.id = pv.product_id
JOIN brands b ON b.id = p.brand_id
LEFT JOIN LATERAL (
    SELECT storage_key, alt_text FROM product_images
    WHERE product_id = p.id ORDER BY position LIMIT 1
) img ON true
WHERE ci.cart_id = $1
ORDER BY ci.added_at, pv.id;

-- How many items a cart holds, for the header badge. Counts UNITS, not lines:
-- a badge reading 1 over a cart holding three of something is wrong in the way
-- a visitor notices at checkout.
-- name: CartItemCount :one
SELECT coalesce(sum(quantity), 0)::bigint FROM cart_items WHERE cart_id = $1;

-- A variant a visitor is trying to add. Checked before the write so an inactive
-- or missing variant is a message rather than a foreign-key error.
-- name: VariantForCart :one
SELECT pv.id, pv.is_active,
       (pv.stock_quantity - pv.safety_stock)::integer AS sellable_quantity,
       p.slug, p.status
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE pv.id = $1;

-- The shipping choices at checkout: each method's CURRENT version.
--
-- DISTINCT ON takes the newest version per method. An order stores the version
-- id it was placed under, so a later price change cannot rewrite what an old
-- order was charged.
-- A method is offered only if this cart's contents can physically go by it.
--
-- The test is PER ITEM, not over the cart total, and that is the whole rule:
-- more parcels are always possible, so two things that each fit are two
-- parcels — but a single item that does not fit cannot be split, whatever else
-- is in the basket. Weight is per item for the same reason.
--
-- A method with NULL limits accepts everything, and a variant with NULL
-- measurements is refused by nothing. Unknown is not "too big": a shop that has
-- not measured its catalogue would otherwise lose 超商取貨 — the channel 75.2%
-- of Taiwanese online shoppers prefer — on every product at once, silently, and
-- that costs more than the counter refusal it would prevent. /admin/products
-- shows what is unmeasured.
-- name: ShippingChoices :many
SELECT DISTINCT ON (sm.id)
    v.id AS version_id,
    sm.code,
    sm.destination_kind,
    localized_name(v.name, v.name_en, @locale::text) AS name,
    v.carrier,
    v.fee_cents,
    v.free_over_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active AND v.effective_at <= now()
  AND NOT EXISTS (
      SELECT 1
      FROM cart_items ci
      JOIN product_variants pv ON pv.id = ci.variant_id
      WHERE ci.cart_id = @cart_id
        AND ((sm.max_parcel_longest_mm IS NOT NULL AND pv.parcel_longest_mm IS NOT NULL
              AND pv.parcel_longest_mm > sm.max_parcel_longest_mm)
          OR (sm.max_parcel_sum_mm IS NOT NULL AND pv.parcel_sum_mm IS NOT NULL
              AND pv.parcel_sum_mm > sm.max_parcel_sum_mm)
          OR (sm.max_parcel_weight_g IS NOT NULL AND pv.parcel_weight_g IS NOT NULL
              AND pv.parcel_weight_g > sm.max_parcel_weight_g)))
ORDER BY sm.id, v.effective_at DESC;

-- One shipping version, re-read at order time. The form's value is not trusted:
-- a hand-edited version id must not let an order be placed at a fee that was
-- never offered.
-- The delivery addresses an account has saved, for the checkout to offer.
--
-- Scoped to the owner IN the query rather than checked afterwards: the id comes
-- off a URL, and "is this mine?" asked after the read is a question somebody
-- eventually forgets to ask.
--
-- internal/account owns the address book and its CRUD. This is a read of the
-- same rows from the one page that has to fill a form with them; duplicating
-- the columns here rather than importing account's store is what keeps the two
-- features from depending on each other's internals.
-- name: SavedAddresses :many
SELECT id, label, recipient_name, phone, postal_code, city, district, street, is_default
FROM addresses WHERE user_id = $1
ORDER BY is_default DESC, created_at;

-- What one version charges to send an order to one postal code.
--
-- The zone lookup is the QUERY; the arithmetic is ShippingFee in Go, beside the
-- coupon capping and the totals it has to agree with. It was a SQL function
-- first — one rule, three callers — and sqlc cannot resolve the columns of a
-- set-returning function, so the choice was a composite blob in Go or the rule
-- split across two languages. One definition in Go, read by every caller, is
-- the same guarantee without either.
--
-- postal_code may be empty: a 超商取貨 order has no postal code, because its
-- destination is a store. Such an order matches no prefix and gets the mainland
-- answer, which is right rather than a special case.
-- name: ShippingZoneFor :one
SELECT coalesce(vz.surcharge_cents, 0)::bigint AS surcharge_cents,
       coalesce(localized_name(z.name, z.name_en, @locale::text), '')::text AS zone_name
FROM shipping_method_versions v
LEFT JOIN shipping_zone_prefixes zp
       ON nullif(@postal_code::text, '') IS NOT NULL
      AND zp.prefix = left(@postal_code::text, 3)
LEFT JOIN shipping_zones z ON z.id = zp.zone_id
LEFT JOIN shipping_version_zones vz
       ON vz.version_id = v.id AND vz.zone_id = zp.zone_id
WHERE v.id = @version_id;

-- name: ShippingVersion :one
SELECT v.id, sm.code, sm.destination_kind,
       localized_name(v.name, v.name_en, @locale::text) AS name,
       v.fee_cents, v.free_over_cents
FROM shipping_method_versions v
JOIN shipping_methods sm ON sm.id = v.method_id
WHERE v.id = $1 AND sm.is_active AND v.effective_at <= now();

-- Place the order header. The order_number comes from next_order_number(),
-- which is SECURITY DEFINER because store cannot write the counter directly.
-- name: CreateOrder :one
INSERT INTO orders (
    order_number, user_id, shipping_version_id, shipping_method_code,
    shipping_method_name, shipping_cents, discount_cents, tax_cents, customer_note,
    locale
) VALUES (
    next_order_number(), @user_id, @shipping_version_id, @shipping_method_code,
    @shipping_method_name, @shipping_cents, @discount_cents, 0, @customer_note,
    @locale
)
RETURNING id, order_number;

-- One order line, priced from the variant as it stands at this moment. The
-- price is COPIED rather than referenced: an order is a record of what was
-- agreed, and a later price change must not rewrite it.
-- name: CreateOrderLine :exec
INSERT INTO order_lines (
    order_id, variant_id, sku, product_name, variant_label,
    unit_price_cents, quantity, position
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- The delivery details, which live in their own table so erase_user can blank
-- them without touching the financial record.
-- The address columns and the pickup columns are BOTH written, and exactly one
-- group is non-NULL — order_private_data_one_destination refuses anything else.
-- Passing all of them and letting the caller null the group that does not apply
-- keeps the two destinations one statement rather than two INSERTs and a branch
-- in Go deciding which order gets which.
-- name: CreateOrderPrivateData :exec
INSERT INTO order_private_data (
    order_id, email, recipient_name, phone, postal_code, city, district, street,
    pickup_brand, pickup_store_code, pickup_store_name
) VALUES (
    @order_id, @email, @recipient_name, @phone,
    nullif(@postal_code::text, ''), nullif(@city::text, ''),
    nullif(@district::text, ''), nullif(@street::text, ''),
    nullif(@pickup_brand::text, ''), nullif(@pickup_store_code::text, ''),
    nullif(@pickup_store_name::text, '')
);

-- Whether an account owns an order. This is what lets a signed-in customer reach
-- the confirmation and payment pages without the placed-order cookie — after
-- signing in on another device, for instance.
--
-- internal/payment reads this too. sqlc generates one db package for the whole
-- module, so the query is defined once here rather than duplicated there.
-- name: OrderBelongsTo :one
SELECT EXISTS (
    SELECT 1 FROM orders WHERE order_number = $1 AND user_id = $2
);

-- name: OrderSummaryByNumber :one
SELECT o.id, o.order_number, o.fulfillment_status,
       o.shipping_cents, o.discount_cents, o.tax_cents,
       -- WHICH discount, joined rather than snapshotted: coupons.code is never
       -- updated and the FK is ON DELETE RESTRICT, so one join always reaches it.
       -- Without it an order shows "折扣 −NT$200" with nothing saying why, to
       -- the customer or to the shop.
       coalesce((SELECT c.code || ' · ' || c.description
                 FROM coupon_redemptions cr JOIN coupons c ON c.id = cr.coupon_id
                 WHERE cr.order_id = o.id), '')::text AS discount_reason,
       o.shipping_method_name, o.placed_at,
       coalesce((SELECT sum(ol.unit_price_cents * ol.quantity) FROM order_lines ol
                 WHERE ol.order_id = o.id), 0)::bigint AS subtotal_cents,
       coalesce(pd.email, '') AS email,
       coalesce(pd.postal_code, '') AS postal_code,
       coalesce(pd.city, '') AS city,
       coalesce(pd.district, '') AS district,
       coalesce(pd.street, '') AS street,
       coalesce(pd.pickup_brand, '') AS pickup_brand,
       coalesce(pd.pickup_store_code, '') AS pickup_store_code,
       coalesce(pd.pickup_store_name, '') AS pickup_store_name,
       -- Whether the order is funded. 'pending' does NOT mean unpaid: a webhook
       -- can have captured the money minutes before the shop moves the order to
       -- picking, and offering a cancel button on that order is a control that
       -- can only say no. Read through committed_orders, the one definition.
       (o.id IN (SELECT id FROM committed_orders))::boolean AS committed,
       -- What is left to pay, and NOT derivable from `committed` above. A fully
       -- store-credited order has no payment row and stays 'pending' until a
       -- human picks it, so committed_orders reports it false while the customer
       -- owes nothing. Both columns, because neither answers the other's case —
       -- this is the same pair internal/payment already reads as Paid and
       -- FullyFunded before it will open a Stripe session.
       order_amount_owed(o.id)::bigint AS owed_cents
FROM orders o
LEFT JOIN order_private_data pd ON pd.order_id = o.id
WHERE o.order_number = $1;

-- An order's lines as a REORDER sees them: what was bought, and what of it can
-- still be bought.
--
-- LEFT JOIN and not JOIN: order_lines keeps the product name and price it was
-- sold at, and variant_id is nullable precisely so a line survives the variant
-- being deleted. An order from last year will have some, and dropping those
-- rows silently would make the page say it added everything.
-- name: ReorderLines :many
SELECT ol.variant_id, ol.product_name, ol.variant_label, ol.quantity,
       coalesce(pv.is_active AND p.status = 'active', false)::boolean AS sellable,
       coalesce(pv.stock_quantity - pv.safety_stock, 0)::integer AS available
FROM order_lines ol
JOIN orders o ON o.id = ol.order_id
LEFT JOIN product_variants pv ON pv.id = ol.variant_id
LEFT JOIN products p ON p.id = pv.product_id
WHERE o.order_number = $1
ORDER BY ol.position, ol.id;

-- name: OrderLinesByOrder :many
SELECT sku, product_name, variant_label, unit_price_cents, quantity
FROM order_lines WHERE order_id = $1 ORDER BY position, id;

-- Record that a checkout attempt produced an order, keyed by the idempotency
-- key the form carried. A resubmitted checkout finds its own order here instead
-- of placing a second one.
-- name: RecordCheckoutAttempt :exec
INSERT INTO checkout_attempts (idempotency_key, cart_id, order_id)
VALUES ($1, $2, $3)
ON CONFLICT (idempotency_key) DO NOTHING;

-- name: CheckoutAttempt :one
SELECT order_id FROM checkout_attempts WHERE idempotency_key = $1;

-- Hold the stock an order needs. The reservation expires, so an abandoned
-- checkout returns its stock to the shelf rather than holding it forever.
-- name: HoldForOrder :one
SELECT hold_inventory(
    @order_id, @variant_id, @quantity::integer, @expires_at, @idempotency_key::text
);

-- The order's first history entry. Written in the same transaction as the order
-- itself, so an order without a 'placed' event cannot exist.
--
-- internal/admin and internal/payment append to this table too; sqlc generates
-- one db package for the whole module, so the query lives wherever it was
-- first needed rather than being duplicated per feature.
-- name: RecordPlacedEvent :exec
INSERT INTO order_events (order_id, kind) VALUES ($1, 'placed');

-- The order's history, for the customer's own confirmation page.
--
-- The customer sees WHAT happened, never WHO did it: an order timeline that
-- names the shop assistant who picked it leaks staff identity to anyone with
-- the order number. The back office reads the same table with the actor joined.
-- name: OrderTimeline :many
SELECT kind, note, occurred_at
FROM order_events WHERE order_id = $1 ORDER BY occurred_at, id;

-- name: OrderTracking :many
SELECT carrier, tracking_number, shipped_at, delivered_at
FROM order_shipments WHERE order_id = $1 ORDER BY shipped_at, id;

-- Reservations whose hold has run out and whose order never got funded.
--
-- order_is_committed, not "exists a succeeded payment": a zero-owed order — a
-- 100% discount, or one fully covered by store credit — is committed with no
-- payment row at all, and releasing its stock would take the shelf away from
-- goods that are going to be shipped.
--
-- Bounded, because a sweeper that tries to clear a year of abandoned checkouts
-- in one pass holds locks for as long as that takes. It runs again in a minute.
-- name: ExpiredReservations :many
SELECT ir.id
FROM inventory_reservations ir
JOIN orders o ON o.id = ir.order_id
WHERE ir.state = 'held'
  AND ir.expires_at < now()
  AND NOT order_is_committed(ir.order_id)
  -- Committed is not the whole question. A zero-owed order — fully store-credited,
  -- or zeroed by a 100% coupon — has no payment row and sits at 'pending' until a
  -- human picks it, so committed_orders reports it false while the customer has
  -- already paid in full. release_reservation refuses it by name; this keeps the
  -- sweeper from asking every minute and counting the refusal as a skip.
  AND (o.fulfillment_status = 'cancelled' OR order_amount_owed(ir.order_id) <> 0)
ORDER BY ir.expires_at
LIMIT $1;

-- name: ReleaseReservation :exec
SELECT release_reservation($1);

-- The held reservations on one order, for cancelling it.
--
-- Ordered by id so two cancellations of the same order take the variant locks
-- in the same sequence — release_reservation locks the variant then the order,
-- and two callers walking the same set in different orders is a deadlock.
-- The customer's own cancellation, recorded in the order's history.
--
-- actor_user_id stays NULL: a guest has no account, and putting the shop's own
-- id there would attribute the customer's decision to a staff member who never
-- touched it. The ABSENCE of an actor is what distinguishes this from a
-- back-office cancel — the back office always writes one.
--
-- Structurally, and never as a note saying the same thing. The customer's own
-- order page RENDERS notes, so '顧客自行取消' written here reaches an English
-- customer as a Chinese sentence in their timeline, forever. A fact carried
-- structurally is a fact each audience can be told in its own language — and a
-- query that assembles chrome is chrome written where nobody can ask who is
-- reading, which is why the Han sweep covers .sql literals too.
-- name: RecordCancellation :exec
INSERT INTO order_events (order_id, kind)
SELECT id, 'cancelled' FROM orders WHERE order_number = $1;

-- name: HeldReservationsForOrder :many
SELECT r.id FROM inventory_reservations r
JOIN orders o ON o.id = r.order_id
WHERE o.order_number = $1 AND r.state = 'held'
ORDER BY r.id;

-- Cancel an order, but only from the one state a customer may cancel from.
--
-- Two predicates, and each is load-bearing because committed_orders does NOT
-- count a cancelled order: `pending` is what refuses a second cancellation and
-- an order the shop has started, and `not committed` is what refuses one
-- somebody has paid for. Both are proven by mutation.
--
-- In the WHERE clause rather than read first: two cancellations racing a
-- capture must not both decide the order was cancellable.
-- orders_check_transition would refuse an illegal move anyway; this makes the
-- refusal a row count the page can act on.
--
-- Not the same statement as the back office's AdvanceOrder: that one moves an
-- order to any legal status and is audited as staff work, and widening it to
-- carry a customer's own cancellation would put "who did this" back into doubt.
-- name: CancelOrderByCustomer :execrows
UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
WHERE order_number = $1
  AND fulfillment_status = 'pending'
  AND id NOT IN (SELECT id FROM committed_orders);

-- What a signed-in customer has to spend, from store_credit_balances — the one
-- definition of the figure. Summed from the ledger, never stored.
-- name: AvailableCredit :one
SELECT coalesce((SELECT b.balance_cents FROM store_credit_balances b
                 WHERE b.user_id = $1), 0)::bigint;

-- Spend credit against an order. A NEGATIVE amount, keyed on the order so a
-- retried checkout debits once — post_store_credit returns the existing entry
-- rather than writing a second, and store_credit_never_negative refuses a
-- balance the customer does not have.
-- name: SpendCredit :one
SELECT post_store_credit(@user_id, @amount_cents::bigint, @reason::text,
                         @order_id, @idempotency_key::text, NULL);

-- What the customer asked for on their invoice. Written with the order, because
-- the choice is part of what was agreed and erase_user clears it alongside the
-- delivery details.
-- name: CreateInvoicePreference :exec
INSERT INTO invoice_preferences (order_id, invoice_type, carrier_code, tax_id)
VALUES (@order_id, @invoice_type::text, nullif(@carrier_code::text, ''), nullif(@tax_id::text, ''));

-- A coupon by the code a customer typed.
--
-- Matched on upper(code) so it hits coupons_code_key, and so SUMMER20 and
-- summer20 are the same code — which is what a customer reading it off a card
-- expects.
--
-- The window is decided HERE, in SQL, against the DATABASE's clock.
--
-- starts_at defaults to the database's now(); comparing it to Go's time.Now()
-- is comparing two clocks, and a container a few milliseconds ahead of its host
-- makes a coupon that was just created read as "not started yet". They are one
-- clock now.
--
-- The LIMITS are still not decided here: redeem_coupon counts them under a lock
-- on the coupon row, and counting them in this query would be counting them
-- without one — two concurrent checkouts would each pass.
-- name: CouponByCode :one
SELECT id, code, description, kind, amount_cents, percent_bp,
       min_subtotal_cents, max_discount_cents,
       max_redemptions, per_customer_limit, is_active,
       (starts_at <= now() AND (ends_at IS NULL OR ends_at > now()))::boolean AS is_current
FROM coupons WHERE upper(code) = upper(@code::text);

-- sqlc.narg on the user: a guest checkout has no account, and redeem_coupon
-- treats NULL as "no per-customer limit to count against" rather than as a
-- customer whose id happens to be zero.
-- name: RedeemCoupon :one
SELECT redeem_coupon(@coupon_id, @order_id, sqlc.narg(user_id)::uuid, @amount_cents::bigint);

-- Enqueue a message in the SAME transaction as the fact it is about.
--
-- That is the whole point of an outbox: the order and the intent to email about
-- it commit together, so a crash cannot leave one without the other. Sending
-- after the commit loses messages; sending before it sends about orders that
-- never happened.
--
-- ON CONFLICT DO NOTHING against (topic, dedupe_key), so a retried checkout
-- enqueues once.
-- name: EnqueueMessage :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload)
VALUES (@topic::text, @dedupe_key::text, @payload)
ON CONFLICT (topic, dedupe_key) DO NOTHING;

-- The same enqueue, behind everything transactional.
--
-- A separate query rather than a priority parameter on the one above, because
-- every existing caller is transactional and a parameter would let one of them
-- pass the wrong number. Bulk is the exception and it says so at the call site.
-- name: EnqueueBulkMessage :exec
INSERT INTO outbox_messages (topic, dedupe_key, payload, priority)
VALUES (@topic::text, @dedupe_key::text, @payload, @priority)
ON CONFLICT (topic, dedupe_key) DO NOTHING;

-- Checkout idempotency keys past their replay window.
--
-- One row per checkout attempt, keyed on what the form carried, and nothing ever
-- deleted them: the table grows with every submission goen has ever seen,
-- successful or not.
--
-- Deleting a row frees its key to be replayed, which is why the window is weeks
-- rather than hours. A key belongs to one rendered form; a browser holding one
-- for a month has long since been closed, and a double-submit that far apart is
-- not the failure this table defends against.
-- name: DeleteOldCheckoutAttempts :exec
DELETE FROM checkout_attempts
WHERE created_at < now() - sqlc.arg(retain)::interval;

-- Give back store credit spent on an order being cancelled.
--
-- Called in the cancellation's OWN transaction, beside the stock release and for
-- the same reason: the status change is what makes both legal, and a cancellation
-- that committed without them would leave the units off the shelf and the money
-- gone.
-- name: ReverseOrderCredit :one
SELECT reverse_order_credit(@order_id)::bigint AS returned_cents;

-- OrderIDByNumber is defined in internal/admin/query.sql. sqlc builds ONE db
-- package for the module, so it is written once and called from here.

-- Does this order number belong to this email address?
--
-- Both in ONE statement, and the answer is a boolean rather than a row. A guest
-- who has lost the cookie that proves they placed an order has only these two
-- things, and the pair is the whole credential: order numbers come off a per-day
-- counter and are guessable, so the address is the only secret in it.
--
-- Returning a row would tempt a caller into comparing the address in Go, and a
-- caller that compares would be a caller that can report WHICH half was wrong.
-- One boolean cannot.
--
-- Matched case-insensitively on the address as stored.
--
-- `erased_at IS NULL` is belt to the email's braces, and saying so matters: what
-- actually excludes an erased order is that erase_user sets email to NULL, so the
-- comparison below is NULL rather than true. Deleting the erased_at predicate does
-- not open the door — proven by mutation, which is why this comment does not claim
-- it does. It stays because it says what the query means, and because a future
-- erasure that blanked less would then still be caught here.
-- name: OrderBelongsToEmail :one
SELECT EXISTS (
    SELECT 1 FROM orders o
    JOIN order_private_data pd ON pd.order_id = o.id
    WHERE o.order_number = @order_number::text
      AND pd.erased_at IS NULL
      AND lower(pd.email) = lower(@email::text)
) AS ok;

-- Give a browser access to an order it just placed, or just proved the email for.
--
-- :execrows rather than :exec, because the INSERT ... SELECT writes NO ROWS when
-- the order number matches nothing — and SQL does not call that an error. The
-- caller would return nil, set a cookie, and hand the browser a token no grant
-- backs: a customer locked out of their own order, with no failure anywhere to
-- explain it. Both call sites hold a number the database just gave them, so it
-- is unreachable today; it is the SHAPE that becomes a silent lockout, and it is
-- the same correction the three admin toggles got when SetProductStatus on a
-- missing slug answered 303 and wrote an audit row saying it had published.
-- name: GrantOrderAccess :execrows
INSERT INTO order_access_grants (digest, order_id)
SELECT @digest, id FROM orders WHERE order_number = @order_number::text
ON CONFLICT (digest) DO NOTHING;

-- Restart the retention clock on the grants a browser is still carrying.
--
-- The cookie holds up to ten tokens and is RE-ISSUED with a fresh MaxAge every
-- time an order is placed, carrying the older ones forward. The grants behind
-- them were swept on their own created_at, so the two clocks came apart the
-- moment somebody ordered twice: a customer who bought on day 0 and again on day
-- 25 held a cookie live until day 55 naming an order whose grant died on day 30.
--
-- GrantRetain's own comment names that state as the one that must never happen —
-- "a grant swept while its cookie is still live locks a customer out of their own
-- order" — and equality between the two constants only delivers it if the cookie
-- is never re-issued. It is. So the clock is restarted HERE, on the same event
-- that restarts the cookie's, which is what makes the two intervals comparable
-- at all.
--
-- Scoped to the digests presented: a token this browser is not carrying is not
-- evidence of anything, and touching every grant on the order would extend a
-- credential held by some other browser.
-- name: TouchOrderAccessGrants :exec
UPDATE order_access_grants SET created_at = now()
WHERE digest = ANY(@digests::bytea[]);

-- Hold the checkout's idempotency key for the length of this transaction.
--
-- Two requests from one double-click arrive milliseconds apart, and the read
-- above them ran on the pool before either had a transaction — so both missed,
-- and both went on to place an order. The attempt row that would have collided
-- was written second-to-last, with ON CONFLICT DO NOTHING as :exec, so its row
-- count was discarded and the loser committed anyway.
--
-- An advisory lock rather than an early INSERT, because of what happens to the
-- LOSER: an early row would hold the key while its order_id is still NULL, so
-- the second request would find a claim it cannot answer with a number. Waiting
-- means that by the time it looks, the winner has committed and there IS a
-- number to hand back. Same order, one placement, no error shown to anybody.
--
-- xact, so it releases on commit or rollback with nothing to remember. The key
-- is hashed to the bigint the lock space wants; a collision between two
-- different keys costs one request a short wait and nothing else.
-- name: LockCheckoutKey :one
SELECT pg_advisory_xact_lock(hashtextextended(@idempotency_key::text, 0));

-- The order a completed attempt produced.
-- name: OrderNumberByID :one
SELECT order_number FROM orders WHERE id = @id;

-- Drop access grants nobody can present any more.
--
-- The cookie carrying these tokens has a 30-day MaxAge, so a grant older than
-- that is unreachable by any browser: a live bearer credential — it opens the
-- order page, the cancel form and the return form — kept forever for nobody.
-- One recovered from a proxy log or an old backup worked indefinitely.
--
-- Retention rather than an expires_at column, because the question is "is this
-- older than the cookie that carries it" and there is exactly one answer for
-- every row. A column would be a second place to write the same interval.
-- name: DeleteOldOrderAccessGrants :exec
DELETE FROM order_access_grants WHERE created_at < now() - @retain::interval;

-- Which of the orders this browser holds tokens for is the one being asked about.
--
-- The digests are compared IN THE DATABASE and the answer is a boolean, for the
-- reason FindOrder returns one: a caller that got rows back could report WHICH token
-- matched, and the set of tokens a browser holds is not something any page needs to
-- disclose.
-- name: OrderAccessibleWith :one
SELECT EXISTS (
    SELECT 1 FROM order_access_grants g
    JOIN orders o ON o.id = g.order_id
    WHERE o.order_number = @order_number::text AND g.digest = ANY(@digests::bytea[])
);

-- Every Checkout Session this order still has open at Stripe.
--
-- Read INSIDE the cancelling transaction and acted on after it commits, the same
-- shape as HeldReservationsForOrder above: the set a cancellation acts on is the
-- one that transaction decided, not whatever a second read finds later.
--
-- 'requires_payment' is goen's record of an open session, and it is only ever a
-- HINT here — a customer who paid seconds ago still has this row, because only
-- the webhook moves it. Stripe decides whether each of these can be expired, by
-- refusing anything but an open session. Reading this as "no money has arrived"
-- would be the mistake the whole webhook path is built to avoid.
--
-- Called by internal/admin as well as internal/cart: sqlc builds one package, and
-- the two cancel doors ask one question. A second copy of it in the back office's
-- query.sql is a second place for the predicate to be wrong.
-- name: OpenSessionsForOrder :many
SELECT p.provider_ref
FROM payments p
JOIN orders o ON o.id = p.order_id
WHERE o.order_number = @order_number::text AND p.status = 'requires_payment'
ORDER BY p.created_at;
