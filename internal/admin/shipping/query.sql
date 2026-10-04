-- DISTINCT ON the method, ordered by effective_at DESC: the versions table is
-- append-only, so the current fee is the newest row that has taken effect.
-- name: AdminShippingMethods :many
SELECT DISTINCT ON (sm.id)
    sm.id AS method_id, sm.code, sm.destination_kind, sm.is_active,
    v.id AS version_id, v.name, v.carrier,
    coalesce(v.name_en, '') AS name_en, coalesce(v.carrier_en, '') AS carrier_en,
    v.fee_cents, v.free_over_cents, v.effective_at,
    (SELECT count(*) FROM shipping_method_versions mv WHERE mv.method_id = sm.id)::bigint
        AS version_count
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE v.effective_at <= now()
ORDER BY sm.id, v.effective_at DESC;

-- name: AdminVersionZones :many
SELECT z.id AS zone_id, z.code, z.name, vz.surcharge_cents
FROM shipping_version_zones vz
JOIN shipping_zones z ON z.id = vz.zone_id
WHERE vz.version_id = ANY(@version_ids::uuid[])
ORDER BY z.position, z.name;

-- An INSERT and never an UPDATE: shipping_method_versions_append_only refuses
-- one, because every past order names the version it was priced from.
-- name: PublishShippingVersion :one
INSERT INTO shipping_method_versions (method_id, name, carrier, name_en, carrier_en,
                                      fee_cents, free_over_cents, effective_at)
VALUES (@method_id, @name, nullif(@carrier::text, ''),
        nullif(@name_en::text, ''), nullif(@carrier_en::text, ''),
        @fee_cents, nullif(@free_over_cents::bigint, 0), statement_timestamp())
RETURNING id;

-- Fee publication and surcharge edits share this root; version rows are append-only.
-- name: LockShippingMethod :one
SELECT id FROM shipping_methods WHERE id = $1;

-- name: LockShippingMethodForVersion :one
SELECT sm.id
FROM shipping_methods sm
JOIN shipping_method_versions v ON v.method_id = sm.id
WHERE v.id = $1;

-- A waiting transaction's now() predates the publication that released its lock.
-- name: CurrentShippingVersion :one
SELECT id FROM shipping_method_versions
WHERE method_id = $1 AND effective_at <= statement_timestamp()
ORDER BY effective_at DESC, id DESC
LIMIT 1;

-- Without this, publishing a new base fee silently drops every surcharge: the
-- rows key on the VERSION, and the new version has none — so a shop raising its
-- home-delivery fee would start shipping to the outlying islands at that fee.
-- name: CarryZoneSurcharges :exec
INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
SELECT @new_version_id, vz.zone_id, vz.surcharge_cents
FROM shipping_version_zones vz
WHERE vz.version_id = (
    SELECT v.id FROM shipping_method_versions v
    WHERE v.method_id = @method_id AND v.id <> @new_version_id
      AND v.effective_at <= statement_timestamp()
    ORDER BY v.effective_at DESC, v.id DESC
    LIMIT 1
)
ON CONFLICT (version_id, zone_id) DO NOTHING;

-- name: AdminShippingZones :many
SELECT z.id, z.code, z.name, coalesce(z.name_en, '') AS name_en, z.position,
       (SELECT count(*) FROM shipping_zone_prefixes zp WHERE zp.zone_id = z.id)::bigint
           AS prefix_count,
       coalesce((SELECT string_agg(zp.prefix, ' ' ORDER BY zp.prefix)
                 FROM shipping_zone_prefixes zp WHERE zp.zone_id = z.id), '')::text
           AS prefixes
FROM shipping_zones z
ORDER BY z.position, z.name;

-- ON CONFLICT so the form is idempotent: a staff member who submits twice has
-- set one surcharge, not failed the second time.
-- name: SetZoneSurcharge :exec
INSERT INTO shipping_version_zones (version_id, zone_id, surcharge_cents)
VALUES (@version_id, @zone_id, @surcharge_cents)
ON CONFLICT (version_id, zone_id) DO UPDATE
SET surcharge_cents = EXCLUDED.surcharge_cents;

-- Absence is what "no surcharge" means to the lookup, which coalesces a missing
-- row to zero, so clearing is a DELETE and never a stored zero.
-- name: ClearZoneSurcharge :execrows
DELETE FROM shipping_version_zones WHERE version_id = $1 AND zone_id = $2;

-- The caller writes the first VERSION in the same transaction: a method with no
-- version is one the checkout finds and cannot price. Zero on a parcel ceiling
-- means "no stated limit" and stores NULL, the honest default for home delivery.
-- name: CreateShippingMethod :one
INSERT INTO shipping_methods (code, destination_kind, position,
                              max_parcel_longest_mm, max_parcel_sum_mm, max_parcel_weight_g)
VALUES (@code::text, @destination_kind::text,
        coalesce((SELECT max(position) FROM shipping_methods), 0) + 1,
        nullif(@max_parcel_longest_mm::integer, 0),
        nullif(@max_parcel_sum_mm::integer, 0),
        nullif(@max_parcel_weight_g::integer, 0))
RETURNING id;

-- name: SetShippingMethodActive :execrows
UPDATE shipping_methods SET is_active = @is_active::boolean WHERE id = @method_id;

-- name: CreateShippingZone :one
INSERT INTO shipping_zones (code, name, name_en, position)
VALUES (@code::text, @name::text, nullif(@name_en::text, ''),
        coalesce((SELECT max(position) FROM shipping_zones), 0) + 1)
RETURNING id;

-- Serializes whole-set edits for one zone. Without this, two forms can each
-- sweep against the other's partial work and commit a union neither submitted.
-- name: LockShippingZone :one
SELECT id FROM shipping_zones WHERE id = @zone_id FOR UPDATE;

-- prefix is the PRIMARY KEY, so a postal code belongs to exactly one zone by
-- construction and moving one is an upsert rather than an insert.
-- name: AssignZonePrefix :exec
INSERT INTO shipping_zone_prefixes (prefix, zone_id)
VALUES (@prefix::text, @zone_id)
ON CONFLICT (prefix) DO UPDATE SET zone_id = @zone_id;

-- The field carries this zone's WHOLE set. Scoped by zone_id so one zone's
-- stale form cannot sweep a prefix that has since moved to another zone.
-- name: RemoveZonePrefixesExcept :execrows
DELETE FROM shipping_zone_prefixes
WHERE zone_id = @zone_id
  AND NOT (prefix = ANY(coalesce(@keep::text[], '{}'::text[])));

-- Decided by the DELETE's own WHERE clause, like DeleteBrand.
-- name: DeleteShippingZone :execrows
DELETE FROM shipping_zones z
WHERE z.id = @zone_id
  AND NOT EXISTS (SELECT 1 FROM shipping_zone_prefixes p WHERE p.zone_id = z.id)
  AND NOT EXISTS (SELECT 1 FROM shipping_version_zones v WHERE v.zone_id = z.id);
