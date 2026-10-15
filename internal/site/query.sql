-- The FAQ, grouped by category in the order the back office set. The handler
-- groups by category_id, never by the name: a name is a label, and two headings
-- may read the same in one language.
-- name: FAQEntries :many
SELECT c.id AS category_id,
       localized_name(c.name, c.name_en, @locale::text) AS category,
       localized_name(e.question, e.question_en, @locale::text) AS question,
       localized_name(e.answer, e.answer_en, @locale::text) AS answer
FROM faq_entries e
JOIN faq_categories c ON c.id = e.category_id
-- Category first, because position is unique only within a category and the
-- handler groups by adjacency.
ORDER BY c.position, e.position, e.id;

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
