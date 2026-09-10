-- The FAQ, grouped by category in the order the back office set.
-- name: FAQEntries :many
SELECT localized_name(category, category_en, @locale::text) AS category,
       localized_name(question, question_en, @locale::text) AS question,
       localized_name(answer, answer_en, @locale::text) AS answer
FROM faq_entries
-- Category first, because position is unique only within a category and the
-- handler groups by adjacency. On the canonical category, never the localized
-- one, or a half-translated category splits into two headings.
ORDER BY category, position, id;

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
