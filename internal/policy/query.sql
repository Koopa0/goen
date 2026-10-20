-- The shipping methods a policy page describes.
-- name: ShippingPolicy :many
SELECT DISTINCT ON (sm.id)
    sm.code,
    localized_name(v.name, v.name_en, @locale::text) AS name,
    coalesce(localized_name(v.carrier, v.carrier_en, @locale::text), '')::text AS carrier,
    v.fee_cents, v.free_over_cents,
    v.id AS version_id
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active AND v.effective_at <= now()
  -- Pickup is listed only where checkout offers it.
  AND (@with_pickup::boolean OR sm.destination_kind <> 'pickup_point')
ORDER BY sm.id, v.effective_at DESC;

-- The zone surcharges those methods carry, one row per surcharge. A string_agg
-- would assemble the sentence here, where no locale exists to write it in.
-- name: ShippingPolicyZones :many
SELECT vz.version_id, localized_name(z.name, z.name_en, @locale::text) AS name,
       vz.surcharge_cents
FROM shipping_version_zones vz
JOIN shipping_zones z ON z.id = vz.zone_id
WHERE vz.version_id = ANY(@version_ids::uuid[])
ORDER BY z.position, z.name;

-- How long placing an order holds the stock and how long the customer has to
-- start paying, as the schema names them; the policy pages state both.
-- name: CheckoutWindows :one
SELECT checkout_hold_minutes()::bigint AS hold_minutes,
       checkout_pay_start_minutes()::bigint AS pay_start_minutes;

-- with_pickup is false where the store map is not configured: checkout offers no
-- pickup there, so a floor or threshold that counted it would promise a price
-- nobody can choose.
-- The strip makes one claim, so it is the cart's: the amount at which EVERY
-- method is free, the highest threshold. A method that costs nothing is free at
-- any amount and takes no part; one that charges and never turns free leaves no
-- claim. coalesce AND cast, because the aggregates over an empty set are NULL
-- and sqlc types the result as non-null.
-- name: FreeDeliveryThreshold :one
SELECT coalesce(
    CASE WHEN bool_or(coalesce(v.free_over_cents, 0) = 0) THEN 0 ELSE max(v.free_over_cents) END,
    0)::bigint AS free_over_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active
  AND (@with_pickup::boolean OR sm.destination_kind <> 'pickup_point')
  AND v.effective_at <= now()
  AND v.fee_cents > 0
  AND v.id = (SELECT id FROM shipping_method_versions
              WHERE method_id = sm.id AND effective_at <= now()
              ORDER BY effective_at DESC LIMIT 1);

-- with_pickup is false where the store map is not configured: checkout offers no
-- pickup there, so a floor or threshold that counted it would promise a price
-- nobody can choose.
-- MIN across methods: the strip states one floor, and the honest one is the
-- lowest fee any active method charges; a method that is always free charges
-- none, so it is not the fee below the threshold. coalesce AND cast, because
-- min() over an empty set is NULL and sqlc types the result as non-null.
-- name: LowestDeliveryFee :one
SELECT coalesce(min(v.fee_cents), 0)::bigint AS fee_cents
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE sm.is_active
  AND (@with_pickup::boolean OR sm.destination_kind <> 'pickup_point')
  AND v.effective_at <= now()
  AND v.fee_cents > 0
  AND v.id = (SELECT id FROM shipping_method_versions
              WHERE method_id = sm.id AND effective_at <= now()
              ORDER BY effective_at DESC LIMIT 1);

-- The windows the shop states about itself, each read from the function that
-- names it, so no page keeps a copy of the number.
-- name: StatedWindows :one
SELECT rescission_days()::bigint AS rescission_days,
       return_goodwill_days()::bigint AS return_days,
       checkout_hold_minutes()::bigint AS hold_minutes,
       checkout_pay_start_minutes()::bigint AS pay_start_minutes;
