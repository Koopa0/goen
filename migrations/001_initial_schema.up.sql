-- goen — the whole schema, created from nothing.
--
-- Rules that would corrupt money, stock or history are enforced here rather than
-- in application code: CHECK within a row, UNIQUE/FK across rows, and a trigger
-- where a rule spans rows — each locks its aggregate root before it reads and
-- raises with an explicit CONSTRAINT name.
--
-- Money is bigint minor units: TWD has two decimals for charges, so NT$33,900 is
-- 3390000. Presence is `~ '[^[:space:]]'` and never length(btrim(x)) > 0,
-- because btrim strips spaces only and a lone tab would pass.

-- squawk-ignore-file prefer-bigint-over-int
-- squawk-ignore-file prefer-bigint-over-smallint
-- Every money column is bigint. What remains integer or smallint is quantities,
-- positions, dimensions and a 1-to-5 rating, each bounded by its own CHECK.
-- squawk-ignore-file adding-foreign-key-constraint
-- squawk-ignore-file constraint-missing-not-valid
-- Those two are for altering a table people are already writing to; this file
-- creates the schema from nothing, so there are no rows to scan.
-- squawk-ignore-file require-concurrent-index-creation
-- Every index is on a table created above it, and CONCURRENTLY cannot run inside
-- the transaction golang-migrate wraps this file in.

SET lock_timeout = '3s';
SET statement_timeout = '120s';

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ============================================================================
-- Roles
--
-- A trigger cannot stop `UPDATE product_variants SET stock_quantity = 999`, so
-- the one-writer claims below are enforced by the role the application connects
-- as. goen owns the schema, store is what a customer-facing request may do,
-- store_svc is the only LOGIN role, reporting is what a dashboard may read.
-- `admin` and `maintenance` are created far below, beside their grants.
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'store') THEN
        CREATE ROLE store NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'reporting') THEN
        CREATE ROLE reporting NOLOGIN;
    END IF;
    -- NOSUPERUSER is the point: `RESET ROLE` must not restore a superuser, or
    -- every REVOKE below is decorative. The password is set by operations.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'store_svc') THEN
        CREATE ROLE store_svc LOGIN NOSUPERUSER IN ROLE store;
    END IF;
END
$$;

CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

-- Refuses any UPDATE or DELETE; a correction is a new row.
CREATE FUNCTION forbid_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    -- The one permitted mutation: the ON DELETE SET NULL that nulls
    -- actor_user_id when the acting user is erased. Everything else must be
    -- byte-identical, so a value becomes unknown and never false.
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(NEW) - 'actor_user_id') = (to_jsonb(OLD) - 'actor_user_id')
       AND to_jsonb(NEW) ->> 'actor_user_id' IS NULL THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION '% is append-only; correct it with a new row', TG_TABLE_NAME
        USING ERRCODE = 'check_violation', CONSTRAINT = TG_ARGV[0];
END;
$$;

CREATE TABLE brands (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    slug       text NOT NULL,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT brands_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT brands_name_present CHECK (name ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX brands_slug_key ON brands (slug);

CREATE TRIGGER brands_set_updated_at
    BEFORE UPDATE ON brands
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE categories (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    parent_id  uuid REFERENCES categories (id) ON DELETE RESTRICT,
    slug       text NOT NULL,
    name       text NOT NULL,
    -- The English name, or NULL for one the shop has not translated; NULL falls
    -- back to `name` at read time, and a blank would render an empty nav item.
    name_en    text,
    icon_key   text,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT categories_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT categories_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT categories_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT categories_not_own_parent CHECK (parent_id IS DISTINCT FROM id)
);

-- Position decides the order of the header and of the home page's tiles, and
-- CreateCategory computes it as max(position) + 1 — which two staff members
-- adding a category at the same moment both read. product_specs and faq_entries
-- have carried the same index for the same reason since they were written; this
-- table computed the position the same way and had nothing behind it, so the
-- collision was silent and the shop got an order nobody chose.
--
-- NULLS NOT DISTINCT because parent_id is nullable and the ROOT categories are
-- exactly the rows that matter here: they are the header. Without it PostgreSQL
-- treats every root as distinct and the index constrains only the subtrees.
CREATE UNIQUE INDEX categories_position_key
    ON categories (parent_id, position) NULLS NOT DISTINCT;

CREATE FUNCTION localized_name(zh_hant text, en text, locale text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT CASE WHEN locale = 'en' AND en IS NOT NULL THEN en ELSE zh_hant END;
$$;

COMMENT ON FUNCTION localized_name(text, text, text) IS
    'The name a reader in locale gets, falling back to the Traditional Chinese '
    'one. One definition: a page whose header and breadcrumb disagree about a '
    'category name is the failure this prevents.';

-- Its GRANT is far below: `admin` and `reporting` do not exist yet here, and a
-- GRANT naming a role the file has not created fails the migration outright.

CREATE UNIQUE INDEX categories_slug_key ON categories (slug);
CREATE INDEX categories_parent_id_idx ON categories (parent_id);

CREATE TRIGGER categories_set_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- categories_not_own_parent stops A → A and nothing else; A → B → A passes row
-- by row. The advisory lock is what makes the ancestor walk correct under
-- concurrency.
CREATE FUNCTION categories_reject_cycle() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    ancestor uuid;
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;

    PERFORM pg_advisory_xact_lock(hashtext('categories_tree'));

    WITH RECURSIVE up AS (
        SELECT NEW.parent_id AS id
        UNION ALL
        SELECT c.parent_id FROM categories c JOIN up ON c.id = up.id
        WHERE c.parent_id IS NOT NULL
    )
    SELECT id INTO ancestor FROM up WHERE id = NEW.id LIMIT 1;

    IF ancestor IS NOT NULL THEN
        RAISE EXCEPTION 'category % would become its own ancestor', NEW.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'categories_acyclic';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER categories_acyclic
    BEFORE INSERT OR UPDATE OF parent_id ON categories
    FOR EACH ROW EXECUTE FUNCTION categories_reject_cycle();

CREATE TABLE products (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    brand_id      uuid NOT NULL REFERENCES brands (id) ON DELETE RESTRICT,
    category_id   uuid NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    slug          text NOT NULL,
    name          text NOT NULL,
    summary       text,
    description   text NOT NULL DEFAULT '',
    -- The English copy, each half separately optional. goen never invents a
    -- translation; a shop that has one can say so, and the read falls back.
    name_en       text,
    summary_en    text,
    description_en text,
    warranty_note text,
    -- Months of cover, or NULL when the shop has not stated a term — which
    -- refuses registration rather than computing an expiry nobody promised.
    warranty_months integer,
    status        text NOT NULL DEFAULT 'draft',
    published_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_warranty_months_sane
        CHECK (warranty_months IS NULL OR (warranty_months > 0 AND warranty_months <= 120)),
    CONSTRAINT products_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT products_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT products_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT products_summary_en_present
        CHECK (summary_en IS NULL OR summary_en ~ '[^[:space:]]'),
    CONSTRAINT products_description_en_present
        CHECK (description_en IS NULL OR description_en ~ '[^[:space:]]'),
    CONSTRAINT products_status_known CHECK (status IN ('draft', 'active', 'archived')),
    CONSTRAINT products_active_is_published
        CHECK (status <> 'active' OR published_at IS NOT NULL)
);

CREATE UNIQUE INDEX products_slug_key ON products (slug);
CREATE INDEX products_brand_id_idx ON products (brand_id);
CREATE INDEX products_category_id_idx ON products (category_id);

-- The listing's default read. id is the tie-breaker: a batch published in one
-- transaction shares a timestamp to the microsecond, and keyset pagination would
-- otherwise skip the rest of the batch.
CREATE INDEX products_category_published_idx
    ON products (category_id, published_at DESC, id DESC)
    WHERE status = 'active';

CREATE INDEX products_category_brand_published_idx
    ON products (category_id, brand_id, published_at DESC, id DESC)
    WHERE status = 'active';

-- Measured at 10,000 products: a Latin query is a bitmap index scan at 1.5 ms,
-- while a two-character Chinese query is far too unselective for the planner and
-- scans at 8.8 ms.
CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);

CREATE TRIGGER products_set_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE product_images (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id  uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    storage_key text NOT NULL,
    alt_text    text NOT NULL,
    -- The English alt text, or NULL. A screen reader announces it in the
    -- language <html lang> declares, so the Chinese fallback is mispronounced
    -- rather than silent.
    alt_text_en text,
    width       integer,
    height      integer,
    position    integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_images_storage_key_present CHECK (storage_key ~ '[^[:space:]]'),
    CONSTRAINT product_images_alt_present CHECK (alt_text ~ '[^[:space:]]'),
    CONSTRAINT product_images_alt_en_present
        CHECK (alt_text_en IS NULL OR alt_text_en ~ '[^[:space:]]'),
    CONSTRAINT product_images_width_positive CHECK (width IS NULL OR width > 0),
    CONSTRAINT product_images_height_positive CHECK (height IS NULL OR height > 0),
    CONSTRAINT product_images_position_non_negative CHECK (position >= 0)
);

CREATE UNIQUE INDEX product_images_position_key ON product_images (product_id, position);
-- Per PRODUCT rather than on storage_key alone: content-addressed uploads give
-- one picture one digest, so a generic accessory shot shared by two products
-- would be refused as a duplicate key.
CREATE UNIQUE INDEX product_images_storage_key_key
    ON product_images (product_id, storage_key);

-- The axes a product varies along.
CREATE TABLE product_options (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- A LABEL and never an identifier: the picker puts the canonical `name` in
    -- the URL, so selecting on what is displayed would resolve differently for a
    -- reader in another language.
    name_en    text,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_options_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT product_options_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX product_options_name_key ON product_options (product_id, name);
-- Referenced by variant_option_values, so a value can only pair with an option
-- of the same product.
CREATE UNIQUE INDEX product_options_product_key ON product_options (product_id, id);

-- The values on one axis.
CREATE TABLE product_option_values (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Carried so the composite foreign keys can bind a value to an option of the
    -- same product; pinned by the key below, so it cannot disagree.
    product_id uuid NOT NULL,
    option_id  uuid NOT NULL,
    value      text NOT NULL,
    -- A label, exactly like product_options.name_en: `value` is what the URL
    -- selects on and what variant matching compares.
    value_en   text,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_option_values_value_present CHECK (value ~ '[^[:space:]]'),
    CONSTRAINT product_option_values_value_en_present
        CHECK (value_en IS NULL OR value_en ~ '[^[:space:]]'),
    CONSTRAINT product_option_values_option_fk
        FOREIGN KEY (product_id, option_id) REFERENCES product_options (product_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX product_option_values_value_key ON product_option_values (option_id, value);
CREATE UNIQUE INDEX product_option_values_option_key
    ON product_option_values (product_id, option_id, id);

-- The sellable unit: price and stock live here, never on the product.
CREATE TABLE product_variants (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id             uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    sku                    text NOT NULL,
    price_cents            bigint NOT NULL,
    -- The struck-through "was" price; NULL means not on sale, and the discount
    -- percentage is computed from the pair.
    compare_at_price_cents bigint,
    -- A projection of inventory_movements; record_inventory_movement() is the
    -- only writer.
    stock_quantity         integer NOT NULL DEFAULT 0,
    safety_stock           integer NOT NULL DEFAULT 0,
    preorder_release_on    date,
    -- The parcel this variant ships as, in millimetres and grams. All NULLABLE,
    -- and NULL means UNMEASURED rather than unlimited: a shipping method may only
    -- be refused on a figure that exists.
    parcel_longest_mm      integer,
    parcel_sum_mm          integer,
    parcel_weight_g        integer,
    position               integer NOT NULL DEFAULT 0,
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_variants_sku_format CHECK (sku ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'),
    -- The ceilings are deliberately loose: these refuse a typo, not a decision.
    CONSTRAINT product_variants_parcel_longest_sane
        CHECK (parcel_longest_mm IS NULL OR (parcel_longest_mm > 0 AND parcel_longest_mm <= 5000)),
    CONSTRAINT product_variants_parcel_sum_sane
        CHECK (parcel_sum_mm IS NULL OR (parcel_sum_mm > 0 AND parcel_sum_mm <= 15000)),
    CONSTRAINT product_variants_parcel_weight_sane
        CHECK (parcel_weight_g IS NULL OR (parcel_weight_g > 0 AND parcel_weight_g <= 200000)),
    CONSTRAINT product_variants_parcel_sum_covers_longest
        CHECK (parcel_sum_mm IS NULL OR parcel_longest_mm IS NULL
               OR parcel_sum_mm >= parcel_longest_mm),
    -- Far above any 3C price and far below where quantity x price overflows
    -- bigint.
    CONSTRAINT product_variants_price_in_range
        CHECK (price_cents >= 0 AND price_cents <= 10000000000),
    CONSTRAINT product_variants_stock_non_negative CHECK (stock_quantity >= 0),
    CONSTRAINT product_variants_safety_stock_non_negative CHECK (safety_stock >= 0),
    CONSTRAINT product_variants_compare_at_is_higher
        CHECK (compare_at_price_cents IS NULL OR compare_at_price_cents > price_cents)
);

CREATE UNIQUE INDEX product_variants_sku_key ON product_variants (sku);
CREATE UNIQUE INDEX product_variants_position_key ON product_variants (product_id, position);
CREATE UNIQUE INDEX product_variants_product_key ON product_variants (product_id, id);

CREATE INDEX product_variants_sellable_price_idx
    ON product_variants (product_id, price_cents, id)
    WHERE is_active AND stock_quantity > safety_stock;

-- The admin's low-stock queue: an index on
-- stock_quantity alone cannot answer "below its own safety level".
CREATE INDEX product_variants_low_stock_idx
    ON product_variants (product_id, stock_quantity)
    WHERE is_active AND stock_quantity <= safety_stock;

CREATE TRIGGER product_variants_set_updated_at
    BEFORE UPDATE ON product_variants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- An active product must have something to sell: published with no variant, its
-- page has no price and every listing drops it. DEFERRED, because the variants
-- are inserted after the product row they reference.
CREATE FUNCTION products_check_sellable() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    p products%ROWTYPE;
    sellable integer;
BEGIN
    -- Separate branches rather than a coalesce: on DELETE there is no NEW
    -- record to read at all.
    IF TG_TABLE_NAME = 'products' THEN
        SELECT * INTO p FROM products WHERE id = NEW.id;
    ELSIF TG_OP = 'DELETE' THEN
        SELECT * INTO p FROM products WHERE id = OLD.product_id;
    ELSE
        SELECT * INTO p FROM products WHERE id = NEW.product_id;
    END IF;
    IF NOT FOUND OR p.status <> 'active' THEN
        RETURN NULL;  -- deleted in this transaction, or not published anyway
    END IF;

    SELECT count(*) INTO sellable
    FROM product_variants WHERE product_id = p.id AND is_active;

    IF sellable = 0 THEN
        RAISE EXCEPTION 'product % is active with nothing to sell', p.slug
            USING ERRCODE = 'check_violation', CONSTRAINT = 'products_active_has_variant';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER products_active_has_variant
    AFTER INSERT OR UPDATE OF status ON products
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION products_check_sellable();

-- The same state reached from the other side.
CREATE CONSTRAINT TRIGGER product_variants_keep_product_sellable
    AFTER UPDATE OF is_active OR DELETE ON product_variants
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION products_check_sellable();

-- Which point on each axis a variant sits at. product_id and option_id are
-- carried so the composite keys refuse a variant of A paired with a value of B,
-- and the primary key stops one variant being two colours at once.
CREATE TABLE variant_option_values (
    product_id      uuid NOT NULL,
    variant_id      uuid NOT NULL,
    option_id       uuid NOT NULL,
    option_value_id uuid NOT NULL,
    PRIMARY KEY (variant_id, option_id),
    CONSTRAINT variant_option_values_variant_fk
        FOREIGN KEY (product_id, variant_id) REFERENCES product_variants (product_id, id)
        ON DELETE CASCADE,
    CONSTRAINT variant_option_values_value_fk
        FOREIGN KEY (product_id, option_id, option_value_id)
        REFERENCES product_option_values (product_id, option_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX variant_option_values_value_variant_idx
    ON variant_option_values (option_value_id, variant_id);
-- The primary key leads with variant_id, so it cannot serve the composite
-- foreign key.
CREATE INDEX variant_option_values_product_variant_idx
    ON variant_option_values (product_id, variant_id);
CREATE INDEX variant_option_values_product_option_idx
    ON variant_option_values (product_id, option_id, option_value_id);

CREATE TABLE product_specs (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    label      text NOT NULL,
    value      text NOT NULL,
    -- The English pair, each half separately optional: a spec label needs
    -- translating and a number barely at all.
    label_en   text,
    value_en   text,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_specs_label_present CHECK (label ~ '[^[:space:]]'),
    CONSTRAINT product_specs_label_en_present
        CHECK (label_en IS NULL OR label_en ~ '[^[:space:]]'),
    CONSTRAINT product_specs_value_en_present
        CHECK (value_en IS NULL OR value_en ~ '[^[:space:]]'),
    CONSTRAINT product_specs_label_en_bounded CHECK (length(label_en) <= 40),
    CONSTRAINT product_specs_value_en_bounded CHECK (length(value_en) <= 200),
    CONSTRAINT product_specs_value_present CHECK (value ~ '[^[:space:]]'),
    -- Bounded in CHARACTERS: length() counts characters, so a byte limit would
    -- give a Chinese label a third of the room an English one gets. A spec is a
    -- table cell on /compare, and a label longer than this wraps to three lines.
    CONSTRAINT product_specs_label_bounded CHECK (length(label) <= 40),
    CONSTRAINT product_specs_value_bounded CHECK (length(value) <= 200)
);

CREATE UNIQUE INDEX product_specs_position_key ON product_specs (product_id, position);
CREATE INDEX product_specs_label_idx ON product_specs (label);

CREATE TABLE users (
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    email             text NOT NULL,
    -- NULL for an account that has only ever signed in through Google.
    password_hash     text,
    full_name         text,
    phone             text,
    role              text NOT NULL DEFAULT 'customer',
    email_verified_at timestamptz,
    last_login_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_present CHECK (email ~ '[^[:space:]]'),
    -- No surrounding whitespace of any kind, or the folded unique index below
    -- would hold two rows for one mailbox.
    CONSTRAINT users_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT users_role_known CHECK (role IN ('customer', 'staff', 'admin'))
);

-- Two addresses differing only in case are one mailbox.
CREATE UNIQUE INDEX users_email_key ON users (lower(email));
CREATE INDEX users_role_idx ON users (role) WHERE role <> 'customer';

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_identities (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider         text NOT NULL,
    provider_subject text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_identities_provider_known CHECK (provider IN ('google')),
    CONSTRAINT user_identities_subject_present CHECK (provider_subject ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX user_identities_provider_subject_key
    ON user_identities (provider, provider_subject);
CREATE UNIQUE INDEX user_identities_user_provider_key
    ON user_identities (user_id, provider);

CREATE TABLE staff_totp_credentials (
    user_id          uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    -- AES-256-GCM, keyed from the environment: a TOTP secret is a
    -- password-equivalent, so a database dump alone must not defeat the factor.
    secret_encrypted bytea NOT NULL,
    -- NULL until the enrolling person has proved they can generate a code, or
    -- a mistyped secret locks them out of what it protects.
    confirmed_at     timestamptz,
    -- The most recent time step accepted. A code is valid for its whole
    -- 30-second step, and 90 seconds with the skew window either side, so
    -- requiring a strictly greater step is what makes each code single-use.
    last_step        bigint,
    created_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT staff_totp_secret_present CHECK (octet_length(secret_encrypted) > 0),
    CONSTRAINT staff_totp_step_needs_confirmation
        CHECK (last_step IS NULL OR confirmed_at IS NOT NULL)
);

-- Sessions store a hash, never the cookie value.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent text,
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- When this session last proved a second factor, and NULL for every customer
    -- session. The factor guards the BACK OFFICE and not the sign-in, so there is
    -- no half-authenticated state to keep anywhere.
    totp_verified_at timestamptz,
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT sessions_totp_after_creation
        CHECK (totp_verified_at IS NULL OR totp_verified_at >= created_at)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE password_reset_tokens (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    CONSTRAINT password_reset_tokens_expiry_after_creation CHECK (expires_at > created_at)
);

-- The back office's customer search. users already has a UNIQUE index on
-- lower(email), but a btree under the default collation cannot serve a prefix
-- LIKE, so this is a second index rather than a duplicate one.
CREATE INDEX users_email_prefix_idx ON users (lower(email) text_pattern_ops);
CREATE INDEX users_name_prefix_idx ON users (full_name text_pattern_ops);

CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

-- An address waiting to be proved. ONE table for two acts: proving the address
-- somebody registered with, and proving a new one they want to move to. The
-- change takes effect only on confirmation, so mail keeps reaching the old one.
CREATE TABLE email_verifications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The address being proved. Not a foreign key to anything: it may not be
    -- the user's current address, which is the case this table exists for.
    email      text NOT NULL,
    -- sha256 of the token in the link. Spent on first use and expiring, so
    -- nothing needs to reproduce it.
    digest     bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT email_verifications_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT email_verifications_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT email_verifications_digest_sha256 CHECK (octet_length(digest) = 32),
    CONSTRAINT email_verifications_expires_after_created CHECK (expires_at > created_at)
);

-- One outstanding request per customer: asking again REPLACES the previous, so
-- a mailbox holds one live link rather than three.
CREATE UNIQUE INDEX email_verifications_user_key ON email_verifications (user_id);
CREATE UNIQUE INDEX email_verifications_digest_key ON email_verifications (digest);

CREATE TABLE addresses (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label          text,
    recipient_name text NOT NULL,
    phone          text NOT NULL,
    postal_code    text NOT NULL,
    city           text NOT NULL,
    district       text NOT NULL,
    street         text NOT NULL,
    is_default     boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT addresses_recipient_present CHECK (recipient_name ~ '[^[:space:]]'),
    CONSTRAINT addresses_phone_present CHECK (phone ~ '[^[:space:]]'),
    CONSTRAINT addresses_postal_code_present CHECK (postal_code ~ '[^[:space:]]'),
    CONSTRAINT addresses_city_present CHECK (city ~ '[^[:space:]]'),
    CONSTRAINT addresses_district_present CHECK (district ~ '[^[:space:]]'),
    CONSTRAINT addresses_street_present CHECK (street ~ '[^[:space:]]')
);

CREATE INDEX addresses_user_id_idx ON addresses (user_id);
-- "Default" is singular, said where a forgetful code path cannot bypass it.
CREATE UNIQUE INDEX addresses_one_default_per_user
    ON addresses (user_id)
    WHERE is_default;

CREATE TRIGGER addresses_set_updated_at
    BEFORE UPDATE ON addresses
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================================
-- Store credit
--
-- A ledger, so the balance is the sum of its explanations. The account row exists
-- to be locked: without one row to take FOR UPDATE, two concurrent spends each
-- read the same balance and both pass.
-- ============================================================================

CREATE TABLE store_credit_accounts (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- SET NULL, not RESTRICT: erasure must not be held hostage by a balance,
    -- and the ledger keeps its own account_id.
    user_id    uuid UNIQUE REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE store_credit_entries (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    account_id     uuid NOT NULL REFERENCES store_credit_accounts (id) ON DELETE RESTRICT,
    amount_cents   bigint NOT NULL,
    reason         text NOT NULL,
    -- The caller's name for this posting: a retried checkout submits the same
    -- key and meets the unique index instead of writing a second debit.
    idempotency_key text NOT NULL,
    -- The order this posting settles, when there is one. The foreign key is
    -- added after `orders` is created, further down.
    order_id       uuid,
    reverses_id    uuid REFERENCES store_credit_entries (id) ON DELETE RESTRICT,
    -- Who posted this, when a person did. NULL for a checkout spending credit
    -- on its own, and SET NULL on delete so erasing an employee is not blocked.
    actor_user_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT store_credit_entries_amount_non_zero CHECK (amount_cents <> 0),
    -- Symmetric ceiling, or the guard's running balance could be driven to
    -- overflow bigint instead of raising store_credit_never_negative.
    CONSTRAINT store_credit_entries_amount_in_range
        CHECK (amount_cents BETWEEN -10000000000 AND 10000000000),
    CONSTRAINT store_credit_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT store_credit_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

-- A reversal undoes exactly one entry, once.
CREATE UNIQUE INDEX store_credit_entries_reverses_key
    ON store_credit_entries (reverses_id) WHERE reverses_id IS NOT NULL;

CREATE UNIQUE INDEX store_credit_entries_idempotency_key
    ON store_credit_entries (idempotency_key);
CREATE INDEX store_credit_entries_account_idx ON store_credit_entries (account_id, created_at DESC);
CREATE INDEX store_credit_entries_order_idx ON store_credit_entries (order_id);
CREATE INDEX store_credit_entries_actor_idx ON store_credit_entries (actor_user_id);

-- Locks the account before it sums, so two concurrent debits serialise rather
-- than both reading a balance that is about to be spent.
CREATE FUNCTION store_credit_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    balance bigint;
    original store_credit_entries%ROWTYPE;
    o_id uuid;
    o_status text;
    o_paid boolean;
BEGIN
    PERFORM 1 FROM store_credit_accounts WHERE id = NEW.account_id FOR UPDATE;

    IF NEW.reverses_id IS NOT NULL THEN
        SELECT * INTO original FROM store_credit_entries WHERE id = NEW.reverses_id FOR UPDATE;
        IF original.account_id <> NEW.account_id OR NEW.amount_cents <> -original.amount_cents THEN
            RAISE EXCEPTION 'a reversal must negate one entry of the same account'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_never_negative';
        END IF;
        -- A reversal cannot itself be reversed: it carries no order_id, so the
        -- order-state rules below would not see the chain at all.
        IF original.reverses_id IS NOT NULL THEN
            RAISE EXCEPTION 'a reversal cannot itself be reversed; post a new entry to correct'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_reversal_single_layer';
        END IF;
    END IF;

    -- Lock the ORDER first. A posting attributed to one takes part in its
    -- funding, so it has to serialise against the fulfilment transition, which
    -- locks the same row.
    o_id := CASE WHEN NEW.reverses_id IS NOT NULL THEN original.order_id ELSE NEW.order_id END;
    IF o_id IS NOT NULL THEN
        SELECT fulfillment_status,
               EXISTS (SELECT 1 FROM payments WHERE order_id = o_id AND status = 'succeeded')
        INTO o_status, o_paid
        FROM orders WHERE id = o_id FOR UPDATE;

        IF NEW.reverses_id IS NULL AND NEW.amount_cents < 0 THEN
            -- A SPEND is part of paying for it: only while the checkout is
            -- still open and unpaid.
            IF o_status <> 'pending' OR o_paid THEN
                RAISE EXCEPTION 'store credit cannot be spent on order % (status %, paid %)',
                    o_id, o_status, o_paid
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_posting_matches_order';
            END IF;
        ELSIF NEW.reverses_id IS NULL THEN
            -- A POSITIVE entry on an order is a COMPENSATION, legal only once
            -- the order is settled. Testing the state without the SIGN makes a
            -- spend and a return one rule, so a credit-funded return cannot be paid.
            IF o_status = 'pending' AND NOT o_paid THEN
                RAISE EXCEPTION 'order % is an open unpaid checkout; reverse the spend rather than compensating it',
                    o_id
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_posting_matches_order';
            END IF;
        ELSE
            -- Reversing a spend un-funds the order: legal only while it is an
            -- unpaid checkout or after it was cancelled.
            IF NOT ((o_status = 'pending' AND NOT o_paid) OR o_status = 'cancelled') THEN
                RAISE EXCEPTION 'store credit spend on order % cannot be reversed (status %, paid %)',
                    o_id, o_status, o_paid
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_posting_matches_order';
            END IF;
        END IF;
    END IF;

    SELECT coalesce(sum(amount_cents), 0) INTO balance
    FROM store_credit_entries
    WHERE account_id = NEW.account_id AND id <> NEW.id;

    IF balance + NEW.amount_cents < 0 THEN
        RAISE EXCEPTION 'store credit would go negative: % + %', balance, NEW.amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_never_negative';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER store_credit_never_negative
    BEFORE INSERT ON store_credit_entries
    FOR EACH ROW EXECUTE FUNCTION store_credit_guard();

CREATE TRIGGER store_credit_entries_append_only
    BEFORE UPDATE OR DELETE ON store_credit_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_change('store_credit_entries_append_only');

-- ============================================================================
-- Inventory
--
-- inventory_movements is the truth and product_variants.stock_quantity is a
-- projection of it. Both are written by one function, which holds the variant
-- row while it does: two sessions can each read 1 and each write 0.
-- ============================================================================

CREATE TABLE inventory_movements (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    variant_id      uuid NOT NULL REFERENCES product_variants (id) ON DELETE RESTRICT,
    delta           integer NOT NULL,
    reason          text NOT NULL,
    source_type     text,
    source_id       uuid,
    idempotency_key text NOT NULL,
    actor_user_id   uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT inventory_movements_delta_non_zero CHECK (delta <> 0),
    CONSTRAINT inventory_movements_reason_known CHECK (reason IN (
        'receipt',
        'hold',
        'sale',
        'release',
        'return',
        'adjustment'
    )),
    -- Stock comes IN on a receipt, release or return and OUT on a hold or sale.
    -- Only a manual adjustment may go either way.
    CONSTRAINT inventory_movements_delta_direction CHECK (
        CASE reason
            WHEN 'receipt' THEN delta > 0
            WHEN 'release' THEN delta > 0
            WHEN 'return'  THEN delta > 0
            WHEN 'hold'    THEN delta < 0
            WHEN 'sale'    THEN delta < 0
            ELSE true
        END
    ),
    CONSTRAINT inventory_movements_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX inventory_movements_idempotency_key
    ON inventory_movements (idempotency_key);
CREATE INDEX inventory_movements_variant_idx ON inventory_movements (variant_id, created_at DESC);
CREATE INDEX inventory_movements_source_idx ON inventory_movements (source_type, source_id);
CREATE INDEX inventory_movements_actor_idx ON inventory_movements (actor_user_id);

CREATE TRIGGER inventory_movements_append_only
    BEFORE UPDATE OR DELETE ON inventory_movements
    FOR EACH ROW EXECUTE FUNCTION forbid_change('inventory_movements_append_only');

-- The only writer of stock_quantity. The availability test is in the UPDATE's
-- own WHERE clause, so the read and the write are one statement on one locked
-- row and zero rows updated means there was not enough.
CREATE FUNCTION record_inventory_movement(
    p_variant_id uuid,
    p_delta integer,
    p_reason text,
    p_idempotency_key text,
    p_source_type text DEFAULT NULL,
    p_source_id uuid DEFAULT NULL,
    p_actor uuid DEFAULT NULL
) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    remaining integer;
BEGIN
    -- A sale or a hold may not take stock below the safety level; a receipt,
    -- return or manual correction may.
    UPDATE product_variants
    SET stock_quantity = stock_quantity + p_delta
    WHERE id = p_variant_id
      AND stock_quantity + p_delta >= CASE
              WHEN p_reason IN ('sale', 'hold') THEN safety_stock
              ELSE 0
          END
    RETURNING stock_quantity INTO remaining;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'variant % cannot absorb a % of %', p_variant_id, p_reason, p_delta
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_never_negative';
    END IF;

    INSERT INTO inventory_movements
        (variant_id, delta, reason, source_type, source_id, idempotency_key, actor_user_id)
    VALUES
        (p_variant_id, p_delta, p_reason, p_source_type, p_source_id, p_idempotency_key, p_actor);

    RETURN remaining;
END;
$$;

-- A hold taken while the customer is paying: stock leaves when the reservation
-- is created and returns when it expires.
CREATE TABLE inventory_reservations (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    -- The foreign key is added after `orders` is created, further down: a hold
    -- is taken during checkout, before the order exists.
    order_id    uuid NOT NULL,
    variant_id  uuid NOT NULL REFERENCES product_variants (id) ON DELETE RESTRICT,
    quantity    integer NOT NULL,
    state       text NOT NULL DEFAULT 'held',
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    settled_at  timestamptz,
    CONSTRAINT inventory_reservations_quantity_positive CHECK (quantity > 0),
    CONSTRAINT inventory_reservations_state_known
        CHECK (state IN ('held', 'consumed', 'released')),
    CONSTRAINT inventory_reservations_settled_has_state
        CHECK ((state = 'held') = (settled_at IS NULL)),
    CONSTRAINT inventory_reservations_expiry_after_creation CHECK (expires_at > created_at)
);

-- At most one LIVE hold per (order, variant). Partial on state='held', so a
-- released hold does not occupy the slot forever and the same order can hold
-- again.
CREATE UNIQUE INDEX inventory_reservations_order_variant_key
    ON inventory_reservations (order_id, variant_id)
    WHERE state = 'held';
-- The unique index above is partial, so it serves the order_id foreign key
-- only for held rows.
CREATE INDEX inventory_reservations_order_idx ON inventory_reservations (order_id);
CREATE INDEX inventory_reservations_variant_idx ON inventory_reservations (variant_id);
CREATE INDEX inventory_reservations_expiring_idx
    ON inventory_reservations (expires_at)
    WHERE state = 'held';

-- Take a hold: decrement stock through the ledger and record the reservation,
-- in one transaction.
CREATE FUNCTION hold_inventory(
    p_order_id uuid,
    p_variant_id uuid,
    p_quantity integer,
    p_expires_at timestamptz,
    p_idempotency_key text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    reservation_id uuid;
BEGIN
    IF p_quantity <= 0 THEN
        RAISE EXCEPTION 'a hold must be for a positive quantity'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservations_quantity_positive';
    END IF;

    -- The idempotency key is the CALLER's: a retry of the same attempt is a
    -- no-op through the movement's unique key, while a genuinely new hold after
    -- a release passes a fresh one.
    PERFORM record_inventory_movement(
        p_variant_id, -p_quantity, 'hold',
        p_idempotency_key, 'order', p_order_id, NULL);

    INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
    VALUES (p_order_id, p_variant_id, p_quantity, p_expires_at)
    RETURNING id INTO reservation_id;

    RETURN reservation_id;
END;
$$;

-- Consume a hold at fulfilment. It records NO stock movement: the stock left
-- when the hold was taken, and posting another would return the item at the
-- moment it is sold. The conditional UPDATE is the lock.
CREATE FUNCTION consume_reservation(p_reservation_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE inventory_reservations
    SET state = 'consumed', settled_at = now()
    WHERE id = p_reservation_id AND state = 'held';

    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;
END;
$$;

-- Consume PART of a hold. Splitting rather than a consumed_quantity column, so
-- one row records one settled fact; the consumed row carries the ORIGINAL's
-- created_at and expires_at, which is what keeps expiry_after_creation true.
CREATE FUNCTION consume_reservation_partial(p_reservation_id uuid, p_quantity integer)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    r inventory_reservations%ROWTYPE;
BEGIN
    SELECT * INTO r FROM inventory_reservations
    WHERE id = p_reservation_id FOR UPDATE;

    IF NOT FOUND OR r.state <> 'held' THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;

    IF p_quantity <= 0 OR p_quantity > r.quantity THEN
        RAISE EXCEPTION 'cannot consume % of reservation % which holds %',
            p_quantity, p_reservation_id, r.quantity
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_consume_within_hold';
    END IF;

    IF p_quantity = r.quantity THEN
        UPDATE inventory_reservations
        SET state = 'consumed', settled_at = now()
        WHERE id = p_reservation_id;
        RETURN;
    END IF;

    UPDATE inventory_reservations
    SET quantity = quantity - p_quantity
    WHERE id = p_reservation_id;

    INSERT INTO inventory_reservations
        (order_id, variant_id, quantity, state, expires_at, created_at, settled_at)
    VALUES (r.order_id, r.variant_id, p_quantity, 'consumed',
            r.expires_at, r.created_at, now());
END;
$$;

-- Release a hold — cancelled checkout or expiry sweep. Refused on a COMMITTED
-- order, and separately on one that OWES NOTHING: a store-credited order has no
-- payment row, so the view cannot see it and the sweeper would take its stock.
CREATE FUNCTION release_reservation(p_reservation_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    r inventory_reservations%ROWTYPE;
    o_status text;
BEGIN
    SELECT * INTO r FROM inventory_reservations WHERE id = p_reservation_id FOR UPDATE;
    IF NOT FOUND OR r.state <> 'held' THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;

    -- Variant first, then order — the order hold_inventory takes them in.
    -- Reversed, a concurrent re-hold and release of the same pair close a cycle
    -- and PostgreSQL aborts one with 40P01, which no caller retries.
    PERFORM 1 FROM product_variants WHERE id = r.variant_id FOR UPDATE;
    SELECT o.fulfillment_status INTO o_status
    FROM orders o WHERE o.id = r.order_id FOR UPDATE;
    -- COMMITTED, not settled: a cancelled order's stock must come back.
    IF order_is_committed(r.order_id) THEN
        RAISE EXCEPTION 'reservation % is on a committed order; consume it, do not release', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_committed_no_release';
    END IF;

    -- FUNDED but not committed — the zero-owed order, paid for and still
    -- pending. 'cancelled' is excluded because its stock must come back and the
    -- credit reversal runs after the status move.
    IF o_status <> 'cancelled' AND order_amount_owed(r.order_id) = 0 THEN
        RAISE EXCEPTION 'reservation % is on an order that owes nothing; consume it, do not release', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_funded_no_release';
    END IF;

    UPDATE inventory_reservations
    SET state = 'released', settled_at = now()
    WHERE id = p_reservation_id AND state = 'held';

    PERFORM record_inventory_movement(
        r.variant_id, r.quantity, 'release',
        'release:' || r.id, 'reservation', r.id, NULL);
END;
$$;

-- A cart exists before an account does: it is identified by a cookie token and
-- adopted on sign-in.
CREATE TABLE carts (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX carts_token_hash_key ON carts (token_hash);
-- One cart per account, or a merge that runs twice leaves two. Partial, but a
-- lookup by user_id is always `WHERE user_id = $1`, so it serves the foreign key
-- as well.
CREATE UNIQUE INDEX carts_one_per_user ON carts (user_id) WHERE user_id IS NOT NULL;

CREATE TRIGGER carts_set_updated_at
    BEFORE UPDATE ON carts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One row per variant: adding the same variant twice raises the quantity.
CREATE TABLE cart_items (
    cart_id    uuid NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    variant_id uuid NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    quantity   integer NOT NULL,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cart_id, variant_id),
    CONSTRAINT cart_items_quantity_in_range CHECK (quantity > 0 AND quantity <= 999)
);

CREATE INDEX cart_items_variant_id_idx ON cart_items (variant_id);

-- Server-issued, so a double-click resolves to the order it already created
-- instead of a second order and a second payment.
CREATE TABLE checkout_attempts (
    idempotency_key text PRIMARY KEY,
    cart_id         uuid REFERENCES carts (id) ON DELETE SET NULL,
    -- Filled in once the attempt has produced an order. The foreign key is
    -- added after `orders` exists, further down.
    order_id        uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT checkout_attempts_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

-- The retention sweep's range; without it the daily delete is a sequential scan
-- over every checkout goen has ever seen.
CREATE INDEX checkout_attempts_created_at_idx ON checkout_attempts (created_at);

-- Partial on order_id IS NOT NULL, which a lookup by order_id implies, so this
-- also serves the foreign key.
CREATE UNIQUE INDEX checkout_attempts_order_key ON checkout_attempts (order_id)
    WHERE order_id IS NOT NULL;
CREATE INDEX checkout_attempts_cart_idx ON checkout_attempts (cart_id);

CREATE TABLE wishlist_items (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, product_id)
);

CREATE INDEX wishlist_items_product_id_idx ON wishlist_items (product_id);

-- Restock notices, keyed by variant and address so a guest can ask too.
CREATE TABLE stock_notifications (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    variant_id  uuid NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    email       text NOT NULL,
    -- The language the visitor was reading. Kept here for the reason
    -- orders.locale is: the notice is produced with nobody present.
    locale      text NOT NULL DEFAULT 'zh-Hant',
    created_at  timestamptz NOT NULL DEFAULT now(),
    notified_at timestamptz,
    CONSTRAINT stock_notifications_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT stock_notifications_locale_known CHECK (locale IN ('zh-Hant', 'en'))
);

-- Asking twice for the same restock is one request.
CREATE UNIQUE INDEX stock_notifications_pending_key
    ON stock_notifications (variant_id, lower(email))
    WHERE notified_at IS NULL;
-- The partial index above covers pending rows only.
CREATE INDEX stock_notifications_variant_id_idx ON stock_notifications (variant_id);
CREATE INDEX stock_notifications_user_id_idx ON stock_notifications (user_id);

CREATE TABLE product_questions (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    -- Nullable: erasure must not take a published question with it, and must
    -- not be blocked by having asked one.
    user_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    body       text NOT NULL,
    -- Hidden by staff. NOT a moderation queue: a question nobody sees is a
    -- question nobody answers.
    hidden_at  timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_questions_body_present CHECK (body ~ '[^[:space:]]'),
    CONSTRAINT product_questions_body_bounded CHECK (length(body) <= 1000)
);

CREATE INDEX product_questions_product_idx
    ON product_questions (product_id, created_at DESC);
CREATE INDEX product_questions_user_id_idx ON product_questions (user_id);

CREATE TABLE product_answers (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    question_id uuid NOT NULL REFERENCES product_questions (id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    body        text NOT NULL,
    -- Whether the SHOP said it, recorded at the moment it was said. Not derived
    -- from the author's current role: a customer who later joins would turn their
    -- old answers official, and a leaver would strip the badge from real ones.
    is_staff    boolean NOT NULL DEFAULT false,
    hidden_at   timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_answers_body_present CHECK (body ~ '[^[:space:]]'),
    CONSTRAINT product_answers_body_bounded CHECK (length(body) <= 2000)
);

CREATE INDEX product_answers_question_idx
    ON product_answers (question_id, created_at);
CREATE INDEX product_answers_user_id_idx ON product_answers (user_id);

CREATE TABLE product_reviews (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id           uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    -- Nullable: erasure must not take a published review with it, and must not
    -- be blocked by having left one.
    user_id              uuid REFERENCES users (id) ON DELETE SET NULL,
    rating               smallint NOT NULL,
    title                text,
    body                 text NOT NULL,
    is_verified_purchase boolean NOT NULL DEFAULT false,
    -- Hidden by staff, and hiding takes the review out of the SCORE as well as
    -- the list: the displayed rating is computed live from these rows, so hiding
    -- one and leaving the average alone would achieve nothing.
    hidden_at            timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_reviews_rating_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT product_reviews_body_present CHECK (body ~ '[^[:space:]]')
);

-- The reviews the shop shows, which is what every rating is computed from. Two
-- callers keep the base table: moderation needs the hidden rows to put them
-- back, and HasReviewed needs them or the customer meets the unique index.
CREATE VIEW visible_reviews AS
    SELECT id, product_id, user_id, rating, title, body,
           is_verified_purchase, created_at
    FROM product_reviews
    WHERE hidden_at IS NULL;

COMMENT ON VIEW visible_reviews IS
    'Reviews that count: not hidden by staff. Every rating and review count '
    'reads this, so hiding one moves the score as well as the list.';

-- The verified-purchase claim is checked against a real committed order rather
-- than trusted from the writer. Only the moment it is SET is guarded: erasure
-- nulls user_id, so re-verifying an erased author is impossible.
CREATE FUNCTION product_reviews_verify_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT NEW.is_verified_purchase THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.is_verified_purchase THEN
        RETURN NEW;
    END IF;
    IF NEW.user_id IS NULL OR NOT EXISTS (
        SELECT 1
        FROM orders o
        JOIN order_lines ol ON ol.order_id = o.id
        JOIN product_variants pv ON pv.id = ol.variant_id
        WHERE o.user_id = NEW.user_id
          AND pv.product_id = NEW.product_id
          AND order_is_committed(o.id)
    ) THEN
        RAISE EXCEPTION 'review on product % claims a verified purchase with no committed order behind it',
            NEW.product_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'product_reviews_verified_is_real';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER product_reviews_verified_is_real
    BEFORE INSERT OR UPDATE OF is_verified_purchase, user_id, product_id ON product_reviews
    FOR EACH ROW EXECUTE FUNCTION product_reviews_verify_purchase();

-- One review per product per person.
CREATE UNIQUE INDEX product_reviews_author_key ON product_reviews (product_id, user_id);
CREATE INDEX product_reviews_product_created_idx ON product_reviews (product_id, created_at DESC);
CREATE INDEX product_reviews_user_id_idx ON product_reviews (user_id);

-- ============================================================================
-- Coupons
--
-- A percentage is basis points — 2500 is 25% — because storing 0.25 as a float
-- is how a discount comes out a cent short on some orders and over on others.
-- ============================================================================

CREATE TABLE coupons (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Compared case-insensitively (the unique index below is on upper(code)),
    -- because a code printed on a card is read by a human.
    code                text NOT NULL,
    description         text NOT NULL,
    kind                text NOT NULL,
    -- Exactly one of these carries the value, decided by kind.
    amount_cents        bigint,
    percent_bp          integer,
    min_subtotal_cents  bigint NOT NULL DEFAULT 0,
    -- How "20% off, up to NT$500" is expressed; NULL means uncapped.
    max_discount_cents  bigint,
    starts_at           timestamptz NOT NULL DEFAULT now(),
    ends_at             timestamptz,
    -- NULL is unlimited; coupon_redemptions is what counts.
    max_redemptions     integer,
    per_customer_limit  integer NOT NULL DEFAULT 1,
    is_active           boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT coupons_code_format CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9-]{1,31}$'),
    CONSTRAINT coupons_description_present CHECK (description ~ '[^[:space:]]'),
    CONSTRAINT coupons_kind_known CHECK (kind IN ('amount', 'percent', 'free_shipping')),
    -- An equivalence per kind, because "amount IS NOT NULL OR percent IS NOT
    -- NULL" would admit both at once and leave the handler to pick.
    CONSTRAINT coupons_value_matches_kind CHECK (
        (kind = 'amount'        AND amount_cents IS NOT NULL AND percent_bp IS NULL)
     OR (kind = 'percent'       AND percent_bp IS NOT NULL   AND amount_cents IS NULL)
     OR (kind = 'free_shipping' AND amount_cents IS NULL     AND percent_bp IS NULL)
    ),
    CONSTRAINT coupons_amount_positive
        CHECK (amount_cents IS NULL OR (amount_cents > 0 AND amount_cents <= 10000000000)),
    CONSTRAINT coupons_percent_in_range
        CHECK (percent_bp IS NULL OR (percent_bp > 0 AND percent_bp <= 10000)),
    CONSTRAINT coupons_min_subtotal_non_negative
        CHECK (min_subtotal_cents >= 0 AND min_subtotal_cents <= 10000000000),
    CONSTRAINT coupons_max_discount_positive
        CHECK (max_discount_cents IS NULL OR max_discount_cents > 0),
    -- On a fixed amount a cap would be a second, quieter amount that silently
    -- overrides the first.
    CONSTRAINT coupons_cap_only_on_percent
        CHECK (max_discount_cents IS NULL OR kind = 'percent'),
    CONSTRAINT coupons_window_ordered CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT coupons_redemptions_positive
        CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    CONSTRAINT coupons_per_customer_positive CHECK (per_customer_limit > 0)
);

-- Case-insensitive: SUMMER20 and summer20 are one code to whoever reads a card,
-- and issuing both is how one of them silently stops working.
CREATE UNIQUE INDEX coupons_code_key ON coupons (upper(code));
CREATE INDEX coupons_active_idx ON coupons (is_active, starts_at, ends_at);

CREATE TRIGGER coupons_set_updated_at
    BEFORE UPDATE ON coupons
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMENT ON COLUMN coupons.percent_bp IS
    'Discount in basis points: 2500 is 25%. Integer, because a float percentage rounds differently on different totals.';
COMMENT ON COLUMN coupons.per_customer_limit IS
    'How many times ONE customer may redeem it. Guests are counted by order, so the limit binds per account only.';

-- ============================================================================
-- Shipping
--
-- Methods are versioned because a fee is a promise made at a moment: changing it
-- must not make last month's orders unexplainable.
-- ============================================================================

CREATE TABLE shipping_methods (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    code       text NOT NULL,
    -- WHERE this method delivers to, which decides what the checkout must ask
    -- for. On the METHOD rather than a branch on code = 'store_pickup', because
    -- a rule written in Go is a rule the next method forgets.
    destination_kind text NOT NULL DEFAULT 'address',
    -- What this method's carrier will physically accept, per parcel; NULL means
    -- no stated limit. Convenience-store pickup refuses a parcel over 45cm on
    -- its longest side, 105cm across three sides or 10kg, and Hi-Life over 5kg.
    max_parcel_longest_mm integer,
    max_parcel_sum_mm     integer,
    max_parcel_weight_g   integer,
    is_active  boolean NOT NULL DEFAULT true,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shipping_methods_code_format CHECK (code ~ '^[a-z0-9]+(_[a-z0-9]+)*$'),
    CONSTRAINT shipping_methods_max_longest_positive
        CHECK (max_parcel_longest_mm IS NULL OR max_parcel_longest_mm > 0),
    CONSTRAINT shipping_methods_max_sum_positive
        CHECK (max_parcel_sum_mm IS NULL OR max_parcel_sum_mm > 0),
    CONSTRAINT shipping_methods_max_weight_positive
        CHECK (max_parcel_weight_g IS NULL OR max_parcel_weight_g > 0),
    CONSTRAINT shipping_methods_destination_kind
        CHECK (destination_kind IN ('address', 'pickup_point'))
);

CREATE UNIQUE INDEX shipping_methods_code_key ON shipping_methods (code);

CREATE TABLE shipping_method_versions (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    method_id       uuid NOT NULL REFERENCES shipping_methods (id) ON DELETE RESTRICT,
    name            text NOT NULL,
    carrier         text,
    -- On the VERSION, like the fee: a past order names the version it was
    -- priced from, so translating a name cannot rewrite what it says it chose.
    name_en         text,
    carrier_en      text,
    fee_cents       bigint NOT NULL,
    -- The order value at or above which this method ships free; NULL means the
    -- fee always applies.
    free_over_cents bigint,
    effective_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shipping_method_versions_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT shipping_method_versions_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT shipping_method_versions_carrier_en_present
        CHECK (carrier_en IS NULL OR carrier_en ~ '[^[:space:]]'),
    CONSTRAINT shipping_method_versions_fee_non_negative CHECK (fee_cents >= 0),
    CONSTRAINT shipping_method_versions_free_over_non_negative
        CHECK (free_over_cents IS NULL OR free_over_cents >= 0)
);

CREATE UNIQUE INDEX shipping_method_versions_effective_key
    ON shipping_method_versions (method_id, effective_at);

CREATE TRIGGER shipping_method_versions_append_only
    BEFORE UPDATE OR DELETE ON shipping_method_versions
    FOR EACH ROW EXECUTE FUNCTION forbid_change('shipping_method_versions_append_only');

-- A zone is a set of postal-code prefixes. The prefix is the primary key of the
-- membership table, so an address is in exactly one zone — a postcode in two
-- would be a fee that depends on which row the planner returned first.
CREATE TABLE shipping_zones (
    id       uuid PRIMARY KEY DEFAULT uuidv7(),
    code     text NOT NULL,
    name     text NOT NULL,
    -- Read on /shipping and in the surcharge sentence shown before charging it.
    name_en  text,
    position integer NOT NULL DEFAULT 0,
    CONSTRAINT shipping_zones_code_format CHECK (code ~ '^[a-z0-9]+(_[a-z0-9]+)*$'),
    CONSTRAINT shipping_zones_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT shipping_zones_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX shipping_zones_code_key ON shipping_zones (code);

COMMENT ON TABLE shipping_zones IS
    'Delivery regions that cost differently. An address in no zone is the '
    'mainland default: no surcharge, every method serves it.';

CREATE TABLE shipping_zone_prefixes (
    -- The three-digit prefix of a Taiwanese postal code. Three and not five: the
    -- surcharge is decided by the district, and the last two digits are the
    -- delivery route within it.
    prefix  text PRIMARY KEY,
    zone_id uuid NOT NULL REFERENCES shipping_zones (id) ON DELETE RESTRICT,
    CONSTRAINT shipping_zone_prefixes_format CHECK (prefix ~ '^[0-9]{3}$')
);

CREATE INDEX shipping_zone_prefixes_zone_idx ON shipping_zone_prefixes (zone_id);

-- What one method charges extra for one zone. A version with NO row for a zone
-- serves it at no surcharge, so this table cannot change what an untouched
-- install does.
CREATE TABLE shipping_version_zones (
    version_id      uuid NOT NULL REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
    zone_id         uuid NOT NULL REFERENCES shipping_zones (id) ON DELETE RESTRICT,
    surcharge_cents bigint NOT NULL,
    PRIMARY KEY (version_id, zone_id),
    CONSTRAINT shipping_version_zones_surcharge_positive
        CHECK (surcharge_cents > 0)
);

-- The primary key leads on version_id, so a lookup by zone has nothing to use.
CREATE INDEX shipping_version_zones_zone_idx ON shipping_version_zones (zone_id);

COMMENT ON COLUMN shipping_version_zones.surcharge_cents IS
    'Added to the fee AFTER the free-over threshold is applied. 免運 covers the '
    'base rate the shop advertises, never the 離島 surcharge a carrier charges '
    'on top of it.';

-- ============================================================================
-- Orders
--
-- One column per lifecycle: payment state lives in `payments`, return state in
-- `return_requests`, and what remains here is fulfilment. The money columns are
-- the ones that are NOT derivable — a discount granted, a fee quoted, a tax
-- assessed.
-- ============================================================================

-- One row per business day, incremented atomically: a MAX()+1 in application
-- code hands the same number to two concurrent checkouts.
CREATE TABLE order_number_counters (
    business_date date PRIMARY KEY,
    last_no       integer NOT NULL,
    CONSTRAINT order_number_counters_in_range CHECK (last_no BETWEEN 1 AND 999999)
);

-- Six digits, not four: at four, the 10,000th order of a day cannot be placed.
CREATE FUNCTION next_order_number() RETURNS text
LANGUAGE plpgsql AS $$
DECLARE
    today date;
    seq   integer;
BEGIN
    today := (now() AT TIME ZONE 'Asia/Taipei')::date;

    INSERT INTO order_number_counters (business_date, last_no)
    VALUES (today, 1)
    ON CONFLICT (business_date) DO UPDATE
        SET last_no = order_number_counters.last_no + 1
    RETURNING last_no INTO seq;

    RETURN 'GO-' || to_char(today, 'YYMMDD') || '-' || to_char(seq, 'FM000000');
END;
$$;

CREATE TABLE orders (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    -- The number a customer quotes to support, so nobody reads a uuid aloud.
    order_number         text NOT NULL DEFAULT next_order_number(),
    -- NULL for a guest order, and NULL again once an account is erased.
    user_id              uuid REFERENCES users (id) ON DELETE SET NULL,
    fulfillment_status   text NOT NULL DEFAULT 'pending',
    currency             text NOT NULL DEFAULT 'TWD',
    discount_cents       bigint NOT NULL DEFAULT 0,
    shipping_cents       bigint NOT NULL DEFAULT 0,
    tax_cents            bigint NOT NULL DEFAULT 0,
    -- The version that was in force, plus its name as shown. The FK explains
    -- the price; the snapshot survives the version being superseded.
    shipping_version_id  uuid NOT NULL REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
    shipping_method_code text NOT NULL,
    shipping_method_name text NOT NULL,
    customer_note        text,
    staff_note           text,
    -- The language the order was placed in, so every message ABOUT it speaks it.
    -- On the order because the receipt comes from a webhook and the dispatch
    -- notice from a back-office click, with no visitor present to read.
    locale               text NOT NULL DEFAULT 'zh-Hant',
    placed_at            timestamptz NOT NULL DEFAULT now(),
    cancelled_at         timestamptz,
    completed_at         timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_number_format CHECK (order_number ~ '^GO-[0-9]{6}-[0-9]{6}$'),
    CONSTRAINT orders_currency_is_twd CHECK (currency = 'TWD'),
    CONSTRAINT orders_locale_known CHECK (locale IN ('zh-Hant', 'en')),
    CONSTRAINT orders_discount_non_negative CHECK (discount_cents >= 0),
    CONSTRAINT orders_shipping_non_negative CHECK (shipping_cents >= 0),
    CONSTRAINT orders_tax_non_negative CHECK (tax_cents >= 0),
    CONSTRAINT orders_shipping_code_present CHECK (shipping_method_code ~ '[^[:space:]]'),
    CONSTRAINT orders_shipping_name_present CHECK (shipping_method_name ~ '[^[:space:]]'),
    CONSTRAINT orders_fulfillment_status_known CHECK (fulfillment_status IN (
        'pending',    -- 待付款
        'picking',    -- 撿貨中
        'shipped',    -- 已出貨
        'delivered',  -- 已送達
        'completed',  -- 已完成
        'cancelled'   -- 已取消
    )),
    CONSTRAINT orders_not_both_ended
        CHECK (cancelled_at IS NULL OR completed_at IS NULL),
    CONSTRAINT orders_cancelled_after_placed
        CHECK (cancelled_at IS NULL OR cancelled_at >= placed_at),
    CONSTRAINT orders_completed_after_placed
        CHECK (completed_at IS NULL OR completed_at >= placed_at),
    -- One-way, deliberately. The status may move on — a cancelled order can
    -- afterwards be refunded — and a two-way form would make that inexpressible.
    CONSTRAINT orders_cancelled_has_time
        CHECK (fulfillment_status <> 'cancelled' OR cancelled_at IS NOT NULL),
    CONSTRAINT orders_completed_has_time
        CHECK (fulfillment_status <> 'completed' OR completed_at IS NOT NULL)
);

CREATE UNIQUE INDEX orders_number_key ON orders (order_number);
CREATE INDEX orders_user_placed_idx ON orders (user_id, placed_at DESC);
CREATE INDEX orders_shipping_version_idx ON orders (shipping_version_id);
CREATE INDEX orders_open_idx
    ON orders (placed_at)
    WHERE fulfillment_status IN ('pending', 'picking');

CREATE TRIGGER orders_set_updated_at
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The two tables written during checkout, before the order they will belong to
-- exists. All three are created above and hold no rows, so there is nothing for
-- these constraints to scan and no writer for their lock to block.
ALTER TABLE store_credit_entries
    ADD CONSTRAINT store_credit_entries_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE RESTRICT;

ALTER TABLE inventory_reservations
    ADD CONSTRAINT inventory_reservations_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE RESTRICT;

ALTER TABLE checkout_attempts
    ADD CONSTRAINT checkout_attempts_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE SET NULL;

-- Fulfilment moves forward: un-shipping is a lost fact, not a correction.
CREATE FUNCTION orders_check_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    legal boolean;
    lines integer;
    subtotal bigint;
    order_total bigint;
    owed           bigint;
BEGIN
    IF NEW.fulfillment_status = OLD.fulfillment_status THEN
        RETURN NEW;
    END IF;

    legal := CASE OLD.fulfillment_status
        WHEN 'pending'   THEN NEW.fulfillment_status IN ('picking', 'cancelled')
        WHEN 'picking'   THEN NEW.fulfillment_status IN ('shipped', 'cancelled')
        WHEN 'shipped'   THEN NEW.fulfillment_status IN ('delivered', 'completed')
        WHEN 'delivered' THEN NEW.fulfillment_status = 'completed'
        ELSE false  -- completed and cancelled are terminal
    END;

    IF NOT legal THEN
        RAISE EXCEPTION 'fulfilment cannot move from % to %',
            OLD.fulfillment_status, NEW.fulfillment_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_legal_transition';
    END IF;

    -- goen does not ship what it has not collected. Leaving pending requires the
    -- order to owe nothing — free or fully store-credited — or to carry a
    -- succeeded payment. A new funding source is added HERE.
    IF OLD.fulfillment_status = 'pending' AND NEW.fulfillment_status = 'picking' THEN
        SELECT count(*), coalesce(sum(unit_price_cents * quantity), 0)
        INTO lines, subtotal FROM order_lines WHERE order_id = NEW.id;
        -- order_amount_owed is the ONE definition: total less store credit, net
        -- of reversals.
        owed := order_amount_owed(NEW.id);
        IF owed <> 0
           AND NOT EXISTS (SELECT 1 FROM payments
                           WHERE order_id = NEW.id AND status = 'succeeded') THEN
            RAISE EXCEPTION 'order % cannot leave pending unfunded (owes %)',
                NEW.order_number, owed
                USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_funded_to_leave_pending';
        END IF;
    END IF;

    -- An order does not FINISH while it still owes a parcel. 'delivered' and
    -- 'completed' both end a delivery — 超商取貨 goes straight to the second,
    -- because nobody at the counter witnesses a handover — and the dropdown
    -- offers them as peers with no hint that anything is outstanding.
    --
    -- Without this, shipping one parcel of several and then completing the order
    -- stranded the rest: the holds stay `held`, release_reservation refuses them
    -- by name because a completed order is committed, ExpiredReservations
    -- excludes committed orders, and /admin/health counts expired holds with
    -- that same predicate — so the stock was off the shelf permanently and
    -- invisible on the one page built to show stock backlogs, while the customer
    -- read 已完成 for goods that never left.
    --
    -- It refuses rather than releasing. What has not gone out is either still
    -- going out — CanShip already allows the second parcel, and follows from what
    -- is outstanding rather than from the status — or it is an abandonment,
    -- which is a decision a person makes and not a side effect of a dropdown.
    -- 'completed' only, and the distinction is the point. DELIVERED is a fact
    -- about what went out — the parcels that shipped have arrived — and it is
    -- true whether or not more is still to come. COMPLETED says the order is
    -- finished, which an order still owing a parcel is not.
    --
    -- Guarding both closed the ONLY writer of order_shipments.delivered_at, so a
    -- partially shipped order could never record that anything had arrived and
    -- /admin/returns read 尚未送達 for goods the customer was holding — on the
    -- one screen built to inform a 消保法 §19 decision, in the shop's favour.
    -- That is the cost mistake #17 already recorded, reintroduced from the
    -- other side.
    IF NEW.fulfillment_status = 'completed'
       AND OLD.fulfillment_status <> NEW.fulfillment_status THEN
        SELECT count(*) INTO lines
        FROM order_lines ol
        WHERE ol.order_id = NEW.id
          AND ol.quantity > coalesce((
              SELECT sum(sl.quantity) FROM order_shipment_lines sl
              WHERE sl.order_line_id = ol.id), 0);
        IF lines > 0 THEN
            RAISE EXCEPTION 'order % still owes % line(s) a parcel and cannot be %',
                NEW.order_number, lines, NEW.fulfillment_status
                USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_finished_when_shipped';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_legal_transition
    BEFORE UPDATE OF fulfillment_status ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_check_transition();

-- Inserting straight into 'shipped' would skip every transition guard and every
-- side effect they carry.
CREATE FUNCTION orders_check_initial_status() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.fulfillment_status <> 'pending' THEN
        RAISE EXCEPTION 'a new order must start pending, not %', NEW.fulfillment_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_start_pending';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_start_pending
    BEFORE INSERT ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_check_initial_status();

-- What was bought, as it was at the moment of buying. variant_id may become
-- NULL and the line still reads correctly, because the name, sku and price are
-- its own.
CREATE TABLE order_lines (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id         uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    variant_id       uuid REFERENCES product_variants (id) ON DELETE SET NULL,
    sku              text NOT NULL,
    product_name     text NOT NULL,
    variant_label    text,
    unit_price_cents bigint NOT NULL,
    quantity         integer NOT NULL,
    position         integer NOT NULL DEFAULT 0,
    CONSTRAINT order_lines_sku_present CHECK (sku ~ '[^[:space:]]'),
    CONSTRAINT order_lines_product_name_present CHECK (product_name ~ '[^[:space:]]'),
    CONSTRAINT order_lines_unit_price_in_range
        CHECK (unit_price_cents >= 0 AND unit_price_cents <= 10000000000),
    CONSTRAINT order_lines_quantity_in_range CHECK (quantity > 0 AND quantity <= 999)
);

CREATE UNIQUE INDEX order_lines_position_key ON order_lines (order_id, position);
CREATE INDEX order_lines_variant_id_idx ON order_lines (variant_id);
-- Referenced by the composite foreign keys that keep shipment and return lines
-- on the same order as the line.
CREATE UNIQUE INDEX order_lines_order_key ON order_lines (order_id, id);

-- What lets a browser see a guest's order: the cookie carries a high-entropy
-- token and this holds its digest. Several rows per order on purpose — proving
-- the email on a second device must not cost the first browser its access.
CREATE TABLE order_access_grants (
    digest     bytea PRIMARY KEY,
    order_id   uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_access_grants_digest_sha256 CHECK (octet_length(digest) = 32)
);

CREATE INDEX order_access_grants_order_idx ON order_access_grants (order_id);

-- Keyed on created_at because that is what the SWEEP asks: a grant carries no
-- expiry column of its own, so retention is the only thing that bounds it.
CREATE INDEX order_access_grants_created_at_idx ON order_access_grants (created_at);

-- Where it went, and who to tell. Separate from `orders` because this is the
-- personal data: erasure empties it and leaves the financial record whole.
CREATE TABLE order_private_data (
    order_id       uuid PRIMARY KEY REFERENCES orders (id) ON DELETE RESTRICT,
    email          text,
    recipient_name text,
    phone          text,
    postal_code    text,
    city           text,
    district       text,
    street         text,
    -- The pickup store a parcel is collected from. Three columns because they
    -- answer different questions: which carrier's manifest the parcel joins,
    -- which store, and what a human reads on the label.
    pickup_brand      text,
    pickup_store_code text,
    pickup_store_name text,
    erased_at      timestamptz,
    -- Two exhaustive states, not "erased iff nothing set": the weaker form is
    -- satisfied by a LIVE row that happens to have only some fields cleared.
    CONSTRAINT order_private_data_all_or_erased CHECK (
        (erased_at IS NULL
            AND email IS NOT NULL AND recipient_name IS NOT NULL
            AND phone IS NOT NULL)
        OR
        (erased_at IS NOT NULL
            AND email IS NULL AND recipient_name IS NULL AND phone IS NULL
            AND postal_code IS NULL AND city IS NULL AND district IS NULL
            AND street IS NULL
            AND pickup_brand IS NULL AND pickup_store_code IS NULL
            AND pickup_store_name IS NULL)
    ),
    -- A live row carries EXACTLY ONE destination. Written as two all-or-nothing
    -- groups and an XOR rather than "street IS NOT NULL OR pickup_store_code IS
    -- NOT NULL", which a row carrying a city and no street would satisfy.
    CONSTRAINT order_private_data_one_destination CHECK (
        erased_at IS NOT NULL
        OR (
            (postal_code IS NOT NULL AND city IS NOT NULL
                AND district IS NOT NULL AND street IS NOT NULL)
            <> (pickup_brand IS NOT NULL AND pickup_store_code IS NOT NULL
                AND pickup_store_name IS NOT NULL)
        )
    ),
    CONSTRAINT order_private_data_address_complete CHECK (
        num_nonnulls(postal_code, city, district, street) IN (0, 4)
    ),
    CONSTRAINT order_private_data_pickup_complete CHECK (
        num_nonnulls(pickup_brand, pickup_store_code, pickup_store_name) IN (0, 3)
    ),
    -- An allowlist, because the brand decides which carrier's manifest the
    -- parcel joins and a typo there is a parcel that never leaves.
    CONSTRAINT order_private_data_pickup_brand_known CHECK (
        pickup_brand IS NULL
        OR pickup_brand IN ('seven_eleven', 'family_mart', 'hi_life', 'ok_mart')
    ),
    -- Digits or uppercase letters, at the length ECPay publishes for the field.
    -- Measured against their GetStoreList on 2026-08-06: Hi-Life uses four
    -- characters and 149 of its 1,350 stores lead with a letter.
    CONSTRAINT order_private_data_pickup_store_code_format CHECK (
        pickup_store_code IS NULL OR pickup_store_code ~ '^[0-9A-Z]{1,10}$'
    )
);

CREATE INDEX order_private_data_email_idx ON order_private_data (lower(email));

CREATE TABLE order_shipments (
    id                    uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id              uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    carrier               text NOT NULL,
    tracking_number       text NOT NULL,
    shipped_at            timestamptz NOT NULL DEFAULT now(),
    delivered_at          timestamptz,
    estimated_delivery_on date,
    CONSTRAINT order_shipments_carrier_present CHECK (carrier ~ '[^[:space:]]'),
    CONSTRAINT order_shipments_tracking_present CHECK (tracking_number ~ '[^[:space:]]'),
    CONSTRAINT order_shipments_delivered_after_shipped
        CHECK (delivered_at IS NULL OR delivered_at >= shipped_at)
);

CREATE INDEX order_shipments_order_id_idx ON order_shipments (order_id);
CREATE UNIQUE INDEX order_shipments_tracking_key ON order_shipments (carrier, tracking_number);
-- Referenced by the composite foreign key that ties a shipment line to a
-- shipment of the same order.
CREATE UNIQUE INDEX order_shipments_order_key ON order_shipments (order_id, id);

-- Which lines, and how many of each, went in this parcel. order_id is carried so
-- the composite keys enforce that the line and the shipment belong to the SAME
-- order; two plain foreign keys cannot.
CREATE TABLE order_shipment_lines (
    order_id      uuid NOT NULL,
    shipment_id   uuid NOT NULL,
    order_line_id uuid NOT NULL,
    quantity      integer NOT NULL,
    PRIMARY KEY (shipment_id, order_line_id),
    CONSTRAINT order_shipment_lines_quantity_positive CHECK (quantity > 0),
    CONSTRAINT order_shipment_lines_shipment_fk
        FOREIGN KEY (order_id, shipment_id) REFERENCES order_shipments (order_id, id)
        ON DELETE RESTRICT,
    CONSTRAINT order_shipment_lines_line_fk
        FOREIGN KEY (order_id, order_line_id) REFERENCES order_lines (order_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX order_shipment_lines_order_line_idx ON order_shipment_lines (order_id, order_line_id);
CREATE INDEX order_shipment_lines_order_shipment_idx ON order_shipment_lines (order_id, shipment_id);

-- You cannot ship more of a line than was bought, counting every shipment. The
-- line's order is locked first so two shipments cannot both pass.
CREATE FUNCTION shipment_lines_within_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    bought integer;
    already integer;
BEGIN
    SELECT ol.quantity INTO bought
    FROM order_lines ol JOIN orders o ON o.id = ol.order_id
    WHERE ol.id = NEW.order_line_id FOR UPDATE OF o;

    SELECT coalesce(sum(quantity), 0) INTO already
    FROM order_shipment_lines
    WHERE order_line_id = NEW.order_line_id AND shipment_id <> NEW.shipment_id;

    IF already + NEW.quantity > bought THEN
        RAISE EXCEPTION 'shipping % of a line that had % (already shipped %)',
            NEW.quantity, bought, already
            USING ERRCODE = 'check_violation', CONSTRAINT = 'shipment_within_purchase';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER shipment_within_purchase
    BEFORE INSERT OR UPDATE ON order_shipment_lines
    FOR EACH ROW EXECUTE FUNCTION shipment_lines_within_purchase();

-- The back office's order search. text_pattern_ops because the search is a
-- PREFIX: that is what an index can serve without pg_trgm on a table holding
-- PII, and it is what somebody reading their own name out loud gives you.
CREATE INDEX order_private_data_email_prefix_idx
    ON order_private_data (lower(email) text_pattern_ops);
CREATE INDEX order_private_data_recipient_prefix_idx
    ON order_private_data (recipient_name text_pattern_ops);

-- The timeline the customer sees. Append-only.
CREATE TABLE order_events (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id      uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    kind          text NOT NULL,
    note          text,
    actor_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_events_kind_known CHECK (kind IN (
        'placed', 'paid', 'picking', 'shipped', 'in_transit',
        'delivered', 'completed', 'cancelled', 'refunded'
    ))
);

CREATE INDEX order_events_order_occurred_idx ON order_events (order_id, occurred_at);
CREATE INDEX order_events_actor_idx ON order_events (actor_user_id);

CREATE TRIGGER order_events_append_only
    BEFORE UPDATE OR DELETE ON order_events
    FOR EACH ROW EXECUTE FUNCTION forbid_change('order_events_append_only');

-- An order is not a valid thing to have until it has something in it and
-- somewhere to go. DEFERRED, because the lines are necessarily inserted after
-- the order row they reference.
CREATE FUNCTION orders_check_complete() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lines integer;
    subtotal bigint;
    o orders%ROWTYPE;
BEGIN
    SELECT * INTO o FROM orders WHERE id = NEW.id;
    IF NOT FOUND THEN
        RETURN NULL;  -- deleted within the same transaction
    END IF;

    SELECT count(*), coalesce(sum(unit_price_cents * quantity), 0)
    INTO lines, subtotal
    FROM order_lines WHERE order_id = o.id;

    IF lines = 0 THEN
        RAISE EXCEPTION 'order % has no lines', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_have_lines';
    END IF;

    IF NOT EXISTS (SELECT 1 FROM order_private_data WHERE order_id = o.id) THEN
        RAISE EXCEPTION 'order % has no delivery details', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_have_delivery';
    END IF;

    IF subtotal - o.discount_cents + o.shipping_cents + o.tax_cents < 0 THEN
        RAISE EXCEPTION 'order % totals below zero', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_total_non_negative';
    END IF;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER orders_have_lines
    AFTER INSERT ON orders
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION orders_check_complete();

-- A coupon redeemed on an order. This is what makes the limits real: they are
-- counted from these rows, never from a column on the coupon that two concurrent
-- checkouts could each read and each increment.
CREATE TABLE coupon_redemptions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    coupon_id    uuid NOT NULL REFERENCES coupons (id) ON DELETE RESTRICT,
    order_id     uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    -- NULL for a guest checkout, so the per-customer limit binds on accounts
    -- only and the total cap is what bounds a guest.
    user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    -- What it actually took off, frozen here: the coupon may be edited
    -- afterwards, and what this order was given may not change.
    amount_cents bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT coupon_redemptions_amount_non_negative
        CHECK (amount_cents >= 0 AND amount_cents <= 10000000000)
);

-- One coupon per order. Stacking is a policy decision with real arithmetic
-- behind it, and goen does not make it.
CREATE UNIQUE INDEX coupon_redemptions_order_key ON coupon_redemptions (order_id);
CREATE INDEX coupon_redemptions_coupon_idx ON coupon_redemptions (coupon_id);
CREATE INDEX coupon_redemptions_user_idx ON coupon_redemptions (user_id);

CREATE TRIGGER coupon_redemptions_append_only
    BEFORE UPDATE OR DELETE ON coupon_redemptions
    FOR EACH ROW EXECUTE FUNCTION forbid_change('coupon_redemptions_append_only');

-- The redemption and the order's discount_cents are one fact, so the database
-- keeps them one.
CREATE FUNCTION coupon_redemption_matches_order() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    order_discount bigint;
BEGIN
    -- The order row is locked first: without it two writers each read a
    -- discount the other is about to change.
    SELECT discount_cents INTO order_discount
    FROM orders WHERE id = NEW.order_id FOR UPDATE;

    IF NOT FOUND THEN
        RETURN NEW;  -- the foreign key will speak
    END IF;
    IF order_discount <> NEW.amount_cents THEN
        RAISE EXCEPTION 'redemption records % but order was discounted %',
            NEW.amount_cents, order_discount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'coupon_redemption_matches_order';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER coupon_redemption_matches_order
    AFTER INSERT ON coupon_redemptions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION coupon_redemption_matches_order();

-- Once money has been captured, the itemisation that justified it is history:
-- correcting a price afterwards is a refund, not an UPDATE.

CREATE FUNCTION order_lines_freeze() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target uuid := coalesce(NEW.order_id, OLD.order_id);
BEGIN
    -- A line cannot move to another order, or a committed line could be carried
    -- into an open one to escape the freeze.
    IF TG_OP = 'UPDATE' AND NEW.order_id <> OLD.order_id THEN
        RAISE EXCEPTION 'an order line cannot change orders'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_committed';
    END IF;

    -- Lock the order, or T1 edits the line while T2 inserts the succeeded
    -- payment and both pass.
    PERFORM 1 FROM orders WHERE id = target FOR UPDATE;

    -- SETTLED, not committed: a cancelled order's lines are history too.
    IF order_is_settled(target) THEN
        RAISE EXCEPTION 'order % is settled; its lines cannot change', target
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_committed';
    END IF;
    RETURN coalesce(NEW, OLD);
END;
$$;

-- INSERT included: bound to UPDATE and DELETE alone, this lets a paid order
-- accept a brand-new line.
CREATE TRIGGER order_lines_frozen_once_committed
    BEFORE INSERT OR UPDATE OR DELETE ON order_lines
    FOR EACH ROW EXECUTE FUNCTION order_lines_freeze();

CREATE FUNCTION orders_freeze_money() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.discount_cents = OLD.discount_cents
       AND NEW.shipping_cents = OLD.shipping_cents
       AND NEW.tax_cents = OLD.tax_cents
       AND NEW.currency = OLD.currency
       AND NEW.order_number = OLD.order_number
       AND NEW.shipping_version_id = OLD.shipping_version_id
       AND NEW.shipping_method_code = OLD.shipping_method_code
       AND NEW.shipping_method_name = OLD.shipping_method_name
       -- A receipt was already sent in this language.
       AND NEW.locale = OLD.locale THEN
        RETURN NEW;
    END IF;

    PERFORM 1 FROM orders WHERE id = NEW.id FOR UPDATE;
    IF order_is_settled(NEW.id) THEN
        RAISE EXCEPTION 'order % is settled; its totals and shipping cannot change', NEW.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_money_frozen_once_committed';
    END IF;
    RETURN NEW;
END;
$$;

-- The snapshot must name the method the version actually belongs to; the foreign
-- key only proves the version exists. The NAME is a customer-facing label and may
-- differ, so only the code is constrained.
CREATE FUNCTION orders_check_shipping_snapshot() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    version_code text;
BEGIN
    SELECT sm.code INTO version_code
    FROM shipping_method_versions v
    JOIN shipping_methods sm ON sm.id = v.method_id
    WHERE v.id = NEW.shipping_version_id;

    IF NEW.shipping_method_code IS DISTINCT FROM version_code THEN
        RAISE EXCEPTION 'order shipping code % does not match version %''s method (%)',
            NEW.shipping_method_code, NEW.shipping_version_id, version_code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_shipping_snapshot_matches';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_shipping_snapshot_matches
    BEFORE INSERT OR UPDATE OF shipping_version_id, shipping_method_code ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_check_shipping_snapshot();

CREATE TRIGGER orders_money_frozen_once_committed
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_freeze_money();

-- cancelled_at and completed_at are the moments themselves. An UPDATE touching
-- only these trips neither the transition guard nor the money freeze, so only a
-- change to an already-set timestamp is refused.
CREATE FUNCTION orders_freeze_history() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.cancelled_at IS NOT NULL
       AND NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at THEN
        RAISE EXCEPTION 'order %: cancelled_at is history and cannot be rewritten', NEW.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_history_frozen';
    END IF;
    IF OLD.completed_at IS NOT NULL
       AND NEW.completed_at IS DISTINCT FROM OLD.completed_at THEN
        RAISE EXCEPTION 'order %: completed_at is history and cannot be rewritten', NEW.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_history_frozen';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_history_frozen
    BEFORE UPDATE OF cancelled_at, completed_at ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_freeze_history();

CREATE TABLE return_requests (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id           uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    -- Who asked, which is not the same question as who owns the order: support
    -- raises these too.
    requested_by_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    status             text NOT NULL DEFAULT 'requested',
    reason             text NOT NULL,
    resolution         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    decided_at         timestamptz,
    CONSTRAINT return_requests_status_known
        CHECK (status IN ('requested', 'approved', 'rejected', 'completed')),
    -- A BLANK reason is legal: Consumer Protection Act §19 I lets a customer
    -- rescind within seven days without giving one, and §19 V voids any agreement
    -- otherwise. The column stays NOT NULL; '' is "none given".
    CONSTRAINT return_requests_reason_bounded CHECK (length(reason) <= 500),
    CONSTRAINT return_requests_decided_has_time
        CHECK ((status = 'requested') = (decided_at IS NULL))
);

CREATE INDEX return_requests_order_id_idx ON return_requests (order_id);
CREATE UNIQUE INDEX return_requests_order_key ON return_requests (order_id, id);
CREATE INDEX return_requests_requester_idx ON return_requests (requested_by_user_id);
CREATE INDEX return_requests_open_idx ON return_requests (created_at) WHERE status = 'requested';

-- order_id is carried for the same reason as on shipment lines: the composite
-- foreign key makes a cross-order return impossible.
CREATE TABLE return_request_lines (
    order_id          uuid NOT NULL,
    return_request_id uuid NOT NULL REFERENCES return_requests (id) ON DELETE CASCADE,
    order_line_id     uuid NOT NULL,
    quantity          integer NOT NULL,
    -- What actually came back, and how much of it went on the shelf again. NULL
    -- until somebody has opened the parcel: "not inspected yet" and "inspected,
    -- nothing arrived" are different facts.
    received_quantity  integer,
    restocked_quantity integer,
    -- Why the two differ, in the staff member's own words. It renders on
    -- /admin/returns and nowhere a customer reads.
    inspection_note    text,
    PRIMARY KEY (return_request_id, order_line_id),
    CONSTRAINT return_request_lines_quantity_positive CHECK (quantity > 0),
    -- No more back than was asked for, and no more on the shelf than came back.
    CONSTRAINT return_request_lines_received_bounded
        CHECK (received_quantity IS NULL
               OR (received_quantity >= 0 AND received_quantity <= quantity)),
    CONSTRAINT return_request_lines_restocked_bounded
        CHECK (restocked_quantity IS NULL
               OR (restocked_quantity >= 0 AND restocked_quantity <= received_quantity)),
    CONSTRAINT return_request_lines_inspected_together
        CHECK ((received_quantity IS NULL) = (restocked_quantity IS NULL)),
    CONSTRAINT return_request_lines_note_bounded
        CHECK (inspection_note IS NULL OR length(inspection_note) <= 500),
    CONSTRAINT return_request_lines_request_fk
        FOREIGN KEY (order_id, return_request_id) REFERENCES return_requests (order_id, id)
        ON DELETE CASCADE,
    CONSTRAINT return_request_lines_line_fk
        FOREIGN KEY (order_id, order_line_id) REFERENCES order_lines (order_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX return_request_lines_order_line_idx ON return_request_lines (order_id, order_line_id);
CREATE INDEX return_request_lines_order_request_idx ON return_request_lines (order_id, return_request_id);

-- You cannot return what was never sent. The ceiling is the SHIPPED quantity:
-- bounded by what was ORDERED, a customer could open a return — and, since
-- refunds pay out on approval, be paid — for goods still in the warehouse.
CREATE FUNCTION return_lines_within_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    shipped integer;
    already integer;
BEGIN
    -- Two statements, because PostgreSQL refuses FOR UPDATE with GROUP BY. The
    -- lock comes first, or two requests each read a total the other will spend.
    PERFORM 1
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    WHERE ol.id = NEW.order_line_id
    FOR UPDATE OF o;

    -- No such line at all, as opposed to one that has shipped nothing. The
    -- foreign key already refuses this, so failing loudly beats a zero ceiling.
    IF NOT FOUND THEN
        RAISE EXCEPTION 'order line % does not exist', NEW.order_line_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_within_shipment';
    END IF;

    SELECT coalesce(sum(sl.quantity), 0) INTO shipped
    FROM order_shipment_lines sl
    WHERE sl.order_line_id = NEW.order_line_id;

    SELECT coalesce(sum(rl.quantity), 0) INTO already
    FROM return_request_lines rl
    JOIN return_requests r ON r.id = rl.return_request_id
    WHERE rl.order_line_id = NEW.order_line_id
      AND r.status <> 'rejected'
      AND rl.return_request_id <> NEW.return_request_id;

    IF already + NEW.quantity > shipped THEN
        RAISE EXCEPTION 'returning % of a line that shipped % (already claimed %)',
            NEW.quantity, shipped, already
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_within_shipment';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_within_shipment
    BEFORE INSERT OR UPDATE ON return_request_lines
    FOR EACH ROW EXECUTE FUNCTION return_lines_within_purchase();

-- requested → approved | rejected, approved → completed. 'rejected' is terminal,
-- so there is deliberately no branch leaving it: this file keeps no guard that
-- cannot fire.
CREATE FUNCTION return_requests_recount() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    legal boolean;
BEGIN
    IF OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;
    legal := CASE OLD.status
        WHEN 'requested' THEN NEW.status IN ('approved', 'rejected')
        WHEN 'approved'  THEN NEW.status = 'completed'
        ELSE false
    END;
    IF NOT legal THEN
        RAISE EXCEPTION 'return request cannot move from % to %', OLD.status, NEW.status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_requests_legal_transition';
    END IF;

    -- Completed means the parcel was opened and every line accounted for.
    -- Without it, 'completed' is a label somebody clicks over goods nobody
    -- counted.
    IF NEW.status = 'completed'
       AND EXISTS (SELECT 1 FROM return_request_lines
                   WHERE return_request_id = NEW.id AND received_quantity IS NULL) THEN
        RAISE EXCEPTION 'return request % has lines nobody has inspected', NEW.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_requests_completed_is_inspected';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_requests_legal_transition
    BEFORE UPDATE OF status ON return_requests
    FOR EACH ROW EXECUTE FUNCTION return_requests_recount();

-- Inserting straight into 'approved' would skip the transition machine above,
-- exactly as orders_start_pending guards orders.
CREATE FUNCTION return_requests_check_initial() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    IF NEW.status <> 'requested' THEN
        RAISE EXCEPTION 'a new return request must start requested, not %', NEW.status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_requests_start_requested';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_requests_start_requested
    BEFORE INSERT ON return_requests
    FOR EACH ROW EXECUTE FUNCTION return_requests_check_initial();

-- One row per UNIT, because buying two phones registers two warranties, which
-- one row per order line cannot say.
CREATE TABLE warranty_registrations (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    order_line_id uuid NOT NULL REFERENCES order_lines (id) ON DELETE RESTRICT,
    unit_no       smallint NOT NULL,
    user_id       uuid REFERENCES users (id) ON DELETE SET NULL,
    serial_number text,
    registered_at timestamptz NOT NULL DEFAULT now(),
    expires_on    date NOT NULL,
    CONSTRAINT warranty_registrations_unit_positive CHECK (unit_no > 0)
);

CREATE UNIQUE INDEX warranty_registrations_unit_key
    ON warranty_registrations (order_line_id, unit_no);
CREATE UNIQUE INDEX warranty_registrations_serial_key
    ON warranty_registrations (serial_number) WHERE serial_number IS NOT NULL;
CREATE INDEX warranty_registrations_user_id_idx ON warranty_registrations (user_id);

CREATE FUNCTION warranty_within_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    bought integer;
BEGIN
    SELECT quantity INTO bought FROM order_lines WHERE id = NEW.order_line_id;
    IF NEW.unit_no > bought THEN
        RAISE EXCEPTION 'unit % of a line that had %', NEW.unit_no, bought
            USING ERRCODE = 'check_violation', CONSTRAINT = 'warranty_unit_within_purchase';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER warranty_unit_within_purchase
    BEFORE INSERT OR UPDATE ON warranty_registrations
    FOR EACH ROW EXECUTE FUNCTION warranty_within_purchase();

-- ============================================================================
-- Invoices
--
-- TWO tables, because they are two things: what the customer asked for is a
-- preference and can be edited; what was issued to the tax authority is a
-- document and cannot. A return issues a credit note rather than altering it.
-- ============================================================================

CREATE TABLE invoice_preferences (
    order_id     uuid PRIMARY KEY REFERENCES orders (id) ON DELETE RESTRICT,
    invoice_type text NOT NULL,
    carrier_code text,
    tax_id       text,
    CONSTRAINT invoice_preferences_type_known
        CHECK (invoice_type IN ('mobile_carrier', 'member_carrier', 'company')),
    -- `type <> 'company' OR tax_id ~ regex` evaluates to NULL when tax_id is
    -- NULL, and a NULL CHECK passes — so the NOT NULL has to be spelled out
    -- before the regex.
    CONSTRAINT invoice_preferences_company_has_tax_id
        CHECK (invoice_type <> 'company'
               OR (tax_id IS NOT NULL AND tax_id ~ '^[0-9]{8}$')),
    CONSTRAINT invoice_preferences_mobile_has_carrier
        CHECK (invoice_type <> 'mobile_carrier'
               OR (carrier_code IS NOT NULL AND carrier_code ~ '[^[:space:]]'))
);

CREATE TABLE invoice_documents (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id     uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    kind         text NOT NULL,
    -- The invoice a credit note relieves; NULL for the invoice itself.
    original_id  uuid REFERENCES invoice_documents (id) ON DELETE RESTRICT,
    number       text NOT NULL,
    amount_cents bigint NOT NULL,
    status       text NOT NULL DEFAULT 'issued',
    provider_ref text,
    -- What a repeated FILING would be, so that it cannot become a second
    -- document. ECPay's B2C allowance endpoint carries no idempotency field —
    -- Issue has RelateNumber and Allowance has nothing — so the key is goen's,
    -- and it is derived from the order plus the refunded total the form was
    -- rendered with: a double-click sends the same key, while a genuine second
    -- 折讓 after a further refund carries a different one. NULL on an invoice,
    -- whose repeat is refused by RelateNumber at the provider.
    request_key  text,
    issued_at    timestamptz NOT NULL DEFAULT now(),
    voided_at    timestamptz,
    CONSTRAINT invoice_documents_kind_known CHECK (kind IN ('invoice', 'allowance')),
    CONSTRAINT invoice_documents_request_key_present
        CHECK (request_key IS NULL OR request_key ~ '[^[:space:]]'),
    -- 'pending' is a CLAIM, not a document: it says an attempt with this
    -- request key is in flight, which is what makes a second press refusable
    -- BEFORE the provider is asked. ECPay's allowance endpoint carries no
    -- idempotency field of its own, so filing first and recording after —
    -- correct for an invoice, whose repeat RelateNumber refuses — put two 折讓
    -- in front of the 財政部 for one refund.
    CONSTRAINT invoice_documents_status_known
        CHECK (status IN ('pending', 'issued', 'voided')),
    -- A pending claim has no number yet: allocating one is the provider's job,
    -- and inventing one is what this exists to stop. Both halves, because
    -- "pending means empty" alone would let an ISSUED document carry whitespace
    -- as its number, which the rule refused before the claim state existed.
    CONSTRAINT invoice_documents_number_present
        CHECK ((status = 'pending' AND number = '')
               OR (status <> 'pending' AND number ~ '[^[:space:]]')),
    CONSTRAINT invoice_documents_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT invoice_documents_voided_has_time
        CHECK ((status = 'voided') = (voided_at IS NOT NULL)),
    -- A claim carries the key it is claiming, or it claims nothing.
    CONSTRAINT invoice_documents_pending_is_claimed
        CHECK (status <> 'pending' OR request_key IS NOT NULL),
    CONSTRAINT invoice_documents_allowance_has_original
        CHECK ((kind = 'allowance') = (original_id IS NOT NULL))
    -- No self-reference CHECK: an invoice must have original_id NULL and a
    -- credit note's original must be a real invoice, so it can never fire.
);

-- Partial, because a PENDING claim has no number and carries '' to say so. A
-- whole-table unique on `number` puts every claim in one another's way: two
-- allowances on two different orders, with two different request keys, collide
-- on the empty string, so at most ONE claim could be in flight in the entire
-- database. One provider failure then refused every 折讓 the shop would ever
-- file — and the refusal named the OTHER order's key, so nobody could see why.
CREATE UNIQUE INDEX invoice_documents_number_key
    ON invoice_documents (number)
    WHERE status <> 'pending';
CREATE INDEX invoice_documents_order_idx ON invoice_documents (order_id);
CREATE INDEX invoice_documents_original_idx ON invoice_documents (original_id);
-- At most one live invoice per order: a second while the first stands files two
-- tax documents for one sale. Credit notes are unbounded, and a voided invoice
-- frees the slot for a corrected reissue.
CREATE UNIQUE INDEX invoice_documents_one_active_invoice_per_order
    ON invoice_documents (order_id)
    WHERE kind = 'invoice' AND status <> 'voided';

-- One document per request. The invoice half is covered by RelateNumber at the
-- provider (a repeat is 5070357); the allowance half had nothing at all, so an
-- operator who pressed the button twice — or whose first press timed out after
-- ECPay had filed — put two 折讓 in front of the 財政部 for one refund. A VOIDED
-- allowance leaves its key free, because reissuing after a correction is the
-- legitimate repeat.
CREATE UNIQUE INDEX invoice_documents_request_key
    ON invoice_documents (request_key)
    WHERE request_key IS NOT NULL AND status <> 'voided';

-- Issued documents are filed, not edited.
CREATE FUNCTION invoice_documents_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- A PENDING claim is a reservation, not a document: nothing is at the
        -- 加值中心 under it and it has no number. Releasing one is the only way
        -- out for a claim whose provider call was REFUSED — an answer proving
        -- nothing was filed — and without it a rejected 折讓 holds its key for
        -- ever, with no door: it cannot be voided (a void needs a number), the
        -- key cannot be cleared, and the row cannot be deleted.
        IF OLD.status = 'pending' THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'invoice documents are filed, not deleted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
    END IF;

    -- A PENDING claim is not yet a document: settling it is what writes the
    -- number the provider allocated, and that is the one rewrite this rule must
    -- allow. Everything it guards stays guarded the moment the row is issued.
    IF OLD.status = 'pending' AND NEW.status = 'issued' THEN
        IF NEW.id <> OLD.id OR NEW.order_id <> OLD.order_id OR NEW.kind <> OLD.kind
           OR NEW.amount_cents <> OLD.amount_cents
           OR NEW.original_id IS DISTINCT FROM OLD.original_id
           OR NEW.request_key IS DISTINCT FROM OLD.request_key THEN
            RAISE EXCEPTION 'settling a claim may only write its number, not restate it'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
        END IF;
        RETURN NEW;
    END IF;

    -- request_key belongs in this list for the reason the others do: clearing it
    -- on an issued allowance takes the row out of invoice_documents_request_key
    -- and lets the SAME refund be filed a second time at the 財政部, which is
    -- exactly what that index was added to stop.
    IF NEW.id <> OLD.id OR NEW.order_id <> OLD.order_id OR NEW.kind <> OLD.kind
       OR NEW.number <> OLD.number OR NEW.amount_cents <> OLD.amount_cents
       OR NEW.original_id IS DISTINCT FROM OLD.original_id
       OR NEW.request_key IS DISTINCT FROM OLD.request_key
       OR NEW.issued_at <> OLD.issued_at THEN
        RAISE EXCEPTION 'an issued document may only be voided, not rewritten'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
    END IF;

    -- Voiding is one-way: a voided invoice is filed tax history, and reviving
    -- it to 'issued' would let that history be rewritten.
    IF OLD.status = 'voided' AND NEW.status <> 'voided' THEN
        RAISE EXCEPTION 'a voided document cannot be re-issued'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invoice_documents_only_void
    BEFORE UPDATE OR DELETE ON invoice_documents
    FOR EACH ROW EXECUTE FUNCTION invoice_documents_guard();

-- A credit note must relieve a real, unvoided invoice of the SAME order, and the
-- notes against it may not total more than it was for. The original is locked so
-- two cannot both pass.
CREATE FUNCTION invoice_allowance_valid() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    orig invoice_documents%ROWTYPE;
    already bigint;
BEGIN
    IF NEW.kind <> 'allowance' THEN
        RETURN NEW;
    END IF;

    SELECT * INTO orig FROM invoice_documents WHERE id = NEW.original_id FOR UPDATE;
    IF NOT FOUND OR orig.kind <> 'invoice' OR orig.order_id <> NEW.order_id
       OR orig.status = 'voided' THEN
        RAISE EXCEPTION 'an allowance must relieve an issued invoice of the same order'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_allowance_valid';
    END IF;

    SELECT coalesce(sum(amount_cents), 0) INTO already
    FROM invoice_documents
    WHERE original_id = NEW.original_id AND status <> 'voided' AND id <> NEW.id;

    IF already + NEW.amount_cents > orig.amount_cents THEN
        RAISE EXCEPTION 'allowances would total % against an invoice of %',
            already + NEW.amount_cents, orig.amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_allowance_valid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invoice_allowance_valid
    BEFORE INSERT OR UPDATE ON invoice_documents
    FOR EACH ROW EXECUTE FUNCTION invoice_allowance_valid();

-- Line detail behind each document: the tax authority's credit-note message
-- needs the original line, quantity, unit price and tax type, and none of that
-- can be reconstructed from a header total. Append-only, like the document.
CREATE TABLE invoice_document_lines (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    document_id   uuid NOT NULL REFERENCES invoice_documents (id) ON DELETE RESTRICT,
    description   text NOT NULL,
    quantity      integer NOT NULL,
    unit_price_cents bigint NOT NULL,
    amount_cents  bigint NOT NULL,
    tax_type      text NOT NULL,
    position      integer NOT NULL DEFAULT 0,
    CONSTRAINT invoice_document_lines_description_present CHECK (description ~ '[^[:space:]]'),
    CONSTRAINT invoice_document_lines_quantity_positive CHECK (quantity > 0),
    CONSTRAINT invoice_document_lines_amount_non_negative CHECK (amount_cents >= 0),
    -- A negative unit price would let an issued invoice be padded with a credit
    -- no note recorded. Header-equals-sum reconciliation belongs with a
    -- draft-to-issued flow and is not asked here.
    CONSTRAINT invoice_document_lines_unit_price_in_range
        CHECK (unit_price_cents >= 0 AND unit_price_cents <= 10000000000),
    CONSTRAINT invoice_document_lines_amount_in_range CHECK (amount_cents <= 10000000000),
    CONSTRAINT invoice_document_lines_tax_type_known
        CHECK (tax_type IN ('taxable', 'zero_rated', 'exempt'))
);

CREATE UNIQUE INDEX invoice_document_lines_position_key
    ON invoice_document_lines (document_id, position);

CREATE TRIGGER invoice_document_lines_append_only
    BEFORE UPDATE OR DELETE ON invoice_document_lines
    FOR EACH ROW EXECUTE FUNCTION forbid_change('invoice_document_lines_append_only');

CREATE TABLE payments (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id               uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    provider               text NOT NULL DEFAULT 'stripe',
    provider_ref           text NOT NULL,
    status                 text NOT NULL,
    -- What the intent was created for, and what was actually taken: only a
    -- succeeded payment has captured anything.
    intended_amount_cents  bigint NOT NULL,
    captured_amount_cents  bigint,
    currency               text NOT NULL DEFAULT 'TWD',
    card_brand             text,
    card_last4             text,
    paid_at                timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_provider_known CHECK (provider IN ('stripe')),
    -- There is deliberately no 'failed': Stripe returns a declined intent to
    -- requires_payment_method, and a terminal 'failed' would make a recoverable
    -- decline unrecoverable.
    CONSTRAINT payments_status_known
        CHECK (status IN ('requires_payment', 'requires_action', 'processing',
                          'succeeded', 'cancelled')),
    CONSTRAINT payments_intended_positive CHECK (intended_amount_cents > 0),
    -- Bounded, or a capture can approach 2^63 and overflow the running sums the
    -- refund and store-credit guards compute.
    CONSTRAINT payments_intended_in_range CHECK (intended_amount_cents <= 10000000000),
    CONSTRAINT payments_captured_non_negative
        CHECK (captured_amount_cents IS NULL OR captured_amount_cents >= 0),
    CONSTRAINT payments_captured_in_range
        CHECK (captured_amount_cents IS NULL OR captured_amount_cents <= 10000000000),
    CONSTRAINT payments_currency_is_twd CHECK (currency = 'TWD'),
    CONSTRAINT payments_last4_format CHECK (card_last4 IS NULL OR card_last4 ~ '^[0-9]{4}$'),
    -- Two exhaustive branches, not a comparison of two booleans: the latter
    -- lets a failed payment carry a captured amount, and the refund guard reads
    -- captured without reading status.
    CONSTRAINT payments_succeeded_is_captured CHECK (
        (status = 'succeeded'
            AND paid_at IS NOT NULL
            AND captured_amount_cents IS NOT NULL
            AND captured_amount_cents > 0)
        OR
        (status <> 'succeeded'
            AND paid_at IS NULL
            AND captured_amount_cents IS NULL)
    )
);

CREATE UNIQUE INDEX payments_provider_ref_key ON payments (provider, provider_ref);
CREATE INDEX payments_order_id_idx ON payments (order_id);
-- At most one capture per order: a second succeeded payment means the customer
-- was charged twice.
CREATE UNIQUE INDEX payments_one_capture_per_order
    ON payments (order_id) WHERE status = 'succeeded';

CREATE TRIGGER payments_set_updated_at
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Stripe does not promise webhook ordering, so a late-arriving `created` can
-- follow `succeeded`. Terminal states do not un-happen.
CREATE FUNCTION payments_check_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;
    -- 'failed' is absent because payments_status_known has no such value;
    -- refunds keep it because their own CHECK includes it.
    IF OLD.status IN ('succeeded', 'cancelled') THEN
        RAISE EXCEPTION 'payment % is settled as %, cannot become %',
            OLD.provider_ref, OLD.status, NEW.status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_no_regression';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payments_no_regression
    BEFORE UPDATE OF status ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_check_transition();

-- A payment may only succeed against an order that is still complete. A draft
-- order can lose its lines between orders_have_lines and payment, so this closes
-- that window at the moment money is taken, holding the order locked.
CREATE FUNCTION payments_require_complete_order() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    o orders%ROWTYPE;
    lines integer;
    subtotal bigint;
    order_total bigint;
    owed           bigint;
BEGIN
    IF NEW.status <> 'succeeded' THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.status = 'succeeded' THEN
        RETURN NEW;
    END IF;

    SELECT * INTO o FROM orders WHERE id = NEW.order_id FOR UPDATE;

    SELECT count(*), coalesce(sum(unit_price_cents * quantity), 0)
    INTO lines, subtotal FROM order_lines WHERE order_id = o.id;

    -- A LIVE, filled-in row: order_private_data_all_or_erased permits an
    -- all-NULL erased shape, and a live row could carry blanks. The DESTINATION
    -- is an either/or — asking for a street made every pickup order unpayable.
    IF lines = 0
       OR NOT EXISTS (
           SELECT 1 FROM order_private_data
           WHERE order_id = o.id AND erased_at IS NULL
             AND email ~ '[^[:space:]]' AND recipient_name ~ '[^[:space:]]'
             AND phone ~ '[^[:space:]]'
             AND ((postal_code ~ '[^[:space:]]' AND city ~ '[^[:space:]]'
                   AND district ~ '[^[:space:]]' AND street ~ '[^[:space:]]')
                  OR (pickup_brand ~ '[^[:space:]]' AND pickup_store_code ~ '[^[:space:]]'
                      AND pickup_store_name ~ '[^[:space:]]')))
       OR subtotal - o.discount_cents + o.shipping_cents + o.tax_cents < 0 THEN
        RAISE EXCEPTION 'order % is not complete enough to be paid', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_require_complete_order';
    END IF;

    -- A CANCELLED order cannot be paid, and this is the only line that says so.
    -- Otherwise the two-tab sequence goes through: start a payment, cancel in the
    -- other tab, pay at Stripe, and the stock is already back on the shelf.
    IF o.fulfillment_status = 'cancelled' THEN
        RAISE EXCEPTION 'order % was cancelled and cannot be paid', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_refuse_cancelled_order';
    END IF;

    -- The capture must equal what the order is OWED — its total, less the store
    -- credit spent on it. order_amount_owed is the ONE place that arithmetic
    -- lives, and a new funding source is added there.
    owed := order_amount_owed(o.id);
    IF NEW.captured_amount_cents <> owed THEN
        RAISE EXCEPTION 'order % is owed % but the capture is %',
            o.order_number, owed, NEW.captured_amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_capture_matches_order';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payments_require_complete_order
    BEFORE INSERT OR UPDATE OF status ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_require_complete_order();

-- Once money has been captured the row explaining it is history: lowering
-- captured_amount_cents would silently raise the refundable balance, and moving
-- the payment would detach it from what it paid for.
CREATE FUNCTION payments_freeze_settled() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'succeeded' THEN
        RETURN NEW;
    END IF;
    IF NEW.order_id <> OLD.order_id
       OR NEW.provider <> OLD.provider
       OR NEW.provider_ref <> OLD.provider_ref
       OR NEW.captured_amount_cents IS DISTINCT FROM OLD.captured_amount_cents
       OR NEW.intended_amount_cents <> OLD.intended_amount_cents
       OR NEW.currency <> OLD.currency
       OR NEW.paid_at IS DISTINCT FROM OLD.paid_at THEN
        RAISE EXCEPTION 'payment % is settled; its amounts and identity are history',
            OLD.provider_ref
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_settled_is_history';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payments_settled_is_history
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_freeze_settled();

CREATE TABLE refunds (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    payment_id   uuid NOT NULL REFERENCES payments (id) ON DELETE RESTRICT,
    return_request_id uuid REFERENCES return_requests (id) ON DELETE RESTRICT,
    -- The caller's own key, committed BEFORE the provider is called, so a crash
    -- between the call and the response leaves a row reconciliation can resolve.
    request_key  text NOT NULL,
    -- Filled in once Stripe answers; NULL means asked for and unconfirmed.
    provider_ref text,
    status       text NOT NULL DEFAULT 'pending',
    amount_cents bigint NOT NULL,
    reason       text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    succeeded_at timestamptz,
    failed_at    timestamptz,
    CONSTRAINT refunds_status_known
        CHECK (status IN ('pending', 'requires_action', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT refunds_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT refunds_request_key_present CHECK (request_key ~ '[^[:space:]]'),
    CONSTRAINT refunds_succeeded_has_time
        CHECK ((status = 'succeeded') = (succeeded_at IS NOT NULL)),
    CONSTRAINT refunds_failed_has_time
        CHECK ((status = 'failed') = (failed_at IS NOT NULL)),
    CONSTRAINT refunds_amount_in_range CHECK (amount_cents <= 10000000000)
);

CREATE UNIQUE INDEX refunds_request_key_key ON refunds (request_key);
CREATE UNIQUE INDEX refunds_provider_ref_key ON refunds (provider_ref)
    WHERE provider_ref IS NOT NULL;
CREATE INDEX refunds_payment_id_idx ON refunds (payment_id);
CREATE INDEX refunds_return_request_idx ON refunds (return_request_id);

-- Refunds cannot exceed what was captured. The payment row is locked first, so
-- two concurrent refunds of 60 against a capture of 100 cannot both see zero
-- already refunded.
CREATE FUNCTION refunds_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    captured bigint;
    pay_status text;
    pay_order uuid;
    already  bigint;
BEGIN
    -- The identity of a refund is fixed once written: re-pointing it would let
    -- one capture's allowance be spent against a second.
    IF TG_OP = 'UPDATE' AND (NEW.payment_id <> OLD.payment_id
                             OR NEW.request_key <> OLD.request_key) THEN
        RAISE EXCEPTION 'a refund cannot be moved to another payment'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;

    SELECT captured_amount_cents, status, order_id INTO captured, pay_status, pay_order
    FROM payments WHERE id = NEW.payment_id FOR UPDATE;

    -- Both halves matter: reading captured alone accepts a failed payment that
    -- carries an amount.
    IF pay_status <> 'succeeded' OR captured IS NULL THEN
        RAISE EXCEPTION 'refunding a payment that is % and captured %',
            pay_status, coalesce(captured::text, 'nothing')
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;

    -- payment_id and return_request_id are otherwise unrelated foreign keys, so
    -- order A's capture could be refunded against order B's return.
    IF NEW.return_request_id IS NOT NULL
       AND NOT EXISTS (SELECT 1 FROM return_requests
                       WHERE id = NEW.return_request_id AND order_id = pay_order) THEN
        RAISE EXCEPTION 'refund''s return request belongs to a different order than its payment'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_same_order';
    END IF;

    SELECT coalesce(sum(amount_cents), 0) INTO already
    FROM refunds
    WHERE payment_id = NEW.payment_id
      AND status <> 'failed'
      AND status <> 'cancelled'
      AND id <> NEW.id;

    IF already + NEW.amount_cents > captured THEN
        RAISE EXCEPTION 'refunds would total % against a capture of %',
            already + NEW.amount_cents, captured
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;
    RETURN NEW;
END;
$$;

-- Every UPDATE, not only the columns the sum reads: moving a refund to another
-- payment changes neither amount nor status.
CREATE TRIGGER refunds_within_capture
    BEFORE INSERT OR UPDATE ON refunds
    FOR EACH ROW EXECUTE FUNCTION refunds_guard();

-- Demoting a succeeded refund to failed would drop it out of the sum above and
-- free the allowance to be spent again.
CREATE FUNCTION refunds_check_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = NEW.status THEN
        RETURN NEW;
    END IF;
    IF OLD.status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'refund % is settled as %, cannot become %',
            OLD.request_key, OLD.status, NEW.status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_no_regression';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER refunds_no_regression
    BEFORE UPDATE OF status ON refunds
    FOR EACH ROW EXECUTE FUNCTION refunds_check_transition();

-- A settled refund's amount is history. The no-regression guard stops the STATUS
-- being demoted and leaves the amount editable — a succeeded 60 rewritten to 100
-- is still within the capture, so refunds_guard passes.
CREATE FUNCTION refunds_freeze_settled() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    IF OLD.status NOT IN ('succeeded', 'failed', 'cancelled') THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        IF OLD.status = 'succeeded' THEN
            RAISE EXCEPTION 'a succeeded refund is history and cannot be deleted'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_settled_is_history';
        END IF;
        RETURN OLD;
    END IF;
    IF NEW.amount_cents <> OLD.amount_cents
       OR NEW.payment_id <> OLD.payment_id
       OR NEW.request_key <> OLD.request_key
       OR NEW.return_request_id IS DISTINCT FROM OLD.return_request_id
       OR NEW.provider_ref IS DISTINCT FROM OLD.provider_ref
       OR NEW.succeeded_at IS DISTINCT FROM OLD.succeeded_at
       OR NEW.failed_at IS DISTINCT FROM OLD.failed_at THEN
        RAISE EXCEPTION 'refund % is settled; its amount and identity are history',
            OLD.request_key
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_settled_is_history';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER refunds_settled_is_history
    BEFORE UPDATE OR DELETE ON refunds
    FOR EACH ROW EXECUTE FUNCTION refunds_freeze_settled();

-- Stripe delivers at least once and in no guaranteed order. Recording the event
-- before acting on it is what makes that safe.
CREATE TABLE payment_webhook_events (
    provider            text NOT NULL,
    event_id            text NOT NULL,
    type                text NOT NULL,
    object_ref          text,
    payload             jsonb NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    processed_at        timestamptz,
    -- Why this event could not be acted on, for the cases where "processed"
    -- means "seen and refused" rather than "done". Money arriving for an order
    -- goen had already cancelled is the one that matters: the capture is refused
    -- by payments_refuse_cancelled_order, the event is still marked processed so
    -- Stripe stops retrying — which is correct, retrying changes nothing — and
    -- the only trace used to be a log line. The money is at Stripe and the goods
    -- are back on the shelf, so somebody has to refund it by hand; without a row
    -- there is nothing for /admin/health to name and nothing to reconcile
    -- against.
    unreconciled        text,
    -- When somebody dealt with it. The alarm is monotone without this: once an
    -- event lands unreconciled, /admin/health is unhealthy forever, which is
    -- alarm fatigue on the page built to make failure visible — the objection
    -- expired_holds already carries. The row KEEPS its reason, because what
    -- happened is worth reading after it is handled; contact_messages.handled_at
    -- is the same shape.
    reconciled_at       timestamptz,
    PRIMARY KEY (provider, event_id),
    CONSTRAINT payment_webhook_events_type_present CHECK (type ~ '[^[:space:]]'),
    CONSTRAINT payment_webhook_events_unreconciled_present
        CHECK (unreconciled IS NULL OR unreconciled ~ '[^[:space:]]'),
    -- Nothing to reconcile means nothing to mark reconciled.
    CONSTRAINT payment_webhook_events_reconciled_was_flagged
        CHECK (reconciled_at IS NULL OR unreconciled IS NOT NULL)
);

COMMENT ON COLUMN payment_webhook_events.unreconciled IS
    'Set when an event was accepted but its effect could not be applied, and a person must act.';

CREATE INDEX payment_webhook_events_unreconciled_idx
    ON payment_webhook_events (received_at)
    WHERE unreconciled IS NOT NULL AND reconciled_at IS NULL;

CREATE INDEX payment_webhook_events_unprocessed_idx
    ON payment_webhook_events (received_at)
    WHERE processed_at IS NULL;
CREATE INDEX payment_webhook_events_object_idx ON payment_webhook_events (object_ref);

-- ============================================================================
-- Outbox
--
-- Written in the same transaction as the change that causes it: sending first
-- risks a mail about an order that never committed, committing first risks
-- silence.
-- ============================================================================

CREATE TABLE outbox_messages (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    topic        text NOT NULL,
    dedupe_key   text NOT NULL,
    payload      jsonb NOT NULL,
    -- Lower is sooner: transactional mail is 0 and a bulk send is 100. Without
    -- it one newsletter to ten thousand subscribers sits in front of every
    -- receipt and password reset written after it.
    priority     smallint NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    attempts     integer NOT NULL DEFAULT 0,
    last_error   text,
    CONSTRAINT outbox_messages_topic_present CHECK (topic ~ '[^[:space:]]'),
    CONSTRAINT outbox_messages_attempts_non_negative CHECK (attempts >= 0),
    CONSTRAINT outbox_messages_priority_non_negative CHECK (priority >= 0)
);

CREATE UNIQUE INDEX outbox_messages_dedupe_key ON outbox_messages (topic, dedupe_key);
-- The claim's exact ORDER BY, so a queue with a bulk send waiting in it still
-- finds the next urgent message with an index scan.
CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (priority, available_at)
    WHERE delivered_at IS NULL;

-- The retention sweep's range; without it the daily delete is a sequential scan
-- over every message goen has ever sent.
CREATE INDEX outbox_messages_delivered_at_idx
    ON outbox_messages (delivered_at)
    WHERE delivered_at IS NOT NULL;

CREATE TABLE audit_events (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    actor_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    action        text NOT NULL,
    entity_table  text NOT NULL,
    entity_id     uuid,
    before        jsonb,
    after         jsonb,
    request_id    text,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_events_action_present CHECK (action ~ '[^[:space:]]'),
    CONSTRAINT audit_events_entity_present CHECK (entity_table ~ '[^[:space:]]')
);

CREATE INDEX audit_events_entity_idx ON audit_events (entity_table, entity_id, occurred_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, occurred_at DESC);

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION forbid_change('audit_events_append_only');

CREATE TABLE hero_slides (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    eyebrow             text,
    headline            text NOT NULL,
    body                text,
    primary_cta_label   text NOT NULL,
    primary_cta_href    text NOT NULL,
    secondary_cta_label text,
    secondary_cta_href  text,
    image_key           text,
    image_alt           text,
    -- The English hero, each field separately optional. The HREFs are not
    -- translated: a link goes to one page.
    eyebrow_en             text,
    headline_en            text,
    body_en                text,
    primary_cta_label_en   text,
    secondary_cta_label_en text,
    image_alt_en           text,
    position            integer NOT NULL DEFAULT 0,
    starts_at           timestamptz,
    ends_at             timestamptz,
    is_active           boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT hero_slides_headline_present CHECK (headline ~ '[^[:space:]]'),
    CONSTRAINT hero_slides_primary_cta_present
        CHECK (primary_cta_label ~ '[^[:space:]]' AND primary_cta_href ~ '[^[:space:]]'),
    CONSTRAINT hero_slides_image_has_alt
        CHECK (image_key IS NULL
               OR (image_alt IS NOT NULL AND image_alt ~ '[^[:space:]]')),
    CONSTRAINT hero_slides_secondary_cta_complete
        CHECK ((secondary_cta_label IS NULL) = (secondary_cta_href IS NULL)),
    CONSTRAINT hero_slides_window_ordered
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);

CREATE UNIQUE INDEX hero_slides_position_key ON hero_slides (position);

CREATE TRIGGER hero_slides_set_updated_at
    BEFORE UPDATE ON hero_slides
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A tier is what a customer has SPENT, derived and never stored: a stored tier
-- drifts from the orders behind it the moment one is refunded. The window is
-- ROLLING, because lifetime tiers only ever go up.
CREATE TABLE membership_tiers (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    code                 text NOT NULL,
    name                 text NOT NULL,
    -- The account page reads this INSIDE a sentence, so a missing translation
    -- reads as a broken page rather than as untranslated content.
    name_en              text,
    -- The spend at or above which a customer is in this tier, over
    -- MembershipWindow days of committed orders.
    min_spend_cents      bigint NOT NULL,
    -- What a point is worth here, in basis points of the base rate: 10000 is one
    -- point per NT$100. A MULTIPLIER rather than a discount at checkout, which
    -- would stack with coupons and the free-delivery threshold.
    points_multiplier_bp integer NOT NULL DEFAULT 10000,
    position             integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT membership_tiers_code_format CHECK (code ~ '^[a-z0-9]+(_[a-z0-9]+)*$'),
    CONSTRAINT membership_tiers_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT membership_tiers_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT membership_tiers_min_spend_non_negative CHECK (min_spend_cents >= 0),
    -- A tier earning FEWER points than no tier would punish spending more.
    CONSTRAINT membership_tiers_multiplier_at_least_base
        CHECK (points_multiplier_bp >= 10000)
);

CREATE UNIQUE INDEX membership_tiers_code_key ON membership_tiers (code);

-- One tier per threshold, so "which tier is NT$50,000 in" has one answer.
CREATE UNIQUE INDEX membership_tiers_min_spend_key ON membership_tiers (min_spend_cents);

COMMENT ON TABLE membership_tiers IS
    'Spend bands and what they earn. A customer''s tier is derived from '
    'committed orders in a rolling window, never stored on the user.';

CREATE TABLE promo_banners (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    message       text NOT NULL,
    -- The narrow-screen wording is different copy, not a truncation.
    message_short text,
    code          text,
    cta_label     text,
    cta_href      text,
    -- The English strip. The HREF has no twin, and neither has the CODE: a
    -- coupon code is typed into a box and matched exactly.
    message_en       text,
    message_short_en text,
    cta_label_en     text,
    starts_at     timestamptz,
    ends_at       timestamptz,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT promo_banners_message_present CHECK (message ~ '[^[:space:]]'),
    CONSTRAINT promo_banners_message_en_present
        CHECK (message_en IS NULL OR message_en ~ '[^[:space:]]'),
    CONSTRAINT promo_banners_message_short_en_present
        CHECK (message_short_en IS NULL OR message_short_en ~ '[^[:space:]]'),
    CONSTRAINT promo_banners_cta_label_en_present
        CHECK (cta_label_en IS NULL OR cta_label_en ~ '[^[:space:]]'),
    CONSTRAINT promo_banners_cta_complete CHECK ((cta_label IS NULL) = (cta_href IS NULL)),
    CONSTRAINT promo_banners_window_ordered
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);

CREATE INDEX promo_banners_active_idx ON promo_banners (starts_at) WHERE is_active;

CREATE TRIGGER promo_banners_set_updated_at
    BEFORE UPDATE ON promo_banners
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The campaign is a collection with a deadline; the reduction itself is the
-- variant's price against its compare-at price, and the trigger below is what
-- stops the two disagreeing.
CREATE TABLE sale_campaigns (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    slug       text NOT NULL,
    title      text NOT NULL,
    -- /s/{slug} is a page whose whole heading is this title.
    title_en   text,
    starts_at  timestamptz NOT NULL DEFAULT now(),
    ends_at    timestamptz NOT NULL,
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sale_campaigns_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT sale_campaigns_title_present CHECK (title ~ '[^[:space:]]'),
    CONSTRAINT sale_campaigns_title_en_present
        CHECK (title_en IS NULL OR title_en ~ '[^[:space:]]'),
    CONSTRAINT sale_campaigns_window_ordered CHECK (ends_at > starts_at)
);

CREATE UNIQUE INDEX sale_campaigns_slug_key ON sale_campaigns (slug);

CREATE TRIGGER sale_campaigns_set_updated_at
    BEFORE UPDATE ON sale_campaigns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sale_campaign_products (
    campaign_id uuid NOT NULL REFERENCES sale_campaigns (id) ON DELETE CASCADE,
    product_id  uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    position    integer NOT NULL DEFAULT 0,
    PRIMARY KEY (campaign_id, product_id)
);

CREATE INDEX sale_campaign_products_product_id_idx ON sale_campaign_products (product_id);

-- A featured product that shows no saving is a promise the page cannot keep.
CREATE FUNCTION sale_campaign_products_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- Both halves of "a featured product keeps a discount" meet on the product
    -- row, and without this lock each guard misses the other's uncommitted work.
    -- No cycle: this one locks the product then only READS variants.
    PERFORM 1 FROM products WHERE id = NEW.product_id FOR UPDATE;

    IF NOT EXISTS (
        SELECT 1 FROM product_variants
        WHERE product_id = NEW.product_id
          AND is_active
          AND compare_at_price_cents IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'product % has no discounted variant to feature', NEW.product_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'sale_campaign_needs_discount';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER sale_campaign_needs_discount
    BEFORE INSERT OR UPDATE ON sale_campaign_products
    FOR EACH ROW EXECUTE FUNCTION sale_campaign_products_guard();

-- AFTER, not BEFORE, and DELETE as well as UPDATE. A BEFORE-row trigger sees the
-- pre-statement snapshot, so clearing every discount in one statement passes row
-- by row, and DELETE of the last discounted variant fires nothing at all.
CREATE FUNCTION sale_campaign_variant_still_valid() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target uuid := coalesce(NEW.product_id, OLD.product_id);
BEGIN
    IF NOT EXISTS (SELECT 1 FROM sale_campaign_products WHERE product_id = target) THEN
        RETURN NULL;
    END IF;

    PERFORM 1 FROM products WHERE id = target FOR UPDATE;

    IF NOT EXISTS (
        SELECT 1 FROM product_variants
        WHERE product_id = target AND is_active AND compare_at_price_cents IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'product % is featured in a campaign with no discounted variant left', target
            USING ERRCODE = 'check_violation', CONSTRAINT = 'sale_campaign_variant_still_valid';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER sale_campaign_variant_still_valid
    AFTER UPDATE OF compare_at_price_cents, is_active OR DELETE ON product_variants
    FOR EACH ROW EXECUTE FUNCTION sale_campaign_variant_still_valid();

CREATE TABLE faq_entries (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    category   text NOT NULL,
    question   text NOT NULL,
    answer     text NOT NULL,
    -- The English FAQ, each field separately optional; an entry with none
    -- renders its Chinese.
    category_en text,
    question_en text,
    answer_en   text,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT faq_entries_category_present CHECK (category ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_question_present CHECK (question ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_answer_present CHECK (answer ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_category_en_present
        CHECK (category_en IS NULL OR category_en ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_question_en_present
        CHECK (question_en IS NULL OR question_en ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_answer_en_present
        CHECK (answer_en IS NULL OR answer_en ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX faq_entries_position_key ON faq_entries (category, position);

CREATE TRIGGER faq_entries_set_updated_at
    BEFORE UPDATE ON faq_entries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE contact_messages (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    email      text NOT NULL,
    subject    text NOT NULL,
    order_ref  text,
    message    text NOT NULL,
    handled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT contact_messages_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT contact_messages_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT contact_messages_subject_present CHECK (subject ~ '[^[:space:]]'),
    CONSTRAINT contact_messages_message_present CHECK (message ~ '[^[:space:]]')
);

CREATE INDEX contact_messages_created_at_idx ON contact_messages (created_at DESC);
CREATE INDEX contact_messages_unhandled_idx ON contact_messages (created_at)
    WHERE handled_at IS NULL;

-- A row here is a SUBSCRIPTION, present or past. A request nobody has answered
-- lives in newsletter_confirmations instead, so a send query that forgets to ask
-- whether an address was confirmed still cannot reach one.
CREATE TABLE newsletter_subscribers (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    email              text NOT NULL,
    -- When its owner said yes. NOT NULL because the row means exactly that.
    confirmed_at       timestamptz NOT NULL DEFAULT now(),
    -- NULL while they are on the list. The row survives an opt-out: "this
    -- address asked not to be emailed" is what has to outlive the subscription.
    unsubscribed_at    timestamptz,
    -- The token itself and not a digest: `store` may already UPDATE this table,
    -- so a hash would defend nothing, and with only a digest kept the SEND could
    -- not reproduce the link. It does not expire.
    unsubscribe_token  text NOT NULL,
    -- The language they confirmed in: an issue is sent by a back-office click,
    -- where the subscriber is not present.
    locale             text NOT NULL DEFAULT 'zh-Hant',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT newsletter_subscribers_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT newsletter_subscribers_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT newsletter_subscribers_unsubscribe_token_present
        CHECK (length(unsubscribe_token) >= 32),
    CONSTRAINT newsletter_subscribers_unsubscribed_after_confirmed
        CHECK (unsubscribed_at IS NULL OR unsubscribed_at >= confirmed_at),
    CONSTRAINT newsletter_subscribers_locale_known CHECK (locale IN ('zh-Hant', 'en'))
);

CREATE UNIQUE INDEX newsletter_subscribers_email_key ON newsletter_subscribers (lower(email));

-- Two rows sharing a token would make which subscriber a link removes a matter
-- of plan order.
CREATE UNIQUE INDEX newsletter_subscribers_unsubscribe_token_key
    ON newsletter_subscribers (unsubscribe_token);

-- The back office reads the list newest first.
CREATE INDEX newsletter_subscribers_confirmed_at_idx
    ON newsletter_subscribers (confirmed_at DESC);

CREATE TRIGGER newsletter_subscribers_set_updated_at
    BEFORE UPDATE ON newsletter_subscribers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- An address waiting for its OWNER to say so. Anybody can type anybody's
-- address into the footer form, which is why this table is separate.
CREATE TABLE newsletter_confirmations (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    email      text NOT NULL,
    -- sha256 of the token in the confirmation link. Unlike the unsubscribe
    -- token this one is spent and expires.
    digest     bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT newsletter_confirmations_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT newsletter_confirmations_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT newsletter_confirmations_digest_sha256 CHECK (octet_length(digest) = 32),
    CONSTRAINT newsletter_confirmations_expires_after_created CHECK (expires_at > created_at)
);

-- One outstanding request per address: a second submission REPLACES the first,
-- so a mailbox holds one key and not three.
CREATE UNIQUE INDEX newsletter_confirmations_email_key
    ON newsletter_confirmations (lower(email));
CREATE UNIQUE INDEX newsletter_confirmations_digest_key
    ON newsletter_confirmations (digest);

-- One issue of the newsletter, and the record that it went out.
CREATE TABLE newsletter_issues (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    subject    text NOT NULL,
    body       text NOT NULL,
    -- NULL until it is sent, and this column is also the freeze: the trigger
    -- below refuses an edit once it is set.
    sent_at    timestamptz,
    -- Counted from the rows the send actually enqueued: a figure read
    -- beforehand can disagree with what was sent.
    recipients integer NOT NULL DEFAULT 0,
    -- NULL once that account is erased; the issue survives.
    sent_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT newsletter_issues_subject_present CHECK (subject ~ '[^[:space:]]'),
    CONSTRAINT newsletter_issues_body_present CHECK (body ~ '[^[:space:]]'),
    CONSTRAINT newsletter_issues_recipients_non_negative CHECK (recipients >= 0),
    CONSTRAINT newsletter_issues_unsent_has_no_recipients
        CHECK (sent_at IS NOT NULL OR recipients = 0)
);

CREATE INDEX newsletter_issues_created_at_idx ON newsletter_issues (created_at DESC);
CREATE INDEX newsletter_issues_sent_by_idx ON newsletter_issues (sent_by);

CREATE TRIGGER newsletter_issues_set_updated_at
    BEFORE UPDATE ON newsletter_issues
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A sent issue is frozen: ten thousand copies are in ten thousand mailboxes, and
-- there is no way to correct them.
CREATE FUNCTION newsletter_issues_check_frozen() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    IF OLD.sent_at IS NOT NULL
       AND (NEW.subject <> OLD.subject OR NEW.body <> OLD.body) THEN
        RAISE EXCEPTION 'newsletter issue % has been sent; its text cannot change', OLD.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'newsletter_issues_frozen_once_sent';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER newsletter_issues_frozen_once_sent
    BEFORE UPDATE OF subject, body ON newsletter_issues
    FOR EACH ROW EXECUTE FUNCTION newsletter_issues_check_frozen();

-- Erasing an account. order_private_data keys on the ORDER and
-- stock_notifications carries a plaintext email, so a plain DELETE of a user
-- reaches neither.
CREATE FUNCTION erase_user(p_user_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    addr text;
BEGIN
    -- Read the address before the account goes: the newsletter keys on the
    -- ADDRESS, so nothing below could find it afterwards.
    SELECT email INTO addr FROM users WHERE id = p_user_id;

    -- The LAST ADMIN cannot erase themselves, and this is the only place the
    -- question can be asked: /account/erase never consults the staff feature's
    -- guard, and there is no SQL recovery short of promoting somebody by hand.
    IF (SELECT role FROM users WHERE id = p_user_id) = 'admin'
       AND (SELECT count(*) FROM users WHERE role = 'admin') <= 1 THEN
        RAISE EXCEPTION 'the last admin cannot be erased'
            USING CONSTRAINT = 'erase_user_keeps_one_admin';
    END IF;

    -- Blank every delivery field and stamp erased_at: the all-NULL state
    -- order_private_data_all_or_erased permits.
    UPDATE order_private_data pd SET
        email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
        city = NULL, district = NULL, street = NULL,
        pickup_brand = NULL, pickup_store_code = NULL, pickup_store_name = NULL,
        erased_at = now()
    FROM orders o
    WHERE pd.order_id = o.id AND o.user_id = p_user_id AND pd.erased_at IS NULL;

    -- customer_note is the customer's own words and routinely carries PII;
    -- staff_note is internal and stays. Nulling a note does not trip
    -- orders_freeze_money, so a paid order erases too.
    UPDATE orders SET customer_note = NULL
    WHERE user_id = p_user_id AND customer_note IS NOT NULL;

    -- The restock email is NOT NULL and cannot be blanked, so the rows go.
    -- By user_id AND by address, because anyone may ask for one signed OUT:
    -- a notice taken before the customer had an account carries no user_id,
    -- so a delete keyed on the account never reaches it — and the worker
    -- would then email an address the shop has been told to forget, the day
    -- the variant comes back. This is the contact_messages shape, and the
    -- newsletter is not the only table that keys on the address.
    DELETE FROM stock_notifications WHERE user_id = p_user_id;

    -- The invoice PREFERENCE carries a personal carrier id and a business tax
    -- number. It cannot be nulled in place — its CHECKs require the value for
    -- their type — so the row goes; invoice_documents is the tax record and stays.
    DELETE FROM invoice_preferences ip
    USING orders o
    WHERE ip.order_id = o.id AND o.user_id = p_user_id;

    -- The newsletter keys on the ADDRESS rather than the account. The pending
    -- confirmation goes too: a link already in the mailbox would let the erased
    -- address rejoin the list.
    IF addr IS NOT NULL THEN
        DELETE FROM newsletter_subscribers WHERE lower(email) = lower(addr);
        DELETE FROM newsletter_confirmations WHERE lower(email) = lower(addr);
        -- contact_messages is the SECOND address-keyed table: no user_id, no
        -- foreign key, and it holds a name, an address and whatever the customer
        -- typed.
        DELETE FROM contact_messages WHERE lower(email) = lower(addr);
        DELETE FROM stock_notifications WHERE lower(email) = lower(addr);

        -- The OUTBOX holds the address inside its payload, and outbox.Retain
        -- keeps a delivered message for 30 days — so without this, an erased
        -- customer's address survives the erasure by a month, in the one table
        -- that also carries reset links and unsubscribe tokens. Undelivered
        -- messages go with it: a letter to an address the shop has been told to
        -- forget must not still be waiting to leave.
        -- The whole payload as text, not payload->>'email'. A Go struct field
        -- with no json tag marshals under its GO name, and a jsonb key is
        -- case-sensitive — so the ONE message that carries a live password
        -- reset token was the one this could not reach, and it survived the
        -- erasure for the 30 days outbox.Retain keeps a row. A predicate that
        -- depends on somebody remembering a struct tag is a predicate that
        -- eventually misses one; every row here is a letter, so an address
        -- appearing anywhere in it means the letter is to that person.
        DELETE FROM outbox_messages WHERE payload::text ILIKE '%' || addr || '%';
    END IF;

    -- Every browser's proof of access to this person's orders: a live bearer
    -- credential keyed on the ORDER, with ON DELETE RESTRICT, so nothing above
    -- reaches it.
    DELETE FROM order_access_grants g
    USING orders o
    WHERE g.order_id = o.id AND o.user_id = p_user_id;

    -- The account itself. Its foreign keys carry the rest: actor columns go to
    -- NULL, auth rows cascade.
    DELETE FROM users WHERE id = p_user_id;
END;
$$;

-- ============================================================================
-- Promoting an existing account must not hand over whatever credential it holds.
--
-- goen does not verify an address at REGISTRATION, so anybody may register an
-- address they expect to be hired at, keep their own password and a live
-- session, and wait. UpsertStaff resolves ON CONFLICT (lower(email)) and sets
-- only the role, so the promotion handed that person the back office: sessions
-- read users.role live, and StaffOnly then let the same session enrol its own
-- second factor. Reproduced end to end before this existed.
--
-- The rule this restores is the one the INSERT beside it already states — a new
-- colleague gets NO password and proves the mailbox through /forgot. An account
-- that has not proved its address is in exactly that position, whoever created
-- it, so its credential is cleared and its sessions end. A VERIFIED account
-- provably belongs to whoever reads that mailbox, which is the person being
-- hired, and keeps both.
--
-- SECURITY DEFINER because `admin` deliberately holds no UPDATE on
-- users.password_hash: with it, a staff member could impersonate a customer
-- silently. This is erase_user's pattern rather than record_inventory_movement's
-- — it elevates ONE narrow act for a role denied the general privilege, and is
-- not a sole door to the column, which the app still writes for password
-- changes and resets.
-- ============================================================================
CREATE FUNCTION secure_promoted_account(p_user_id uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    cleared boolean := false;
BEGIN
    UPDATE users SET password_hash = NULL
    WHERE id = p_user_id AND email_verified_at IS NULL AND password_hash IS NOT NULL;
    cleared := FOUND;

    -- Always, not only when the credential was cleared: a session that predates
    -- the promotion was opened by somebody the shop had not yet decided to trust
    -- with the back office, and role is read live on every request.
    DELETE FROM sessions WHERE user_id = p_user_id;

    RETURN cleared;
END $$;

COMMENT ON FUNCTION secure_promoted_account(uuid) IS
    'Neutralises an unproved credential on an account being given back-office access, and ends its sessions.';


-- ============================================================================
-- Privileges
--
-- Applied last, once every table and function exists: store gets ordinary
-- read/write, then the privileged tables have their direct writes revoked so the
-- only way in is a SECURITY DEFINER function.
-- ============================================================================

GRANT USAGE ON SCHEMA public TO store, reporting;

GRANT SELECT ON ALL TABLES IN SCHEMA public TO store, reporting;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO store;

-- reporting reads BUSINESS data, not everything. A table added later is swept in
-- by the blanket GRANT above and stays there silently, so
-- TestReportingCannotReadCredentialsOrPII asks per named table.
REVOKE SELECT ON
    sessions, password_reset_tokens, staff_totp_credentials, user_identities,
    order_private_data, payment_webhook_events, order_access_grants,
    email_verifications, newsletter_confirmations,
    -- outbox_messages is the least obvious: a reset link, an unsubscribe link
    -- and a newsletter confirmation all travel in the PAYLOAD in plaintext,
    -- because sending from the handler loses the message when the process dies.
    users, addresses, carts, contact_messages, invoice_preferences,
    newsletter_subscribers, outbox_messages, stock_notifications
    FROM reporting;

-- Tables whose integrity depends on going through a function; SELECT stays.
-- INSERT is revoked with UPDATE and DELETE, or store writes a born-succeeded
-- capture, or a variant carrying stock the ledger never posted.
REVOKE INSERT, UPDATE, DELETE ON
    inventory_movements, inventory_reservations, audit_events, store_credit_entries
    FROM store;
REVOKE INSERT, UPDATE, DELETE ON product_variants FROM store;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM store;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM store;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM store;
-- UPDATE would repoint a whole balance at another user. DELETE is deleting a
-- balance: an account whose entries net to zero would go, taking the trail with
-- it. INSERT stays, because the account is created on first use.
REVOKE UPDATE, DELETE ON store_credit_accounts FROM store;
-- Deleting a webhook row makes a resent event look new and be processed twice;
-- rewriting the payload rewrites the evidence. The app only stamps when it
-- processed one.

-- A coupon is merchandising: the storefront may READ one to apply it and nothing
-- else, and coupon_redemptions is a ledger that goes through a function.
REVOKE INSERT, UPDATE, DELETE ON coupons, coupon_redemptions FROM store;
REVOKE UPDATE, DELETE ON payment_webhook_events FROM store;
GRANT UPDATE (processed_at, unreconciled) ON payment_webhook_events TO store;
-- reconciled_at is the SHOP saying it refunded money by hand, so it is admin's
-- to write and not the storefront's. A whole-table INSERT would carry it.
REVOKE INSERT ON payment_webhook_events FROM store;
GRANT INSERT (provider, event_id, type, object_ref, payload) ON payment_webhook_events TO store;
-- order_events and shipping_method_versions are append-only too, so the privilege
-- layer backs forbid_change. INSERT stays: the app appends an event, and a new
-- shipping version is an insert.
REVOKE UPDATE ON order_events, shipping_method_versions FROM store;
-- A user must be erased through erase_user(), which also blanks the delivery PII
-- on their orders and drops their restock emails.
REVOKE DELETE ON users FROM store;

-- A newsletter row is a SUPPRESSION record as much as a subscription: deleting it
-- is how a list quietly starts emailing somebody again. Unsubscribing is an
-- UPDATE, which stays.
REVOKE DELETE ON newsletter_subscribers FROM store;
-- A storefront request has no business publishing a newsletter.
REVOKE INSERT, UPDATE, DELETE ON newsletter_issues FROM store;
REVOKE DELETE, TRUNCATE ON
    orders, order_lines, order_private_data, order_shipments,
    order_shipment_lines, order_events, invoice_documents, invoice_preferences,
    return_requests, return_request_lines, warranty_registrations,
    payments, refunds, shipping_method_versions
    FROM store;


-- The posting functions run as their owner, so they can write what store cannot.
-- next_order_number joins them so the counter it increments can be write-revoked
-- above.
ALTER FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) SECURITY DEFINER;
ALTER FUNCTION next_order_number() SECURITY DEFINER;

-- What "committed" means, as a SET. A view as well as a function because it is
-- measured: per row over 14,000 orders costs 106 ms — the planner cannot turn two
-- EXISTS subqueries into a join — while the same question set-wise is 7.7 ms.
CREATE VIEW committed_orders AS
    SELECT o.id
    FROM orders o
    WHERE o.fulfillment_status <> 'cancelled'
      AND (o.fulfillment_status <> 'pending'
           OR EXISTS (SELECT 1 FROM payments p
                      WHERE p.order_id = o.id AND p.status = 'succeeded'));

COMMENT ON VIEW committed_orders IS
    'The single definition of a committed order: work the shop has taken on. '
    'Aggregates JOIN this; row-at-a-time callers use order_is_committed().';

-- Every order whose money and lines are FINAL, which is not the same question.
-- Committed means the shop is doing the work; settled means the record is closed.
-- Cancelled is settled and NOT committed, or its stock can never come back.
CREATE VIEW settled_orders AS
    SELECT id FROM committed_orders
    UNION
    SELECT id FROM orders WHERE fulfillment_status = 'cancelled';

COMMENT ON VIEW settled_orders IS
    'Orders whose money and lines may no longer change: committed, or cancelled.';

-- Granted EXPLICITLY: the sweeping GRANT ... ON ALL TABLES already ran, so a view
-- created below it starts with no privileges — and order_is_committed() is
-- SECURITY INVOKER, so every checkout would die on it.
GRANT SELECT ON committed_orders, settled_orders TO store, reporting;

-- What an order still owes: its total, less the store credit spent on it, NET OF
-- REVERSALS — summing only `amount_cents < 0` counts the ghost of a reversed
-- spend and lets an unfunded order ship.
CREATE FUNCTION order_amount_owed(p_order_id uuid) RETURNS bigint
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT (
        coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                  FROM order_lines ol WHERE ol.order_id = o.id), 0)
        - o.discount_cents + o.shipping_cents + o.tax_cents
    ) + coalesce((
        SELECT sum(s.amount_cents + coalesce(r.amount_cents, 0))
        FROM store_credit_entries s
        LEFT JOIN store_credit_entries r ON r.reverses_id = s.id
        WHERE s.order_id = o.id AND s.amount_cents < 0), 0)
    FROM orders o WHERE o.id = p_order_id;
$$;

COMMENT ON FUNCTION order_amount_owed(uuid) IS
    'The amount still payable on an order: total less store credit spent on it, '
    'net of reversals. The one definition every funding check and the payment page '
    'read, so the figure charged and the figure demanded cannot disagree.';

-- The discount is allocated PROPORTIONALLY to what is going back and rounded UP,
-- so several partial returns cannot sum past the capture. tax_cents is absent
-- because a displayed price is tax-inclusive (Business Tax Act §32 II).
CREATE FUNCTION return_refundable_amount(p_return_request_id uuid) RETURNS bigint
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT ret.gross
         - ceil(o.discount_cents::numeric * ret.gross::numeric
                / nullif(ord.subtotal, 0)::numeric)::bigint
         + CASE WHEN NOT EXISTS (
               SELECT 1 FROM order_lines ol
               WHERE ol.order_id = o.id
                 AND ol.quantity > (
                     SELECT coalesce(sum(rl.quantity), 0)
                     FROM return_request_lines rl
                     JOIN return_requests rr ON rr.id = rl.return_request_id
                     WHERE rl.order_line_id = ol.id AND rr.status <> 'rejected')
           ) THEN o.shipping_cents ELSE 0 END
    FROM return_requests r
    JOIN orders o ON o.id = r.order_id
    CROSS JOIN LATERAL (
        SELECT coalesce(sum(rl.quantity * ol.unit_price_cents), 0)::bigint AS gross
        FROM return_request_lines rl
        JOIN order_lines ol ON ol.id = rl.order_line_id
        WHERE rl.return_request_id = r.id
    ) ret
    CROSS JOIN LATERAL (
        SELECT coalesce(sum(ol.quantity * ol.unit_price_cents), 0)::bigint AS subtotal
        FROM order_lines ol WHERE ol.order_id = o.id
    ) ord
    WHERE r.id = p_return_request_id;
$$;

COMMENT ON FUNCTION return_refundable_amount(uuid) IS
    'What one return request is worth paying back: the returned goods at their '
    'order-line prices, less their proportional share of the order discount, '
    'plus the delivery fee when the request completes a full rescission. The one '
    'definition, read by the queue and by the decision page, so the figure a '
    'staff member sees and the figure that is paid cannot disagree.';

CREATE FUNCTION order_is_committed(p_order_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM committed_orders WHERE id = p_order_id);
$$;

CREATE FUNCTION order_is_settled(p_order_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM settled_orders WHERE id = p_order_id);
$$;

-- What a customer has spent on committed orders in a rolling window.
-- p_exclude_order is the order being paid for RIGHT NOW: the capture commits it
-- before points are awarded, so it would otherwise raise its own tier.
CREATE FUNCTION member_spend(p_user_id uuid, p_days integer,
                             p_exclude_order uuid DEFAULT NULL)
RETURNS bigint LANGUAGE sql STABLE AS $$
    SELECT coalesce(sum(
        coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                  FROM order_lines ol WHERE ol.order_id = o.id), 0)
        - o.discount_cents + o.shipping_cents + o.tax_cents), 0)::bigint
    FROM orders o
    JOIN committed_orders c ON c.id = o.id
    WHERE o.user_id = p_user_id
      AND o.placed_at >= now() - make_interval(days => p_days)
      AND (p_exclude_order IS NULL OR o.id <> p_exclude_order);
$$;

-- The highest band at or below that spend, or NULL for a customer below every
-- threshold.
CREATE FUNCTION member_tier(p_user_id uuid, p_days integer,
                            p_exclude_order uuid DEFAULT NULL)
RETURNS uuid LANGUAGE sql STABLE AS $$
    SELECT t.id FROM membership_tiers t
    WHERE t.min_spend_cents <= member_spend(p_user_id, p_days, p_exclude_order)
    ORDER BY t.min_spend_cents DESC
    LIMIT 1;
$$;


GRANT EXECUTE ON FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) TO store;
GRANT EXECUTE ON FUNCTION hold_inventory(uuid, uuid, integer, timestamptz, text) TO store;
GRANT EXECUTE ON FUNCTION consume_reservation(uuid) TO store;
GRANT EXECUTE ON FUNCTION release_reservation(uuid) TO store;
GRANT EXECUTE ON FUNCTION next_order_number() TO store;
GRANT EXECUTE ON FUNCTION erase_user(uuid) TO store;
-- The freeze triggers run SECURITY INVOKER, so they call this as store, and an
-- explicit call checks EXECUTE where firing a trigger does not.
GRANT EXECUTE ON FUNCTION order_is_committed(uuid) TO store;
-- The payment page reads what an order owes: without this every /pay is a 500 in
-- production and nowhere else, because every test connects as the owner.
GRANT EXECUTE ON FUNCTION order_amount_owed(uuid) TO store;
GRANT EXECUTE ON FUNCTION order_is_settled(uuid) TO store;
GRANT EXECUTE ON FUNCTION member_spend(uuid, integer, uuid) TO store;
GRANT EXECUTE ON FUNCTION member_tier(uuid, integer, uuid) TO store;

-- ============================================================================
-- The back office
--
-- `admin` is a WIDER set than store and still writes no money or stock directly.
-- The binary opens a SECOND pool for it: SET ROLE per request would leave the
-- role set on a connection returned to the pool.
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'admin') THEN
        CREATE ROLE admin NOLOGIN;
    END IF;
    -- NOSUPERUSER, owns nothing, and its password is set by operations.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'admin_svc') THEN
        CREATE ROLE admin_svc LOGIN NOSUPERUSER IN ROLE admin;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO admin;

-- Granted here because the view is created before this role exists, and it is not
-- redundant: admin is NOT a member of store — pg_auth_members holds no such edge
-- — and that independence is what makes the column revokes below work.
GRANT SELECT ON committed_orders, settled_orders TO admin;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO admin;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO admin;

-- Everything store is barred from, with one exception: product_variants, because
-- maintaining the catalogue IS the back office's job. stock_quantity is not among
-- what it may set — see the column revoke below.
REVOKE INSERT, UPDATE, DELETE ON
    inventory_movements, inventory_reservations, audit_events, store_credit_entries
    FROM admin;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM admin;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM admin;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM admin;
REVOKE UPDATE, DELETE ON store_credit_accounts FROM admin;
-- The back office issues coupons and still may not write a redemption: that row
-- is a fact about an order's money, posted by the same function the storefront
-- uses.
REVOKE INSERT, UPDATE, DELETE ON coupon_redemptions FROM admin;
-- The back office may say it dealt with an event and nothing else: what an
-- event SAID is the provider's statement and not the shop's to edit, while
-- whether somebody acted on it is exactly the shop's to record. Without the
-- column grant the alarm is monotone and /admin/health is unhealthy forever
-- after the first one.
REVOKE UPDATE, DELETE ON payment_webhook_events FROM admin;
GRANT UPDATE (reconciled_at) ON payment_webhook_events TO admin;
REVOKE UPDATE ON order_events, shipping_method_versions FROM admin;
REVOKE DELETE ON users FROM admin;
-- Verification is the customer answering a letter, and a staff member who could
-- write this table could mark any address proved.
REVOKE INSERT, UPDATE, DELETE ON email_verifications FROM admin;

-- ============================================================================
-- The back office may not become a customer.
--
-- With INSERT on sessions and UPDATE on users.password_hash, a staff member could
-- impersonate one silently, and admin holds no INSERT on audit_events. What it
-- genuinely writes is a role and a name, and BOTH verbs take the column list.
-- ============================================================================
REVOKE INSERT, UPDATE ON users FROM admin;
GRANT INSERT (email, full_name, role) ON users TO admin;
GRANT UPDATE (role, full_name) ON users TO admin;

-- sessions: the back office ENDS them and stamps totp_verified_at. Creating one
-- is signing somebody in, which only the sign-in form does.
REVOKE INSERT, UPDATE ON sessions FROM admin;
GRANT UPDATE (totp_verified_at) ON sessions TO admin;
-- The shop cannot put an address on its own mailing list, or take one off it.
-- That is what double opt-in MEANS. SELECT stays.
REVOKE INSERT, UPDATE, DELETE ON newsletter_subscribers, newsletter_confirmations
    FROM admin;
-- It composes and sends. A sent issue is what the shop published, and the
-- mailboxes holding it cannot be edited either.
REVOKE DELETE ON newsletter_issues FROM admin;
REVOKE DELETE, TRUNCATE ON
    orders, order_lines, order_private_data, order_shipments,
    order_shipment_lines, order_events, invoice_documents, invoice_preferences,
    return_requests, return_request_lines, warranty_registrations,
    payments, refunds, shipping_method_versions
    FROM admin;

-- Stock is the one column an admin may not set by hand. A column-level REVOKE
-- does NOT cut into a table-level grant — PostgreSQL reads table-level UPDATE as
-- every column — so the table grant goes first and BOTH verbs take a list.
REVOKE INSERT, UPDATE ON product_variants FROM admin;
-- stock_quantity is in neither list, so the column takes its DEFAULT 0 and
-- record_inventory_movement stays its one writer.
GRANT INSERT (product_id, sku, price_cents, compare_at_price_cents, safety_stock,
              preorder_release_on, position, is_active,
              parcel_longest_mm, parcel_sum_mm, parcel_weight_g)
    ON product_variants TO admin;
GRANT UPDATE (sku, price_cents, compare_at_price_cents, safety_stock,
              preorder_release_on, position, is_active, updated_at,
              parcel_longest_mm, parcel_sum_mm, parcel_weight_g)
    ON product_variants TO admin;

GRANT EXECUTE ON FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) TO admin;
GRANT EXECUTE ON FUNCTION consume_reservation(uuid) TO admin;
-- admin ONLY: dispatching is the back office's act, and store's holds are taken
-- and released whole.
GRANT EXECUTE ON FUNCTION consume_reservation_partial(uuid, integer) TO admin;
GRANT EXECUTE ON FUNCTION release_reservation(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_is_committed(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_amount_owed(uuid) TO admin;
-- admin only: deciding a return is the back office's act, and the customer's own
-- pages never state an amount.
GRANT EXECUTE ON FUNCTION return_refundable_amount(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_is_settled(uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_spend(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_tier(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION erase_user(uuid) TO admin;
-- The back office promotes; the function is how it neutralises whatever
-- credential an unproved account was carrying, which admin's own column
-- grants deliberately cannot reach.
GRANT EXECUTE ON FUNCTION secure_promoted_account(uuid) TO admin;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'schema_migrations'
               AND relnamespace = 'public'::regnamespace) THEN
        EXECUTE 'REVOKE ALL ON schema_migrations FROM admin';
    END IF;
END
$$;

-- ============================================================================
-- Payment posting
--
-- store holds no INSERT on payments, so these are the door. A capture takes the
-- amount the PROVIDER reports and lets payments_capture_matches_order refuse it
-- if that disagrees with what the order is owed.
-- ============================================================================


-- Open a payment intent against an order, or return the one already open:
-- Stripe's own idempotency means a retried create returns the same intent, and
-- this must not then make a second row.
CREATE FUNCTION open_payment(
    p_order_id uuid,
    p_provider_ref text,
    p_intended_amount_cents bigint
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    payment_id uuid;
BEGIN
    SELECT id INTO payment_id FROM payments
    WHERE order_id = p_order_id AND provider_ref = p_provider_ref;
    IF FOUND THEN
        RETURN payment_id;
    END IF;

    INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
    VALUES (p_order_id, p_provider_ref, 'requires_payment', p_intended_amount_cents)
    RETURNING id INTO payment_id;
    RETURN payment_id;
END;
$$;

-- Record that the provider captured money. Idempotent, because a webhook is
-- at-least-once — which is also why payments_settled_is_history never has to
-- refuse a duplicate.
CREATE FUNCTION capture_payment(
    p_provider_ref text,
    p_captured_amount_cents bigint,
    p_card_brand text,
    p_card_last4 text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    payment_id uuid;
    current_status text;
BEGIN
    SELECT id, status INTO payment_id, current_status
    FROM payments WHERE provider_ref = p_provider_ref
    FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'no payment for provider reference %', p_provider_ref
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_provider_ref_known';
    END IF;
    IF current_status = 'succeeded' THEN
        -- Already captured. A repeated webhook is not an error.
        RETURN payment_id;
    END IF;

    UPDATE payments
    SET status = 'succeeded',
        captured_amount_cents = p_captured_amount_cents,
        paid_at = now(),
        card_brand = p_card_brand,
        card_last4 = p_card_last4
    WHERE id = payment_id;
    RETURN payment_id;
END;
$$;

-- 'cancelled', not 'failed': payments_status_known does not admit the latter.
CREATE FUNCTION cancel_payment(p_provider_ref text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE payments SET status = 'cancelled'
    WHERE provider_ref = p_provider_ref AND status <> 'succeeded';
END;
$$;

-- Post a store-credit entry: store holds no INSERT, so this is the door.
-- store_credit_never_negative takes the account row FOR UPDATE before it reads.
CREATE FUNCTION post_store_credit(
    p_user_id uuid,
    p_amount_cents bigint,
    p_reason text,
    p_order_id uuid,
    p_idempotency_key text,
    p_actor_user_id uuid
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    account_id uuid;
    entry_id uuid;
BEGIN
    -- An account is created on first use rather than at registration: most
    -- customers never have store credit.
    SELECT id INTO account_id FROM store_credit_accounts WHERE user_id = p_user_id;
    IF NOT FOUND THEN
        INSERT INTO store_credit_accounts (user_id) VALUES (p_user_id)
        RETURNING id INTO account_id;
    END IF;

    SELECT id INTO entry_id FROM store_credit_entries
    WHERE idempotency_key = p_idempotency_key;
    IF FOUND THEN
        RETURN entry_id;
    END IF;

    INSERT INTO store_credit_entries
        (account_id, amount_cents, reason, order_id, idempotency_key, actor_user_id)
    VALUES (account_id, p_amount_cents, p_reason, p_order_id, p_idempotency_key, p_actor_user_id)
    RETURNING id INTO entry_id;
    RETURN entry_id;
END;
$$;

-- Give back credit spent on an order nobody is going to ship. A separate function
-- rather than a reverses_id parameter on post_store_credit, because a reversal
-- undoes ONE specific entry and a nullable parameter is one passed by accident.
CREATE FUNCTION reverse_order_credit(p_order_id uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    spend  store_credit_entries%ROWTYPE;
    total  bigint := 0;
BEGIN
    FOR spend IN
        SELECT * FROM store_credit_entries
        WHERE order_id = p_order_id
          AND amount_cents < 0
          AND reverses_id IS NULL
        ORDER BY created_at
    LOOP
        -- Already reversed: skipping keeps the returned total honest about what
        -- THIS call gave back.
        CONTINUE WHEN EXISTS (
            SELECT 1 FROM store_credit_entries r WHERE r.reverses_id = spend.id
        );

        INSERT INTO store_credit_entries
            (account_id, amount_cents, reason, reverses_id, idempotency_key)
        VALUES (spend.account_id, -spend.amount_cents, 'order cancelled',
                spend.id, 'reverse:' || spend.id);
        total := total + (-spend.amount_cents);
    END LOOP;
    RETURN total;
END;
$$;

COMMENT ON FUNCTION reverse_order_credit(uuid) IS
    'Return store credit spent on an order that will not ship. Legal only while '
    'the order is a pending unpaid checkout or has been cancelled — '
    'store_credit_guard enforces that, so calling this on a shipped order is '
    'refused rather than quietly paying twice.';

GRANT EXECUTE ON FUNCTION open_payment(uuid, text, bigint) TO store;
GRANT EXECUTE ON FUNCTION capture_payment(text, bigint, text, text) TO store;
GRANT EXECUTE ON FUNCTION cancel_payment(text) TO store;
GRANT EXECUTE ON FUNCTION post_store_credit(uuid, bigint, text, uuid, text, uuid) TO store;
-- A customer cancels their own unpaid order, so the storefront role needs this.
GRANT EXECUTE ON FUNCTION reverse_order_credit(uuid) TO store;
GRANT EXECUTE ON FUNCTION hold_inventory(uuid, uuid, integer, timestamptz, text) TO admin;
GRANT EXECUTE ON FUNCTION post_store_credit(uuid, bigint, text, uuid, text, uuid) TO admin;
-- And the back office cancels on a customer's behalf.
GRANT EXECUTE ON FUNCTION reverse_order_credit(uuid) TO admin;

-- The sweep at the foot of this file revokes EXECUTE from PUBLIC, so an ungranted
-- function is a 500 on every storefront page — and green in every test, because
-- the tests connect as the owner.
GRANT EXECUTE ON FUNCTION localized_name(text, text, text) TO store, admin, reporting;


-- Refund posting. Two functions, not one, because the provider call sits between
-- them: the row is committed BEFORE Stripe is asked, so a crash between the
-- request and the response leaves something reconciliation can find.
CREATE FUNCTION open_refund(
    p_payment_id uuid,
    p_request_key text,
    p_amount_cents bigint,
    p_reason text,
    p_return_request_id uuid
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    refund_id uuid;
BEGIN
    -- Idempotent on the caller's key: a retried approval finds its own row
    -- rather than asking Stripe for a second refund.
    SELECT id INTO refund_id FROM refunds WHERE request_key = p_request_key;
    IF FOUND THEN
        RETURN refund_id;
    END IF;

    INSERT INTO refunds (payment_id, return_request_id, request_key, status,
                         amount_cents, reason)
    VALUES (p_payment_id, p_return_request_id, p_request_key, 'pending',
            p_amount_cents, p_reason)
    RETURNING id INTO refund_id;
    RETURN refund_id;
END;
$$;

-- Record what the provider said. Only ever called with an answer in hand.
CREATE FUNCTION settle_refund(
    p_request_key text,
    p_provider_ref text,
    p_status text
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    current_status text;
BEGIN
    SELECT status INTO current_status FROM refunds
    WHERE request_key = p_request_key FOR UPDATE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'no refund for request key %', p_request_key
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_request_key_known';
    END IF;
    IF current_status = 'succeeded' THEN
        RETURN;  -- already settled; a repeated webhook is not an error
    END IF;

    UPDATE refunds
    SET status       = p_status,
        provider_ref = coalesce(p_provider_ref, provider_ref),
        succeeded_at = CASE WHEN p_status = 'succeeded' THEN now() ELSE succeeded_at END,
        failed_at    = CASE WHEN p_status = 'failed'    THEN now() ELSE failed_at END
    WHERE request_key = p_request_key;
END;
$$;

GRANT EXECUTE ON FUNCTION open_refund(uuid, text, bigint, text, uuid) TO admin;
GRANT EXECUTE ON FUNCTION settle_refund(text, text, text) TO admin;
-- store deliberately gets no EXECUTE on settle_refund: it is a SECURITY DEFINER
-- path around store's revoke on refunds, and 'failed' or 'cancelled' drops a
-- refund out of refunds_guard's sum, freeing the allowance to be claimed again.


-- Redeem a coupon against an order. The amount is passed in rather than
-- recomputed, because pricing it twice is how the two come to disagree. The
-- coupon row is locked FIRST, or two checkouts each read "9 of 10 used".
CREATE FUNCTION redeem_coupon(
    p_coupon_id uuid,
    p_order_id uuid,
    p_user_id uuid,
    p_amount_cents bigint
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    c coupons%ROWTYPE;
    used integer;
    used_by_customer integer;
    redemption_id uuid;
BEGIN
    SELECT * INTO c FROM coupons WHERE id = p_coupon_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'no such coupon'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'coupon_exists';
    END IF;

    IF NOT c.is_active
       OR c.starts_at > now()
       OR (c.ends_at IS NOT NULL AND c.ends_at <= now()) THEN
        RAISE EXCEPTION 'coupon % is not currently valid', c.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'coupon_is_current';
    END IF;

    -- CANCELLED orders do not count: coupon_redemptions is append-only and
    -- revoked from everybody, so counting one consumes a slot forever. A PENDING
    -- unpaid order still counts — it is a checkout in flight.
    IF c.max_redemptions IS NOT NULL THEN
        SELECT count(*) INTO used
        FROM coupon_redemptions cr
        JOIN orders o ON o.id = cr.order_id
        WHERE cr.coupon_id = c.id AND o.fulfillment_status <> 'cancelled';
        IF used >= c.max_redemptions THEN
            RAISE EXCEPTION 'coupon % is fully redeemed', c.code
                USING ERRCODE = 'check_violation', CONSTRAINT = 'coupon_within_total_limit';
        END IF;
    END IF;

    -- The per-customer limit binds on accounts. A guest has none, so for them
    -- the total cap is the only bound.
    IF p_user_id IS NOT NULL THEN
        SELECT count(*) INTO used_by_customer
        FROM coupon_redemptions cr
        JOIN orders o ON o.id = cr.order_id
        WHERE cr.coupon_id = c.id AND cr.user_id = p_user_id
          AND o.fulfillment_status <> 'cancelled';
        IF used_by_customer >= c.per_customer_limit THEN
            RAISE EXCEPTION 'coupon % already used by this customer', c.code
                USING ERRCODE = 'check_violation', CONSTRAINT = 'coupon_within_customer_limit';
        END IF;
    END IF;

    INSERT INTO coupon_redemptions (coupon_id, order_id, user_id, amount_cents)
    VALUES (c.id, p_order_id, p_user_id, p_amount_cents)
    RETURNING id INTO redemption_id;
    RETURN redemption_id;
END;
$$;

GRANT EXECUTE ON FUNCTION redeem_coupon(uuid, uuid, uuid, bigint) TO store;
GRANT EXECUTE ON FUNCTION redeem_coupon(uuid, uuid, uuid, bigint) TO admin;


-- ---------------------------------------------------------------------------
-- Loyalty points
--
-- A ledger, exactly like store_credit_entries. Points are not a second currency:
-- they convert to store credit at one published rate and are spendable nowhere
-- else. Expiry is per ENTRY, not per account.
-- ---------------------------------------------------------------------------

CREATE TABLE loyalty_entries (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    -- At the ACCOUNT, not the user, so erasure leaves the ledger balanced.
    account_id      uuid NOT NULL REFERENCES store_credit_accounts (id) ON DELETE RESTRICT,
    points          bigint NOT NULL,
    reason          text NOT NULL,
    -- The caller's name for this posting: a retried award submits the same key
    -- and meets the unique index.
    idempotency_key text NOT NULL,
    order_id        uuid REFERENCES orders (id) ON DELETE RESTRICT,
    -- NULL for a spend, which has already happened. An award with no expiry is
    -- a liability that grows forever.
    expires_on      date,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT loyalty_entries_points_nonzero CHECK (points <> 0),
    CONSTRAINT loyalty_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT loyalty_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]'),
    -- A spend cannot expire and an award must, or a spend silently disappears
    -- from the balance.
    CONSTRAINT loyalty_entries_expiry_matches_sign
        CHECK ((points > 0) = (expires_on IS NOT NULL))
);

CREATE UNIQUE INDEX loyalty_entries_idempotency_key
    ON loyalty_entries (idempotency_key);
CREATE INDEX loyalty_entries_account_idx
    ON loyalty_entries (account_id, expires_on);
CREATE INDEX loyalty_entries_order_id_idx ON loyalty_entries (order_id);

CREATE TRIGGER loyalty_entries_append_only
    BEFORE UPDATE OR DELETE ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_change('loyalty_entries_append_only');

-- The spendable balance: awards that have not expired, less everything spent.
-- Expiry is applied HERE rather than by a job, which would leave expired points
-- spendable until it ran.
CREATE VIEW loyalty_balances AS
    SELECT a.id AS account_id,
           coalesce(sum(e.points) FILTER (
               WHERE e.points < 0 OR e.expires_on >= current_date), 0)::bigint AS points
    FROM store_credit_accounts a
    LEFT JOIN loyalty_entries e ON e.account_id = a.id
    GROUP BY a.id;

COMMENT ON VIEW loyalty_balances IS
    'Spendable points per account: unexpired awards less everything spent. '
    'Expiry is applied on read, never by a job that might not have run.';

-- Points may not go negative. This lock is DEFENCE IN DEPTH: every posting path
-- goes through redeem_loyalty_points, which takes the same lock first, so
-- removing this one leaves every test green.
CREATE FUNCTION loyalty_never_negative() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    balance bigint;
BEGIN
    PERFORM 1 FROM store_credit_accounts WHERE id = NEW.account_id FOR UPDATE;

    SELECT points INTO balance FROM loyalty_balances WHERE account_id = NEW.account_id;
    IF balance < 0 THEN
        RAISE EXCEPTION 'account % would hold % points', NEW.account_id, balance
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_never_negative';
    END IF;
    RETURN NEW;
END;
$$;

-- AFTER, so the balance it reads includes the row being checked.
CREATE CONSTRAINT TRIGGER loyalty_never_negative
    AFTER INSERT ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION loyalty_never_negative();

GRANT SELECT ON loyalty_entries, loyalty_balances TO store, admin, reporting;
-- Posting is through the function below and nowhere else.
REVOKE INSERT, UPDATE, DELETE ON loyalty_entries FROM store, admin;

CREATE VIEW store_credit_balances AS
    SELECT a.id AS account_id, a.user_id,
           coalesce(sum(e.amount_cents), 0)::bigint AS balance_cents
    FROM store_credit_accounts a
    LEFT JOIN store_credit_entries e ON e.account_id = a.id
    GROUP BY a.id, a.user_id;

COMMENT ON VIEW store_credit_balances IS
    'Store credit per account, summed from the ledger. Never a stored column: a '
    'balance two concurrent writers each read and each overwrite is the defect '
    'the ledger exists to prevent.';

-- Explicitly, because GRANT ... ON ALL TABLES ran thousands of lines above and a
-- view created after it is granted to nobody.
GRANT SELECT ON store_credit_balances TO store, admin, reporting;

-- What has gone back to the customer on one order, by source and in total.
--
-- A refund is paid to the card, to store credit, or split between them —
-- splitRefund pays the card first and credit last — so "what has been refunded"
-- has two halves and every caller needs the sum. It was computed in three
-- places instead: two byte-identical card-only queries and one Go addition of
-- the card figure to the credit position. The two that stopped at the card
-- decided what the 折讓 form OFFERS, while the one that added credit decided
-- what an allowance is ALLOWED to relieve — so a split-refunded order defaulted
-- the form to the card half, and the 統一發票 went on recording a sale that was
-- reversed. An order refunded ENTIRELY from credit offered no form at all.
--
-- A view for the reason committed_orders and store_credit_balances are: the
-- rule would otherwise be copied into whichever caller was written next.
CREATE VIEW order_refunds AS
    SELECT o.id AS order_id,
           o.order_number,
           coalesce((SELECT sum(r.amount_cents)
                     FROM refunds r
                     JOIN payments p ON p.id = r.payment_id
                     WHERE p.order_id = o.id AND r.status = 'succeeded'), 0)::bigint
               AS card_cents,
           -- POSITIVE entries only: a negative one is credit SPENT on this
           -- order, which is the customer paying rather than being paid.
           coalesce((SELECT sum(e.amount_cents)
                     FROM store_credit_entries e
                     WHERE e.order_id = o.id AND e.amount_cents > 0), 0)::bigint
               AS credit_cents
    FROM orders o;

COMMENT ON VIEW order_refunds IS
    'What has gone back to the customer on one order, card and store credit '
    'separately and summed by the caller. The one definition: a 折讓 may not '
    'relieve more than this, and the form that files one offers exactly this.';

GRANT SELECT ON order_refunds TO store, admin, reporting;

-- ---------------------------------------------------------------------------
-- Co-purchase projection
--
-- Measured: computed per request this is 3 ms for a product nobody buys and
-- 136 ms for the one everybody does, because the work is proportional to that
-- product's ORDER HISTORY. An hour-old answer to "what goes with this" is the
-- same answer, which is what makes a projection right here and wrong elsewhere.
-- ---------------------------------------------------------------------------

CREATE TABLE product_copurchases (
    product_id       uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    other_product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- How many committed orders contained both.
    orders           integer NOT NULL,
    computed_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (product_id, other_product_id),
    CONSTRAINT product_copurchases_orders_positive CHECK (orders > 0),
    -- (X, X) would otherwise rank first on every page, since X appears in every
    -- order containing X.
    CONSTRAINT product_copurchases_not_self CHECK (product_id <> other_product_id)
);

COMMENT ON TABLE product_copurchases IS
    'Derived: how many committed orders contained both products. Rebuilt in '
    'full by refresh_copurchases(); never written by a request.';

CREATE INDEX product_copurchases_rank_idx
    ON product_copurchases (product_id, orders DESC);

-- The other side of the pair needs one too, and not for a query goen writes:
-- deleting a product must check every row referencing it.
CREATE INDEX product_copurchases_other_idx
    ON product_copurchases (other_product_id);

-- Rebuild the whole projection. DELETE and re-INSERT inside one transaction, so
-- a reader never sees a half-built projection; TRUNCATE would take ACCESS
-- EXCLUSIVE and block every product page for the duration.
CREATE FUNCTION refresh_copurchases() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    written integer;
BEGIN
    DELETE FROM product_copurchases;

    INSERT INTO product_copurchases (product_id, other_product_id, orders)
    SELECT ours.product_id, theirs.product_id, count(DISTINCT c.id)
    FROM committed_orders c
    JOIN order_lines mine ON mine.order_id = c.id
    JOIN order_lines other ON other.order_id = c.id
    JOIN product_variants ours ON ours.id = mine.variant_id
    JOIN product_variants theirs ON theirs.id = other.variant_id
    WHERE ours.product_id <> theirs.product_id
    GROUP BY ours.product_id, theirs.product_id;

    GET DIAGNOSTICS written = ROW_COUNT;
    RETURN written;
END;
$$;

COMMENT ON FUNCTION refresh_copurchases IS
    'Rebuilds product_copurchases from every committed order. Owned by the '
    'refresh worker in main; never called from a request.';

-- The role a BACKGROUND JOB runs as. A separate POOL and not SET ROLE on a
-- borrowed connection, and it exists because granting refresh_copurchases to
-- `store` makes a 584 ms rebuild callable from any handler.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maintenance') THEN
        CREATE ROLE maintenance NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maintenance_svc') THEN
        CREATE ROLE maintenance_svc LOGIN NOSUPERUSER IN ROLE maintenance;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO maintenance;

GRANT SELECT ON product_copurchases TO store;
GRANT SELECT ON product_copurchases TO admin;
GRANT SELECT ON product_copurchases TO reporting;
-- A projection a request could rewrite is one a request can be made to rewrite.
REVOKE INSERT, UPDATE, DELETE ON product_copurchases FROM store, admin;

-- refresh_copurchases is SECURITY DEFINER, so WHO may call it is the whole
-- control.
GRANT EXECUTE ON FUNCTION refresh_copurchases() TO maintenance;

-- ---------------------------------------------------------------------------
-- Media
--
-- Images live in PostgreSQL: goen is one binary plus one PostgreSQL, a filesystem
-- needs a volume the deployment does not have, and object storage needs a module
-- graph this repository has already refused once. Content-addressed on the
-- re-encoded bytes, so a URL is immutable and a one-year cache is safe.
-- ---------------------------------------------------------------------------

CREATE TABLE media_objects (
    -- The lowercase hex sha256 of `bytes`: the identity of an image IS its
    -- content, and a random id would let one picture exist under two URLs.
    digest       text PRIMARY KEY,
    -- The IANA type goen will serve it as: the one goen chose when it
    -- re-encoded, never the one the client claimed.
    content_type text NOT NULL,
    -- bytea, not a large object: it is read whole, it is bounded, and TOAST
    -- already stores it out of line.
    bytes        bytea NOT NULL,
    width        integer NOT NULL,
    height       integer NOT NULL,
    byte_size    integer NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),

    -- Only what goen can decode AND re-encode.
    CONSTRAINT media_objects_type_supported
        CHECK (content_type IN ('image/jpeg', 'image/png')),
    -- A zero dimension is a decode that went wrong; the ceiling stops a
    -- decompression bomb being stored after it survived decoding.
    CONSTRAINT media_objects_dimensions_sane
        CHECK (width BETWEEN 1 AND 8000 AND height BETWEEN 1 AND 8000),
    CONSTRAINT media_objects_size_positive CHECK (byte_size > 0),
    CONSTRAINT media_objects_size_matches CHECK (byte_size = octet_length(bytes)),
    -- 64 lowercase hex characters. This value reaches a URL path, which is why
    -- the handler needs no escaping.
    CONSTRAINT media_objects_digest_format CHECK (digest ~ '^[0-9a-f]{64}$')
);

COMMENT ON TABLE media_objects IS
    'Uploaded images, content-addressed by the sha256 of the re-encoded bytes.';

CREATE INDEX media_objects_created_at_idx ON media_objects (created_at DESC);

-- An image is never edited: a change produces different bytes and therefore a
-- different row. DELETE is allowed, which is how an unreferenced upload goes.
CREATE TRIGGER media_objects_immutable
    BEFORE UPDATE ON media_objects
    FOR EACH ROW EXECUTE FUNCTION forbid_change('media_objects_immutable');

GRANT SELECT ON media_objects TO store;
GRANT SELECT, INSERT, DELETE ON media_objects TO admin;
GRANT SELECT ON media_objects TO reporting;

-- ---------------------------------------------------------------------------
-- The audit trail
--
-- Per-entity history says what happened to a thing; this says WHO, which spans
-- entities. Written through a function, in the CALLER's transaction: an audit row
-- for work that rolled back is a lie, and work that commits without one is a gap.
-- ---------------------------------------------------------------------------

CREATE FUNCTION record_audit_event(
    p_actor        uuid,
    p_action       text,
    p_entity_table text,
    p_entity_id    uuid,
    p_before       jsonb DEFAULT NULL,
    p_after        jsonb DEFAULT NULL,
    p_request_id   text DEFAULT NULL
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_id uuid;
BEGIN
    -- An audit row with no actor answers nothing, and the back office is behind
    -- RequireStaff, so an absent one is a wiring mistake.
    IF p_actor IS NULL THEN
        RAISE EXCEPTION 'an audit event needs an actor'
            USING ERRCODE = 'not_null_violation', CONSTRAINT = 'audit_events_actor_required';
    END IF;

    INSERT INTO audit_events (actor_user_id, action, entity_table, entity_id,
                              before, after, request_id)
    VALUES (p_actor, p_action, p_entity_table, p_entity_id,
            p_before, p_after, p_request_id)
    RETURNING id INTO v_id;
    RETURN v_id;
END;
$$;

COMMENT ON FUNCTION record_audit_event IS
    'The only door into audit_events. Writes in the caller''s transaction, so an '
    'audit row and the work it describes commit or roll back together.';

GRANT EXECUTE ON FUNCTION record_audit_event(uuid, text, text, uuid, jsonb, jsonb, text)
    TO admin;

-- The only door into loyalty_entries: neither role holds INSERT.

-- Award the points a committed order earned. Idempotent on the order, so a
-- webhook Stripe sent twice awards once; it returns 0 for a repeat.
CREATE FUNCTION award_loyalty_points(
    p_order_id  uuid,
    p_points    bigint,
    p_expires_on date
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_account uuid;
BEGIN
    IF p_points <= 0 THEN
        RAISE EXCEPTION 'an award must be positive, got %', p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_points_nonzero';
    END IF;

    -- The account, created on first use exactly as store credit does it.
    SELECT id INTO v_account FROM store_credit_accounts
    WHERE user_id = (SELECT user_id FROM orders WHERE id = p_order_id);

    IF v_account IS NULL THEN
        INSERT INTO store_credit_accounts (user_id)
        SELECT user_id FROM orders WHERE id = p_order_id AND user_id IS NOT NULL
        ON CONFLICT (user_id) DO UPDATE SET user_id = excluded.user_id
        RETURNING id INTO v_account;
    END IF;

    -- A guest order has no account to credit, and that is not an error.
    IF v_account IS NULL THEN
        RETURN 0;
    END IF;

    INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key,
                                 order_id, expires_on)
    VALUES (v_account, p_points, 'order', 'earn:' || p_order_id::text,
            p_order_id, p_expires_on)
    ON CONFLICT (idempotency_key) DO NOTHING;

    IF NOT FOUND THEN
        RETURN 0;
    END IF;
    RETURN p_points;
END;
$$;

-- Spend points and post the store credit they bought, in ONE transaction. Two
-- statements would let the points go and the credit not arrive.
CREATE FUNCTION redeem_loyalty_points(
    p_account_id uuid,
    p_points     bigint,
    p_cents      bigint,
    p_key        text
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    IF p_points <= 0 OR p_cents <= 0 THEN
        RAISE EXCEPTION 'a redemption must be positive, got % points for %',
            p_points, p_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_points_nonzero';
    END IF;

    -- The account is locked HERE, before anything is inserted. An INSERT takes
    -- FOR KEY SHARE for the foreign key and the AFTER trigger then wants FOR
    -- UPDATE — an upgrade, and a deadlock that LOOKS like the guard working.
    PERFORM 1 FROM store_credit_accounts WHERE id = p_account_id FOR UPDATE;

    -- loyalty_never_negative locks the account and refuses an overdraw, so the
    -- balance is never read here: that would be reading it without the lock.
    INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key)
    VALUES (p_account_id, -p_points, 'redeem', p_key);

    -- The credit, in the same transaction, with a prefixed key so a redemption
    -- and an award of the same id cannot collide.
    INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
    VALUES (p_account_id, p_cents, 'points', 'redeem:' || p_key);

    RETURN p_cents;
END;
$$;

GRANT EXECUTE ON FUNCTION award_loyalty_points(uuid, bigint, date) TO store, admin;
GRANT EXECUTE ON FUNCTION redeem_loyalty_points(uuid, bigint, bigint, text) TO store, admin;

-- The privilege sweep. THIS MUST BE THE LAST THING IN THE FILE: it pins
-- search_path and revokes EXECUTE from PUBLIC on every function, so anything
-- appended below it keeps PUBLIC EXECUTE — reporting could call capture_payment.

-- The revokes below are placed after every table for the same ordering reason: a
-- REVOKE cannot name what does not exist yet, and this block names media_objects,
-- created 1,000 lines below the other store revokes.

-- The AUTHENTICATION surface, which store held in full and needs almost none of:
-- without these revokes it escalates in two statements — UPDATE users SET role,
-- DELETE the target's TOTP credential. INSERT takes the same list as UPDATE.
REVOKE INSERT, UPDATE ON users FROM store;
-- email_verified_at is INSERTABLE as well as updatable, for the identity path: an
-- account created from a provider that has already proved the address is born
-- verified.
GRANT INSERT (email, password_hash, full_name, phone, email_verified_at) ON users TO store;
GRANT UPDATE (password_hash, last_login_at, full_name, phone, email,
              email_verified_at) ON users TO store;

-- A password-equivalent guarding /admin, and nothing on the storefront pool
-- touches it in any direction. SELECT goes too: a sealed secret plus its user id
-- is half of an offline attack.
REVOKE ALL ON staff_totp_credentials FROM store;

-- A table-level INSERT includes totp_verified_at, so `store` could create a
-- session BORN step-up verified — and that is the back office's whole gate. The
-- storefront never UPDATEs a session at all.
REVOKE INSERT, UPDATE ON sessions FROM store;
GRANT INSERT (token_hash, user_id, user_agent, ip, expires_at) ON sessions TO store;

-- Merchandising is not something a storefront request does: it reads all of this
-- and writes none of it. shipping_method_versions keeps SELECT and loses INSERT,
-- because it is append-only and /admin/shipping is the only thing that appends.
REVOKE INSERT, UPDATE, DELETE ON
    products, product_images, product_specs, product_options,
    product_option_values, variant_option_values, categories, brands,
    promo_banners, hero_slides, faq_entries, membership_tiers, sale_campaigns,
    sale_campaign_products, shipping_methods, shipping_zones,
    shipping_zone_prefixes, shipping_version_zones, media_objects
    FROM store;
REVOKE INSERT ON shipping_method_versions FROM store;

-- store INSERTs a grant, reads it, and DELETEs what nobody can present any more.
-- UPDATE goes, because nothing repoints a digest. DELETE stays: the retention
-- sweep runs as `store`, and revoking it would keep the credential forever.
REVOKE UPDATE ON order_access_grants FROM store;
-- created_at ALONE. The placed-order cookie is re-issued with a fresh MaxAge on
-- every order and carries older tokens forward, so their grants must restart from
-- that event or the sweeper deletes one a live cookie still presents.
GRANT UPDATE (created_at) ON order_access_grants TO store;

-- What each role holds and no query it runs exercises, every line produced by
-- TestNoRoleHoldsAWriteItsQueriesNeverMake rather than by reading.
REVOKE INSERT, UPDATE, DELETE ON
    order_shipments, order_shipment_lines, invoice_documents,
    invoice_document_lines
    FROM store;

-- user_identities is the STOREFRONT's. INSERT and DELETE only: linking and
-- unlinking are the two things that happen to a link, and an UPDATE would repoint
-- one identity at a different account with no row to show for it.
REVOKE INSERT, UPDATE, DELETE ON user_identities FROM store;
GRANT INSERT, DELETE ON user_identities TO store;

-- admin: the storefront's own working tables are none of the back office's
-- business. password_reset_tokens is the sharpest — a staff member who could
-- insert one could mint a reset for any account.
REVOKE INSERT, UPDATE, DELETE ON
    addresses, carts, cart_items, checkout_attempts, wishlist_items,
    password_reset_tokens, order_access_grants, order_lines,
    return_request_lines, warranty_registrations, invoice_preferences,
    invoice_documents, invoice_document_lines, user_identities
    FROM admin;
REVOKE INSERT ON payment_webhook_events FROM admin;

-- Filing a document with the tax authority is the back office's act. DELETE is
-- granted for ONE row shape and invoice_documents_only_void is what holds it
-- there: a PENDING claim, which is a reservation with no number and nothing at
-- the 加值中心 under it. Releasing one is the only door out of a 折讓 the
-- provider refused, and the trigger refuses the delete of anything filed —
-- which is where that rule belongs, since it is a rule about the ROW and not
-- about who is asking.
GRANT INSERT, UPDATE, DELETE ON invoice_documents TO admin;
-- Lines are written with their document and never touched again.
GRANT INSERT ON invoice_document_lines TO admin;

-- ---------------------------------------------------------------------------
-- The DECISION columns. store must not write the SHOP's statement about what a
-- customer wrote — hidden_at, a return's resolution, an order's staff_note — and
-- admin must not write the CUSTOMER's own words or the money the checkout
-- computed. Every list is DERIVED from what the write-column guard reports.
--
-- `admin` on order_private_data and stock_notifications is deliberately ABSENT:
-- the parser cannot resolve those sets, so they stay with the table-level guard.
-- ---------------------------------------------------------------------------
REVOKE INSERT, UPDATE ON product_reviews FROM store;
GRANT INSERT (id, product_id, user_id, rating, title, body, is_verified_purchase,
              created_at),
      UPDATE (id, product_id, user_id, rating, title, body, is_verified_purchase,
              created_at)
    ON product_reviews TO store;

REVOKE INSERT, UPDATE ON product_questions FROM store;
GRANT INSERT (id, product_id, user_id, body, created_at),
      UPDATE (id, product_id, user_id, body, created_at)
    ON product_questions TO store;

REVOKE INSERT, UPDATE ON product_answers FROM store;
GRANT INSERT (id, question_id, user_id, body, is_staff, created_at),
      UPDATE (id, question_id, user_id, body, is_staff, created_at)
    ON product_answers TO store;

REVOKE INSERT, UPDATE ON return_requests FROM store;
GRANT INSERT (id, order_id, requested_by_user_id, reason, created_at),
      UPDATE (id, order_id, requested_by_user_id, reason, created_at)
    ON return_requests TO store;

-- The customer says WHAT they are sending back; the shop says what arrived. store
-- writes the claim and never the inspection.
REVOKE INSERT, UPDATE ON return_request_lines FROM store;
GRANT INSERT (order_id, return_request_id, order_line_id, quantity)
    ON return_request_lines TO store;

REVOKE INSERT, UPDATE ON orders FROM store;
GRANT INSERT (id, order_number, user_id, fulfillment_status, discount_cents,
              shipping_cents, tax_cents, shipping_version_id,
              shipping_method_code, shipping_method_name, customer_note, locale,
              placed_at, cancelled_at, updated_at),
      UPDATE (id, order_number, user_id, fulfillment_status, discount_cents,
              shipping_cents, tax_cents, shipping_version_id,
              shipping_method_code, shipping_method_name, customer_note, locale,
              placed_at, cancelled_at, updated_at)
    ON orders TO store;

REVOKE INSERT, UPDATE ON contact_messages FROM store;
GRANT INSERT (id, name, email, subject, order_ref, message, created_at),
      UPDATE (id, name, email, subject, order_ref, message, created_at)
    ON contact_messages TO store;

REVOKE INSERT, UPDATE ON stock_notifications FROM store;
GRANT INSERT (id, variant_id, user_id, email, locale, created_at),
      UPDATE (id, variant_id, user_id, email, locale, created_at)
    ON stock_notifications TO store;

REVOKE INSERT, UPDATE ON order_private_data FROM store;
GRANT INSERT (order_id, email, recipient_name, phone, postal_code, city,
              district, street, pickup_brand, pickup_store_code,
              pickup_store_name),
      UPDATE (order_id, email, recipient_name, phone, postal_code, city,
              district, street, pickup_brand, pickup_store_code,
              pickup_store_name)
    ON order_private_data TO store;

REVOKE INSERT, UPDATE ON product_reviews FROM admin;
GRANT INSERT (id, hidden_at, created_at),
      UPDATE (id, hidden_at, created_at)
    ON product_reviews TO admin;

REVOKE INSERT, UPDATE ON product_questions FROM admin;
GRANT INSERT (id, hidden_at, created_at),
      UPDATE (id, hidden_at, created_at)
    ON product_questions TO admin;

REVOKE INSERT, UPDATE ON product_answers FROM admin;
GRANT INSERT (id, question_id, user_id, body, is_staff, created_at),
      UPDATE (id, question_id, user_id, body, is_staff, created_at)
    ON product_answers TO admin;

REVOKE INSERT, UPDATE ON return_requests FROM admin;
GRANT INSERT (id, status, resolution, created_at, decided_at),
      UPDATE (id, status, resolution, created_at, decided_at)
    ON return_requests TO admin;

-- The mirror. INSERT is revoked outright rather than narrowed: a back office that
-- could add a return line could return goods on somebody's behalf and refund them
-- for it.
REVOKE INSERT, UPDATE ON return_request_lines FROM admin;
GRANT UPDATE (received_quantity, restocked_quantity, inspection_note)
    ON return_request_lines TO admin;

REVOKE INSERT, UPDATE ON orders FROM admin;
GRANT INSERT (id, fulfillment_status, staff_note, placed_at, cancelled_at,
              completed_at, updated_at),
      UPDATE (id, fulfillment_status, staff_note, placed_at, cancelled_at,
              completed_at, updated_at)
    ON orders TO admin;

REVOKE INSERT, UPDATE ON contact_messages FROM admin;
GRANT INSERT (id, handled_at, created_at),
      UPDATE (id, handled_at, created_at)
    ON contact_messages TO admin;

-- ---------------------------------------------------------------------------
-- pg_temp is searched FIRST for relations even when it is not listed, so a role
-- that may create temp tables could plant a decoy an unpinned trigger guard would
-- read. Listing pg_temp LAST is what fixes it; omitting it does NOT.
DO $$
DECLARE
    fn record;
BEGIN
    -- Every language, not just plpgsql: a LANGUAGE sql function resolves its
    -- unqualified relations exactly the same way.
    FOR fn IN
        SELECT p.oid::regprocedure AS sig
        FROM pg_proc p
        WHERE p.pronamespace = 'public'::regnamespace
          AND p.prokind IN ('f', 'p')
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = p.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE format('ALTER FUNCTION %s SET search_path = pg_catalog, public, pg_temp', fn.sig);
        EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', fn.sig);
    END LOOP;

    -- store's TEMP privilege arrives via PUBLIC, so revoking it from PUBLIC is
    -- what takes it away.
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());

    -- No role writes through a VIEW: a simple view is auto-updatable and checked
    -- against the base table as the VIEW'S OWNER, so `DELETE FROM
    -- committed_orders` as admin is accepted. Derived from pg_views.
    FOR fn IN SELECT format('%I.%I', schemaname, viewname) AS sig
              FROM pg_views WHERE schemaname = 'public'
    LOOP
        EXECUTE format(
            'REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON %s FROM store, admin, reporting',
            fn.sig);
    END LOOP;

    -- golang-migrate creates public.schema_migrations before this migration runs,
    -- so the blanket grant above hands store write access to the bookkeeping.
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'schema_migrations'
               AND relnamespace = 'public'::regnamespace) THEN
        EXECUTE 'REVOKE ALL ON schema_migrations FROM store, reporting';
    END IF;
END
$$;
