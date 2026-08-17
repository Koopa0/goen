-- The FAQ, grouped by category in the order the back office set.
-- name: FAQEntries :many
SELECT localized_name(category, category_en, @locale::text) AS category,
       localized_name(question, question_en, @locale::text) AS question,
       localized_name(answer, answer_en, @locale::text) AS answer
FROM faq_entries
-- Category first: policy.go groups by ADJACENCY, and faq_entries_position_key is
-- unique on (category, position), so positions repeat across categories and
-- ordering by position interleaves them into one heading per question.
--
-- On the CANONICAL category, never the localized one, or a half-translated
-- category splits into two headings.
ORDER BY category, position, id;

-- The shipping methods a policy page describes.
--
-- Read from the database rather than written into prose: a page that states a
-- fee is a promise, and the one place that promise is already kept is the table
-- checkout charges from. A page that restates it can drift; this one cannot.
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

-- The zone surcharges those methods carry, as DATA.
--
-- One row per surcharge, and never one column. A string_agg here would build
-- '離島 另加 NT$200、澎湖 另加 NT$150' in SQL for the page to print: the figures
-- right and the WORDS Chinese for every reader, because a sentence assembled
-- where no locale exists cannot be anything else. A query that assembles chrome
-- is chrome written where nobody can ask who is reading, which is why the Han
-- sweep reads .sql literals and not only .go and .templ. The zone NAME stays as
-- the shop typed it; 另加 and the joiner are chrome and follow the visitor.
--
-- Read from the SAME rows checkout charges from, for the reason the fee is: a
-- page that restates a number is a page that eventually contradicts the till.
-- name: ShippingPolicyZones :many
SELECT vz.version_id, localized_name(z.name, z.name_en, @locale::text) AS name,
       vz.surcharge_cents
FROM shipping_version_zones vz
JOIN shipping_zones z ON z.id = vz.zone_id
WHERE vz.version_id = ANY(@version_ids::uuid[])
ORDER BY z.position, z.name;
