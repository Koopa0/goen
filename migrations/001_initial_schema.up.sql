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
    CONSTRAINT categories_icon_key_known CHECK (
        icon_key IS NULL OR icon_key IN (
            'phone', 'laptop', 'tablet', 'headphones', 'watch', 'plug', 'shield'
        )
    ),
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
-- Compare aligns rows by the untranslated label, which is the durable identity
-- shared across products. Two labels on one product would collapse into one
-- cell and silently discard a value at render time.
CREATE UNIQUE INDEX product_specs_label_key ON product_specs (product_id, label);
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
	CONSTRAINT users_email_bounded CHECK (octet_length(email) <= 254),
    -- No surrounding whitespace of any kind, or the folded unique index below
    -- would hold two rows for one mailbox.
    CONSTRAINT users_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
	CONSTRAINT users_full_name_bounded
		CHECK (full_name IS NULL OR char_length(full_name) <= 60),
	CONSTRAINT users_full_name_no_controls
		CHECK (full_name IS NULL OR full_name !~ '[[:cntrl:]]'),
	CONSTRAINT users_phone_bounded
		CHECK (phone IS NULL OR char_length(phone) <= 30),
	CONSTRAINT users_phone_no_controls
		CHECK (phone IS NULL OR phone !~ '[[:cntrl:]]'),
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
    CONSTRAINT user_identities_subject_present CHECK (provider_subject ~ '[^[:space:]]'),
	CONSTRAINT user_identities_subject_bounded
		CHECK (char_length(provider_subject) <= 255),
	CONSTRAINT user_identities_subject_no_controls
		CHECK (provider_subject !~ '[[:cntrl:]]'),
	CONSTRAINT user_identities_subject_trimmed
		CHECK (provider_subject !~ '^[[:space:]]|[[:space:]]$')
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
		CHECK (totp_verified_at IS NULL OR totp_verified_at >= created_at),
	CONSTRAINT sessions_user_agent_bounded
		CHECK (user_agent IS NULL OR char_length(user_agent) <= 512),
	CONSTRAINT sessions_user_agent_no_controls
		CHECK (user_agent IS NULL OR user_agent !~ '[[:cntrl:]]')
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
    CONSTRAINT addresses_street_present CHECK (street ~ '[^[:space:]]'),
    -- These are the same delivery shapes account and checkout accept. Keeping
    -- them here prevents a maintenance script from creating an address that the
    -- customer can select from the book but cannot use to place an order.
    CONSTRAINT addresses_recipient_shape CHECK (
        recipient_name !~ '[^[:space:]]'
        OR (char_length(recipient_name) <= 60
            AND recipient_name !~ '[[:cntrl:]]')
    ),
    CONSTRAINT addresses_phone_format CHECK (
        phone !~ '[^[:space:]]'
        OR (char_length(phone) <= 30
            AND phone ~ '^[-0-9+() ]+$'
            AND char_length(regexp_replace(phone, '[^0-9]', '', 'g')) BETWEEN 8 AND 15)
    ),
    CONSTRAINT addresses_postal_code_format CHECK (
        postal_code !~ '[^[:space:]]' OR postal_code ~ '^[0-9]{3,6}$'
    ),
    CONSTRAINT addresses_city_shape CHECK (
        city !~ '[^[:space:]]'
        OR (char_length(city) <= 20 AND city !~ '[[:cntrl:]]')
    ),
    CONSTRAINT addresses_district_shape CHECK (
        district !~ '[^[:space:]]'
        OR (char_length(district) <= 20 AND district !~ '[[:cntrl:]]')
    ),
    CONSTRAINT addresses_street_shape CHECK (
        street !~ '[^[:space:]]'
        OR (char_length(street) <= 200 AND street !~ '[[:cntrl:]]')
    )
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
    CONSTRAINT store_credit_entries_reason_bounded CHECK (char_length(reason) <= 200),
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
            -- Reversing a spend un-funds the order. It is legal only after the
            -- cancellation transition has durably won; allowing it on any open
            -- unpaid order gives the shared storefront role a cross-customer
            -- checkout-disruption primitive.
            IF o_status <> 'cancelled' THEN
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
    p_hold_for interval,
    p_idempotency_key text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    reservation_id uuid;
    order_status text;
BEGIN
    IF p_quantity <= 0 THEN
        RAISE EXCEPTION 'a hold must be for a positive quantity'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservations_quantity_positive';
    END IF;

    IF p_hold_for IS NULL OR p_hold_for <= interval '0' THEN
        RAISE EXCEPTION 'a hold duration must be positive'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservations_hold_for_positive';
    END IF;

    -- Every path that needs both roots takes the order before stock. In
    -- particular, a concurrent re-hold must not own the variant while a
    -- cancellation/expiry release owns the order and waits for that variant.
    -- The later reservation INSERT would acquire only a foreign-key key-share
    -- lock, which is too late to establish this order.
    SELECT fulfillment_status INTO order_status
    FROM orders WHERE id = p_order_id FOR UPDATE;
    -- Checkout is the only lifecycle that owns a hold. A settled order
    -- would keep the decrement with no session left to consume or release it.
    IF order_status <> 'pending' THEN
        RAISE EXCEPTION 'order % is % and cannot take a hold', p_order_id, order_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_hold_needs_pending';
    END IF;

    -- The idempotency key is the CALLER's: a retry of the same attempt is a
    -- no-op through the movement's unique key, while a genuinely new hold after
    -- a release passes a fresh one.
    PERFORM record_inventory_movement(
        p_variant_id, -p_quantity, 'hold',
        p_idempotency_key, 'order', p_order_id, NULL);

    -- The caller owns only the duration. `now()` is the database transaction
    -- clock, shared with orders.placed_at and every line held by this checkout.
    INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
    VALUES (p_order_id, p_variant_id, p_quantity, now() + p_hold_for)
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
    reservation_order_id uuid;
    o_status text;
BEGIN
    -- Read only the immutable parent first, then take the order lock before the
    -- reservation row. Customer/admin cancellation already holds the order;
    -- the expiry sweeper must use the same order -> reservation sequence or
    -- the two paths form an O/R deadlock that neither caller retries.
    SELECT order_id INTO reservation_order_id
    FROM inventory_reservations WHERE id = p_reservation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;

    SELECT o.fulfillment_status INTO o_status
    FROM orders o WHERE o.id = reservation_order_id FOR UPDATE;

    SELECT * INTO r FROM inventory_reservations WHERE id = p_reservation_id FOR UPDATE;
    IF NOT FOUND OR r.state <> 'held' THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;

    -- A provider-complete Session or verified money awaiting operator action is
    -- neither abandoned nor safe to sell again. Keep the stock pinned until the
    -- same order lock observes one explicit outcome: succeeded (then committed)
    -- or reconciled/cancelled after refund. Cancelled orders are excluded: their
    -- stock must return while the separate money alarm remains visible.
    IF o_status <> 'cancelled' AND (
        EXISTS (
            SELECT 1 FROM payments p
            WHERE p.order_id = reservation_order_id
              AND p.status = 'requires_reconciliation'
        ) OR EXISTS (
            SELECT 1
            FROM payment_webhook_events e
            JOIN payments p
              ON p.provider = e.provider AND p.provider_ref = e.object_ref
            WHERE p.order_id = reservation_order_id
              AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
        )
    ) THEN
        RAISE EXCEPTION 'reservation % is awaiting a payment reconciliation', p_reservation_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'inventory_reservation_payment_reconciliation_no_release';
    END IF;

    -- Multi-reservation callers enumerate variant UUIDs in ascending order.
    PERFORM 1 FROM product_variants WHERE id = r.variant_id FOR UPDATE;
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
    -- Exactly the canonical RawURL encoding of 16 bytes. The final alphabet is
    -- restricted because the last Base64 character carries only two data bits;
    -- every other value has non-zero padding bits and is a second spelling.
    CONSTRAINT checkout_attempts_key_format CHECK (
        idempotency_key ~ '^[A-Za-z0-9_-]{21}[AQgw]$'
    ),
    CONSTRAINT checkout_attempts_key_nonzero CHECK (
        idempotency_key <> 'AAAAAAAAAAAAAAAAAAAAAA'
    )
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
        WHERE o.user_id = NEW.user_id
          AND ol.product_id = NEW.product_id
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
        CHECK (max_discount_cents IS NULL OR
               (max_discount_cents > 0 AND max_discount_cents <= 10000000000)),
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
    CONSTRAINT shipping_method_versions_fee_non_negative
        CHECK (fee_cents >= 0 AND fee_cents <= 500000),
    CONSTRAINT shipping_method_versions_free_over_non_negative
        CHECK (free_over_cents IS NULL OR
               (free_over_cents >= 0 AND free_over_cents <= 10000000000))
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
        CHECK (surcharge_cents > 0 AND surcharge_cents <= 500000)
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

-- ---------------------------------------------------------------------------
-- The shop's calendar. Every deadline goen counts in DAYS is the same
-- question — 消保法 §19's seven days, a warranty term, a point's validity,
-- an order number's business date — and the session TimeZone is not an
-- answer to it: it is set by whoever built the connection string, it can
-- differ between the store pool, the admin pool, migrate, psql and
-- testcontainers, and `at::date` reads identically whichever calendar is in
-- force, so nobody reviewing a call site can tell. The zone is written HERE,
-- once, for the reason committed_orders and store_credit_balances are views.
--
-- 台灣 has kept no DST since 1979 and timezone(text, timestamptz) is itself
-- marked IMMUTABLE by PostgreSQL, so this is too.
CREATE FUNCTION shop_day(at timestamptz)
RETURNS date
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT (at AT TIME ZONE 'Asia/Taipei')::date;
$$;

COMMENT ON FUNCTION shop_day(timestamptz) IS
    'The calendar day a moment falls on for this shop. One definition: a '
    'statutory window counted in one zone and a warranty expiry written in '
    'another is the failure this prevents.';

CREATE FUNCTION shop_today()
RETURNS date
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT shop_day(now());
$$;

COMMENT ON FUNCTION shop_today() IS
    'Today, on the shop''s calendar. Replaces current_date wherever a '
    'deadline is read or written; current_date answers in the session '
    'TimeZone, which no deployment here states.';

-- Their GRANTs are far below: admin and reporting do not exist yet here, and
-- a GRANT naming a role the file has not created fails the migration.

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
    today := shop_today();

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

        -- For a zero-owed/full-credit order, this is the transition into
        -- committed_orders; a card-funded order entered when capture was
        -- accepted. Run the exact count for every pending-to-picking transition
        -- so alternate funding writers and later pending changes are covered.
        IF (SELECT count(*) FROM canonical_invoice_lines(NEW.id)) > 999 THEN
            RAISE EXCEPTION 'order % has more than 999 invoice items', NEW.order_number
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'invoice_issue_item_count';
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

-- What was bought, as it was at the moment of buying. product_id and variant_id
-- are durable catalogue identities used by verified-purchase, fulfilment,
-- returns and reporting. Catalogue retirement is a status/is_active change,
-- never deletion. Nullable identities remain available for legacy imports; the
-- display copy, price and warranty promise are independent snapshots.
CREATE TABLE order_lines (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id         uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    product_id       uuid REFERENCES products (id) ON DELETE RESTRICT,
    variant_id       uuid,
    sku              text NOT NULL,
    product_name     text NOT NULL,
    variant_label    text,
    warranty_note    text,
    warranty_months  integer,
    unit_price_cents bigint NOT NULL,
    quantity         integer NOT NULL,
    position         integer NOT NULL DEFAULT 0,
    CONSTRAINT order_lines_sku_present CHECK (sku ~ '[^[:space:]]'),
    CONSTRAINT order_lines_product_name_present CHECK (product_name ~ '[^[:space:]]'),
    CONSTRAINT order_lines_unit_price_in_range
        CHECK (unit_price_cents >= 0 AND unit_price_cents <= 10000000000),
    CONSTRAINT order_lines_quantity_in_range CHECK (quantity > 0 AND quantity <= 999),
    CONSTRAINT order_lines_warranty_months_sane
        CHECK (warranty_months IS NULL OR (warranty_months > 0 AND warranty_months <= 120)),
    CONSTRAINT order_lines_variant_has_product
        CHECK (variant_id IS NULL OR product_id IS NOT NULL),
    CONSTRAINT order_lines_variant_product_fk
        FOREIGN KEY (product_id, variant_id)
        REFERENCES product_variants (product_id, id)
        ON DELETE RESTRICT
);

-- Legacy/admin import callers historically supplied only variant_id. Bind its
-- durable product identity before the CHECK/FK run; an explicitly supplied,
-- mismatched pair is left untouched and refused by the composite FK.
CREATE FUNCTION order_lines_bind_product() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.variant_id IS NOT NULL AND NEW.product_id IS NULL THEN
        SELECT pv.product_id INTO NEW.product_id
        FROM product_variants pv
        WHERE pv.id = NEW.variant_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER order_lines_bind_product
    BEFORE INSERT ON order_lines
    FOR EACH ROW EXECUTE FUNCTION order_lines_bind_product();

CREATE UNIQUE INDEX order_lines_position_key ON order_lines (order_id, position);
-- Supports the composite FK's referencing side; its leading column also serves
-- verified-purchase lookups by durable product identity.
CREATE INDEX order_lines_product_variant_idx ON order_lines (product_id, variant_id);
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
    -- Nullable because erased and pickup rows intentionally carry no HOME
    -- destination. Whenever a value is present, however, it follows exactly
    -- the application contract used by saved addresses and checkout.
    CONSTRAINT order_private_data_recipient_shape CHECK (
        recipient_name IS NULL
        OR (recipient_name ~ '[^[:space:]]'
            AND char_length(recipient_name) <= 60
            AND recipient_name !~ '[[:cntrl:]]')
    ),
    CONSTRAINT order_private_data_phone_format CHECK (
        phone IS NULL
        OR (char_length(phone) <= 30
            AND phone ~ '^[-0-9+() ]+$'
            AND char_length(regexp_replace(phone, '[^0-9]', '', 'g')) BETWEEN 8 AND 15)
    ),
    CONSTRAINT order_private_data_postal_code_format CHECK (
        postal_code IS NULL OR postal_code ~ '^[0-9]{3,6}$'
    ),
    CONSTRAINT order_private_data_city_shape CHECK (
        city IS NULL
        OR (city ~ '[^[:space:]]'
            AND char_length(city) <= 20 AND city !~ '[[:cntrl:]]')
    ),
    CONSTRAINT order_private_data_district_shape CHECK (
        district IS NULL
        OR (district ~ '[^[:space:]]'
            AND char_length(district) <= 20 AND district !~ '[[:cntrl:]]')
    ),
    CONSTRAINT order_private_data_street_shape CHECK (
        street IS NULL
        OR (street ~ '[^[:space:]]'
            AND char_length(street) <= 200 AND street !~ '[[:cntrl:]]')
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

-- A parcel is recorded only for an order that has ENTERED fulfilment. Ship()
-- moves picking -> shipped and orders_legal_transition guards that move -- but
-- a second parcel skips the UPDATE entirely, so for every other status that
-- trigger never fires and cannot be what refuses this. The admitted set is
-- exactly fillShippable's: picking, shipped and delivered.
CREATE FUNCTION order_shipments_order_in_fulfilment() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
    o_status text;
BEGIN
    -- Lock the aggregate root before reading it: otherwise a concurrent cancel
    -- and dispatch can each pass a test the other invalidates.
    SELECT fulfillment_status INTO o_status
    FROM orders WHERE id = NEW.order_id FOR UPDATE;

    IF o_status IS DISTINCT FROM 'picking'
       AND o_status IS DISTINCT FROM 'shipped'
       AND o_status IS DISTINCT FROM 'delivered' THEN
        RAISE EXCEPTION 'order % is % and has not entered fulfilment',
            NEW.order_id, coalesce(o_status, 'missing')
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'shipment_order_in_fulfilment';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER shipment_order_in_fulfilment
    BEFORE INSERT ON order_shipments
    FOR EACH ROW EXECUTE FUNCTION order_shipments_order_in_fulfilment();

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
    -- Frozen when a request is approved. Goods includes its proportional
    -- discount; delivery is separate so one order can enforce one owner for it.
    -- Card and credit freeze the funding allocation at that SAME order lock: a
    -- failed provider attempt or later account erasure must not change which
    -- source a retry owes. Requested rows are previews and rejected rows reserve
    -- neither money nor a source.
    goods_refund_cents     bigint,
    shipping_refund_cents  bigint NOT NULL DEFAULT 0,
    card_refund_cents      bigint,
    credit_refund_cents    bigint,
    created_at         timestamptz NOT NULL DEFAULT now(),
    decided_at         timestamptz,
    CONSTRAINT return_requests_status_known
        CHECK (status IN ('requested', 'approved', 'rejected', 'completed')),
    -- A BLANK reason is legal: Consumer Protection Act §19 I lets a customer
    -- rescind within seven days without giving one, and §19 V voids any agreement
    -- otherwise. The column stays NOT NULL; '' is "none given".
    CONSTRAINT return_requests_reason_bounded CHECK (length(reason) <= 500),
    -- This becomes immutable provider-attempt reason and append-only audit
    -- evidence at approval. The browser limit is convenience; this is the
    -- authority for every writer.
    CONSTRAINT return_requests_resolution_bounded CHECK (
        resolution IS NULL OR char_length(resolution) <= 300
    ),
    CONSTRAINT return_requests_decided_has_time
        CHECK ((status = 'requested') = (decided_at IS NULL)),
    CONSTRAINT return_requests_refund_snapshot_shape CHECK (
        status NOT IN ('requested', 'approved', 'rejected', 'completed')
        OR
        (status IN ('approved', 'completed')
         AND goods_refund_cents IS NOT NULL
         AND card_refund_cents IS NOT NULL
         AND credit_refund_cents IS NOT NULL)
        OR
        (status IN ('requested', 'rejected')
         AND goods_refund_cents IS NULL
         AND shipping_refund_cents = 0
         AND card_refund_cents IS NULL
         AND credit_refund_cents IS NULL)
    ),
    CONSTRAINT return_requests_refund_snapshot_in_range CHECK (
        (goods_refund_cents IS NULL
         OR goods_refund_cents BETWEEN 0 AND 10000000000)
        AND shipping_refund_cents BETWEEN 0 AND 10000000000
        AND (card_refund_cents IS NULL
             OR card_refund_cents BETWEEN 0 AND 10000000000)
        AND (credit_refund_cents IS NULL
             OR credit_refund_cents BETWEEN 0 AND 10000000000)
    ),
    CONSTRAINT return_requests_sources_equal_refund CHECK (
        status NOT IN ('approved', 'completed')
        OR card_refund_cents + credit_refund_cents
           = goods_refund_cents + shipping_refund_cents
    )
);

CREATE INDEX return_requests_order_id_idx ON return_requests (order_id);
CREATE UNIQUE INDEX return_requests_order_key ON return_requests (order_id, id);
CREATE INDEX return_requests_requester_idx ON return_requests (requested_by_user_id);
CREATE INDEX return_requests_open_idx ON return_requests (created_at) WHERE status = 'requested';
-- A second request while one is undecided is refused HERE, not in Go.
-- returns.Open reads HasOpenReturn on the pool before its own transaction begins,
-- so two submissions can both read "none open". The partial unique key serialises
-- the competing inserts and keeps the customer and staff workflow to one
-- undecided claim per order.
CREATE UNIQUE INDEX return_requests_one_open
    ON return_requests (order_id) WHERE status = 'requested';
-- The transition trigger allocates this under the order lock. The index is the
-- final authority against any future writer which forgets that lock.
CREATE UNIQUE INDEX return_requests_shipping_refund_key
    ON return_requests (order_id) WHERE shipping_refund_cents > 0;

-- A return payout and its customer-visible timeline entry commit separately.
-- Bind that append-only event to the return so a retry can recreate a missing
-- entry without writing a duplicate. The composite foreign key also prevents
-- attributing order A's event to order B's return.
ALTER TABLE order_events
    ADD COLUMN return_request_id uuid,
    ADD CONSTRAINT order_events_return_shape CHECK (
        (kind = 'refunded') = (return_request_id IS NOT NULL)
    ),
    ADD CONSTRAINT order_events_return_request_fk
        FOREIGN KEY (order_id, return_request_id)
        REFERENCES return_requests (order_id, id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX order_events_return_request_key
    ON order_events (return_request_id)
    WHERE return_request_id IS NOT NULL;
CREATE INDEX order_events_return_request_fk_idx
    ON order_events (order_id, return_request_id);

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

-- The quantity and purchased identity are inputs to the approved money
-- snapshot. Inspection may fill received/restocked/note later, but no writer may
-- rewrite those economic inputs once the request leaves requested.
CREATE FUNCTION return_lines_require_open_request() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    v_request_id uuid;
    v_status text;
BEGIN
    IF TG_OP = 'UPDATE'
       AND NEW.order_id IS NOT DISTINCT FROM OLD.order_id
       AND NEW.return_request_id IS NOT DISTINCT FROM OLD.return_request_id
       AND NEW.order_line_id IS NOT DISTINCT FROM OLD.order_line_id
       AND NEW.quantity IS NOT DISTINCT FROM OLD.quantity THEN
        RETURN NEW;
    END IF;
    v_request_id := CASE WHEN TG_OP = 'DELETE'
                         THEN OLD.return_request_id ELSE NEW.return_request_id END;
    SELECT status INTO v_status
    FROM return_requests WHERE id = v_request_id FOR UPDATE;
    IF v_status IS DISTINCT FROM 'requested' THEN
        RAISE EXCEPTION 'return % lines are frozen after its decision', v_request_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'return_lines_frozen_after_decision';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_lines_frozen_after_decision
    BEFORE INSERT OR UPDATE OR DELETE ON return_request_lines
    FOR EACH ROW EXECUTE FUNCTION return_lines_require_open_request();

-- requested → approved | rejected, approved → completed. 'rejected' is terminal,
-- so there is deliberately no branch leaving it: this file keeps no guard that
-- cannot fire.
CREATE FUNCTION return_requests_recount() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    legal boolean;
    v_owner uuid;
    v_shipping bigint;
    v_refundable bigint;
    v_captured bigint;
    v_card_reserved bigint;
    v_other_card_refunded bigint;
    v_credit_spent bigint;
    v_credit_reserved bigint;
    v_other_credit_returned bigint;
    v_credit_expected bigint;
    v_card_expected bigint;
    v_card_paid bigint;
    v_credit_paid bigint;
    v_points_expected bigint;
BEGIN
    IF OLD.status = NEW.status THEN
        IF NEW.goods_refund_cents IS DISTINCT FROM OLD.goods_refund_cents
           OR NEW.shipping_refund_cents IS DISTINCT FROM OLD.shipping_refund_cents
           OR NEW.card_refund_cents IS DISTINCT FROM OLD.card_refund_cents
           OR NEW.credit_refund_cents IS DISTINCT FROM OLD.credit_refund_cents THEN
            RAISE EXCEPTION 'return % refund snapshot is immutable after allocation', OLD.id
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_refund_snapshot_frozen';
        END IF;
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

    IF OLD.status = 'requested' AND NEW.status = 'approved' THEN
        -- Shipping is allocated at the decision's linearization point, never by
        -- created_at/UUID order: now() is transaction-start time and neither is
        -- authority for which approval completed the rescission.
        SELECT user_id, shipping_cents INTO v_owner, v_shipping
        FROM orders WHERE id = NEW.order_id FOR UPDATE;
        NEW.goods_refund_cents := return_goods_refundable_amount(NEW.id);
        NEW.shipping_refund_cents := 0;
        IF NOT EXISTS (
            SELECT 1
            FROM order_lines ol
            WHERE ol.order_id = NEW.order_id
              AND ol.quantity > coalesce((
                  SELECT sum(rl.quantity)
                  FROM return_request_lines rl
                  JOIN return_requests rr ON rr.id = rl.return_request_id
                  WHERE rl.order_line_id = ol.id
                    AND (rr.status IN ('approved', 'completed') OR rr.id = NEW.id)
              ), 0)
        ) AND NOT EXISTS (
            SELECT 1 FROM return_requests rr
            WHERE rr.order_id = NEW.order_id AND rr.shipping_refund_cents > 0
        ) THEN
            NEW.shipping_refund_cents := v_shipping;
        END IF;

        v_refundable := NEW.goods_refund_cents + NEW.shipping_refund_cents;

        -- The capture row is the card-capacity lock used by refunds_guard. A
        -- direct/manual refund which began first commits before this allocation
        -- reads it; one which begins later waits and sees the frozen reservation.
        SELECT coalesce(p.captured_amount_cents, 0)::bigint INTO v_captured
        FROM payments p
        WHERE p.order_id = NEW.order_id AND p.status = 'succeeded'
        FOR UPDATE;
        IF NOT FOUND THEN v_captured := 0; END IF;

        SELECT coalesce(sum(rr.card_refund_cents), 0)::bigint
        INTO v_card_reserved
        FROM return_requests rr
        WHERE rr.order_id = NEW.order_id
          AND rr.id <> NEW.id
          AND rr.status IN ('approved', 'completed');
        SELECT coalesce(sum(rf.amount_cents), 0)::bigint
        INTO v_other_card_refunded
        FROM refunds rf
        JOIN payments p ON p.id = rf.payment_id
        WHERE p.order_id = NEW.order_id
          AND rf.return_request_id IS NULL
          AND rf.status IN ('pending', 'requires_action', 'succeeded');

        NEW.card_refund_cents := least(
            v_refundable,
            greatest(v_captured - v_card_reserved - v_other_card_refunded, 0)
        );

        -- A frozen credit allocation reserves the original order spend even if
        -- its idempotent ledger posting has not happened yet. Posted return
        -- credits are therefore represented by the snapshot, not counted twice
        -- as an unrelated positive entry.
        SELECT coalesce(-sum(e.amount_cents) FILTER (WHERE e.amount_cents < 0), 0)::bigint
        INTO v_credit_spent
        FROM store_credit_entries e
        WHERE e.order_id = NEW.order_id;
        SELECT coalesce(sum(rr.credit_refund_cents), 0)::bigint
        INTO v_credit_reserved
        FROM return_requests rr
        WHERE rr.order_id = NEW.order_id
          AND rr.id <> NEW.id
          AND rr.status IN ('approved', 'completed');
        SELECT coalesce(sum(e.amount_cents), 0)::bigint
        INTO v_other_credit_returned
        FROM store_credit_entries e
        WHERE e.order_id = NEW.order_id
          AND e.amount_cents > 0
          AND NOT EXISTS (
              SELECT 1 FROM return_requests rr
              WHERE rr.order_id = NEW.order_id
                AND rr.status IN ('approved', 'completed')
                AND e.idempotency_key = 'return-credit:' || rr.id::text
          );
        NEW.credit_refund_cents := least(
            greatest(v_refundable - NEW.card_refund_cents, 0),
            greatest(v_credit_spent - v_credit_reserved - v_other_credit_returned, 0)
        );
        IF NEW.card_refund_cents + NEW.credit_refund_cents <> v_refundable THEN
            RAISE EXCEPTION
                'return % requests %, but % is already returned or reserved and only % remains across its durable payment sources',
                NEW.id, v_refundable,
                v_card_reserved + v_other_card_refunded
                    + v_credit_reserved + v_other_credit_returned,
                NEW.card_refund_cents + NEW.credit_refund_cents
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_sources_cover_refund';
        END IF;
        IF NEW.credit_refund_cents > 0 AND v_owner IS NULL THEN
            RAISE EXCEPTION 'return % needs store credit but its account was erased', NEW.id
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_credit_requires_live_account';
        END IF;
    ELSIF OLD.status = 'requested' AND NEW.status = 'rejected' THEN
        NEW.goods_refund_cents := NULL;
        NEW.shipping_refund_cents := 0;
        NEW.card_refund_cents := NULL;
        NEW.credit_refund_cents := NULL;
    ELSE
        IF NEW.goods_refund_cents IS DISTINCT FROM OLD.goods_refund_cents
           OR NEW.shipping_refund_cents IS DISTINCT FROM OLD.shipping_refund_cents
           OR NEW.card_refund_cents IS DISTINCT FROM OLD.card_refund_cents
           OR NEW.credit_refund_cents IS DISTINCT FROM OLD.credit_refund_cents THEN
            RAISE EXCEPTION 'return % refund snapshot is immutable after allocation', OLD.id
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_refund_snapshot_frozen';
        END IF;
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
    IF NEW.status = 'completed' THEN
        -- Application completion takes this lock before the return row. Repeat
        -- it here so a future direct writer cannot race a payout while checking
        -- the exact frozen obligation.
        PERFORM 1 FROM orders WHERE id = NEW.order_id FOR UPDATE;
        v_refundable := coalesce(OLD.goods_refund_cents, 0) + OLD.shipping_refund_cents;
        v_credit_expected := coalesce(OLD.credit_refund_cents, 0);
        v_card_expected := coalesce(OLD.card_refund_cents, 0);
        SELECT coalesce(sum(rf.amount_cents), 0)::bigint INTO v_card_paid
        FROM refunds rf
        WHERE rf.return_request_id = NEW.id AND rf.status = 'succeeded';
        SELECT coalesce(sum(e.amount_cents), 0)::bigint INTO v_credit_paid
        FROM store_credit_entries e
        WHERE e.idempotency_key = 'return-credit:' || NEW.id::text;
        IF v_card_paid <> v_card_expected OR v_credit_paid <> v_credit_expected THEN
            RAISE EXCEPTION 'return % paid card/credit %/%, expected %/%',
                NEW.id, v_card_paid, v_credit_paid, v_card_expected, v_credit_expected
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_completed_money_settled';
        END IF;
        IF v_refundable > 0 AND NOT EXISTS (
            SELECT 1 FROM order_events e
            WHERE e.return_request_id = NEW.id AND e.kind = 'refunded'
        ) THEN
            RAISE EXCEPTION 'return % has no customer-visible refund event', NEW.id
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_completed_event_recorded';
        END IF;
        v_points_expected := return_loyalty_points_allocation(NEW.id);
        IF v_points_expected > 0 AND NOT EXISTS (
            SELECT 1 FROM loyalty_entries e
            WHERE e.return_request_id = NEW.id AND e.kind = 'clawback'
              AND e.requested_points = v_points_expected
        ) THEN
            RAISE EXCEPTION 'return % still owes a % point clawback',
                NEW.id, v_points_expected
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'return_requests_completed_points_settled';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_requests_legal_transition
    BEFORE UPDATE OF status, goods_refund_cents, shipping_refund_cents,
                     card_refund_cents, credit_refund_cents ON return_requests
    FOR EACH ROW EXECUTE FUNCTION return_requests_recount();

-- Inserting straight into 'approved' would skip the transition machine above,
-- exactly as orders_start_pending guards orders.
CREATE FUNCTION return_requests_check_initial() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    IF NEW.status <> 'requested'
       OR NEW.goods_refund_cents IS NOT NULL
       OR NEW.shipping_refund_cents <> 0
       OR NEW.card_refund_cents IS NOT NULL
       OR NEW.credit_refund_cents IS NOT NULL THEN
        RAISE EXCEPTION 'a new return request must start as an unfrozen request, not %', NEW.status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_requests_start_requested';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_requests_start_requested
    BEFORE INSERT ON return_requests
    FOR EACH ROW EXECUTE FUNCTION return_requests_check_initial();

-- A browser may pass the access check just before the order owner erases their
-- account. The order FK keeps the row alive, but only a live owner can receive
-- the store-credit half of a refund. This deferred check runs after all return
-- lines exist, so it can compare every unresolved request with the card capacity
-- none of their durable payouts has reserved. That admits a genuinely card-only
-- claim but refuses a later access-grant claim which makes store credit necessary.
CREATE FUNCTION return_credit_owner_guard() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    v_status text;
    v_owner uuid;
BEGIN
    SELECT r.status, o.user_id
    INTO v_status, v_owner
    FROM return_requests r
    JOIN orders o ON o.id = r.order_id
    WHERE r.id = NEW.id
    FOR UPDATE OF o;

    IF v_status IN ('requested', 'approved')
       AND v_owner IS NULL
       AND open_return_credit_exposure(NEW.order_id) > 0 THEN
        RAISE EXCEPTION 'return % needs store credit but its account was erased', NEW.id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'return_credit_requires_live_account';
    END IF;
    RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER return_credit_requires_live_account
    AFTER INSERT ON return_requests
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION return_credit_owner_guard();

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
-- TWO tables, because they are two things: the first is the immutable filing
-- snapshot captured with the sale; the second is what was actually issued to
-- the tax authority. Delivery PII may later be erased, but the minimum filing
-- identity, carrier and tax number remain with the tax record so a committed
-- sale can still be issued, voided/reissued and allowanced.
-- ============================================================================

-- The Ministry of Finance changed the divisor from 10 to 5 for numbers issued
-- from April 2023. When the seventh digit is 7, its 7*4 contribution may be 1
-- or 0, exactly as the current attachment's two-column example specifies.
-- https://www.fia.gov.tw/singlehtml/3?cntId=c4d9cff38c8642ef8872774ee9987283
CREATE FUNCTION valid_business_tax_id(p_value text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    v_weights integer[] := ARRAY[1, 2, 1, 2, 1, 2, 4, 1];
    v_digit integer;
    v_product integer;
    v_sum integer := 0;
    v_seventh_is_seven boolean := false;
    i integer;
BEGIN
    IF p_value IS NULL OR p_value !~ '^[0-9]{8}$' THEN
        RETURN false;
    END IF;
    FOR i IN 1..8 LOOP
        v_digit := substr(p_value, i, 1)::integer;
        IF i = 7 AND v_digit = 7 THEN
            v_sum := v_sum + 1;
            v_seventh_is_seven := true;
        ELSE
            v_product := v_digit * v_weights[i];
            v_sum := v_sum + v_product / 10 + v_product % 10;
        END IF;
    END LOOP;
    RETURN v_sum % 5 = 0
        OR (v_seventh_is_seven AND (v_sum - 1) % 5 = 0);
END;
$$;

CREATE TABLE invoice_preferences (
    order_id       uuid PRIMARY KEY REFERENCES orders (id) ON DELETE RESTRICT,
    invoice_type   text NOT NULL,
    carrier_code   text,
    tax_id         text,
    customer_name  text NOT NULL,
    customer_email text NOT NULL,
    CONSTRAINT invoice_preferences_type_known
        CHECK (invoice_type IN ('mobile_carrier', 'member_carrier', 'company')),
    CONSTRAINT invoice_preferences_company_tax_id_shape
        CHECK ((invoice_type = 'company') = (tax_id IS NOT NULL)),
    CONSTRAINT invoice_preferences_company_has_tax_id
        CHECK (tax_id IS NULL OR valid_business_tax_id(tax_id)),
    CONSTRAINT invoice_preferences_mobile_carrier_shape
        CHECK ((invoice_type = 'mobile_carrier') = (carrier_code IS NOT NULL)),
    CONSTRAINT invoice_preferences_mobile_has_carrier
        CHECK (carrier_code IS NULL OR carrier_code ~ '^/[0-9A-Z+\-.]{7}$'),
    CONSTRAINT invoice_preferences_customer_name_present
        CHECK (customer_name ~ '[^[:space:]]'),
    CONSTRAINT invoice_preferences_customer_name_bounded
        CHECK (char_length(customer_name) <= 60),
    CONSTRAINT invoice_preferences_customer_name_no_controls
        CHECK (customer_name !~ '[[:cntrl:]]'),
    CONSTRAINT invoice_preferences_customer_email_present
        CHECK (customer_email ~ '[^[:space:]]'),
    CONSTRAINT invoice_preferences_customer_email_trimmed
        CHECK (customer_email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT invoice_preferences_customer_email_bounded
        CHECK (octet_length(customer_email) <= 80)
);

CREATE TRIGGER invoice_preferences_immutable
    BEFORE UPDATE OR DELETE ON invoice_preferences
    FOR EACH ROW EXECUTE FUNCTION forbid_change('invoice_preferences_immutable');

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
    -- The durable operation that produced an allowance. NULL on an invoice,
    -- whose provider identity is its frozen RelateNumber operation instead.
    request_key  text,
    issued_at    timestamptz NOT NULL DEFAULT now(),
    voided_at    timestamptz,
    CONSTRAINT invoice_documents_kind_known CHECK (kind IN ('invoice', 'allowance')),
    CONSTRAINT invoice_documents_request_key_present
        CHECK (request_key IS NULL OR request_key ~ '[^[:space:]]'),
	CONSTRAINT invoice_documents_number_bounded CHECK (char_length(number) <= 32),
	CONSTRAINT invoice_documents_provider_ref_bounded
		CHECK (provider_ref IS NULL OR char_length(provider_ref) <= 100),
	CONSTRAINT invoice_documents_request_key_bounded
		CHECK (request_key IS NULL OR char_length(request_key) <= 100),
    CONSTRAINT invoice_documents_status_known
        CHECK (status IN ('issued', 'voided')),
    CONSTRAINT invoice_documents_number_present
        CHECK (number ~ '[^[:space:]]'),
    CONSTRAINT invoice_documents_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT invoice_documents_voided_has_time
        CHECK ((status = 'voided') = (voided_at IS NOT NULL)),
    CONSTRAINT invoice_documents_allowance_has_original
        CHECK ((kind = 'allowance') = (original_id IS NOT NULL))
    -- No self-reference CHECK: an invoice must have original_id NULL and a
    -- credit note's original must be a real invoice, so it can never fire.
);

CREATE UNIQUE INDEX invoice_documents_number_key
    ON invoice_documents (number);
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
        RAISE EXCEPTION 'invoice documents are filed, not deleted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
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

-- A provider request is durable BEFORE it leaves this database.  This is a
-- separate table rather than a synthetic invoice_documents "pending" row:
-- ECPay has not allocated a tax document yet, while the operation still needs
-- its frozen request, original staff attribution, retry evidence and lease.
CREATE TABLE invoice_operations (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id           uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    kind               text NOT NULL,
    target_document_id uuid REFERENCES invoice_documents (id) ON DELETE RESTRICT,
    result_document_id uuid REFERENCES invoice_documents (id) ON DELETE RESTRICT,
    -- RelateNumber for Issue; the immutable invoice number for Allowance/Void.
    provider_key       text NOT NULL,
    amount_cents       bigint NOT NULL,
    -- The exact request facts derived under the order/original-document lock.
    -- Callers never supply this JSON and reconciliation never re-reads mutable
    -- customer preferences to rebuild it.
    request_payload    jsonb NOT NULL,
    -- The live actor may be erased; the immutable UUID snapshot preserves tax
    -- filing attribution without making account erasure depend on tax history.
    actor_user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_id_snapshot  uuid NOT NULL,
    request_id         text NOT NULL,
    status             text NOT NULL DEFAULT 'pending',
    reconcile_attempts integer NOT NULL DEFAULT 0,
    send_attempts      integer NOT NULL DEFAULT 0,
    -- An Allowance is not provider-idempotent. Every retry after the first
    -- pre-send stamp therefore consumes one explicit operator authorization,
    -- recorded below with the staff/request identity that granted it.
    resend_authorizations integer NOT NULL DEFAULT 0,
    last_send_at       timestamptz,
    last_error         text,
    available_at       timestamptz NOT NULL DEFAULT now(),
    lease_owner        uuid,
    lease_until        timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    completed_at       timestamptz,
    CONSTRAINT invoice_operations_kind_known
        CHECK (kind IN ('issue', 'allowance', 'void')),
    CONSTRAINT invoice_operations_status_known
        CHECK (status IN ('pending', 'attention', 'succeeded', 'rejected')),
    CONSTRAINT invoice_operations_provider_key_present
        CHECK (provider_key ~ '[^[:space:]]' AND char_length(provider_key) <= 100),
    CONSTRAINT invoice_operations_issue_provider_key_safe
        CHECK (kind <> 'issue' OR provider_key ~ '^[A-Za-z0-9]{1,30}$'),
    CONSTRAINT invoice_operations_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT invoice_operations_payload_object
        CHECK (jsonb_typeof(request_payload) = 'object'),
    CONSTRAINT invoice_operations_request_present
        CHECK (request_id ~ '[^[:space:]]' AND char_length(request_id) <= 200),
    CONSTRAINT invoice_operations_actor_snapshot_matches
        CHECK (actor_user_id IS NULL OR actor_id_snapshot = actor_user_id),
    CONSTRAINT invoice_operations_attempts_non_negative
        CHECK (reconcile_attempts >= 0 AND send_attempts >= 0
               AND resend_authorizations >= 0),
    CONSTRAINT invoice_operations_resend_authority_bounded
        CHECK (resend_authorizations <= send_attempts),
    CONSTRAINT invoice_operations_resend_authority_shape
        CHECK (kind = 'allowance' OR resend_authorizations = 0),
    CONSTRAINT invoice_operations_last_send_matches_attempts
        CHECK ((send_attempts = 0) = (last_send_at IS NULL)),
    CONSTRAINT invoice_operations_error_bounded
        CHECK (last_error IS NULL OR char_length(last_error) <= 2000),
    CONSTRAINT invoice_operations_lease_complete
        CHECK ((lease_owner IS NULL) = (lease_until IS NULL)),
    CONSTRAINT invoice_operations_completion_matches_status
        CHECK ((status = 'succeeded') = (completed_at IS NOT NULL)),
    CONSTRAINT invoice_operations_result_only_on_success
        CHECK (result_document_id IS NULL OR status = 'succeeded'),
    CONSTRAINT invoice_operations_target_shape CHECK (
        (kind = 'issue' AND target_document_id IS NULL)
        OR (kind IN ('allowance', 'void') AND target_document_id IS NOT NULL)
    )
);

CREATE INDEX invoice_operations_reconcile_idx
    ON invoice_operations (available_at, created_at, id)
    WHERE status = 'pending';
CREATE INDEX invoice_operations_order_idx ON invoice_operations (order_id, created_at DESC);
CREATE INDEX invoice_operations_actor_user_id_idx ON invoice_operations (actor_user_id);
CREATE INDEX invoice_operations_target_document_id_idx
    ON invoice_operations (target_document_id);
CREATE INDEX invoice_operations_result_document_id_idx
    ON invoice_operations (result_document_id);
-- ECPay compares RelateNumber case-insensitively and never permits reuse.
CREATE UNIQUE INDEX invoice_operations_issue_provider_key_key
    ON invoice_operations (lower(provider_key)) WHERE kind = 'issue';
-- These are the database authority for "one uncertain remote effect".  An
-- attention operation remains active deliberately: a mismatch or multiple
-- provider candidates must block a fresh request, never make resending easier.
CREATE UNIQUE INDEX invoice_operations_one_active_issue
    ON invoice_operations (order_id)
    WHERE kind = 'issue' AND status IN ('pending', 'attention');
CREATE UNIQUE INDEX invoice_operations_one_active_allowance
    ON invoice_operations (target_document_id)
    WHERE kind = 'allowance' AND status IN ('pending', 'attention');
CREATE UNIQUE INDEX invoice_operations_one_active_void
    ON invoice_operations (target_document_id)
    WHERE kind = 'void' AND status IN ('pending', 'attention');

-- A credit note must relieve a real, unvoided invoice of the SAME order, and the
-- notes against it may not total more than it was for. The original is locked so
-- two cannot both pass.
CREATE FUNCTION invoice_allowance_valid() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    orig invoice_documents%ROWTYPE;
    already numeric;
    refunded numeric;
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

    -- The same original-invoice row is the aggregate lock for this second cap.
    -- Two allowances that both passed a pool-level precheck therefore cannot
    -- each consume the same refunded room.
    SELECT (card_cents::numeric + credit_cents::numeric) INTO refunded
    FROM order_refunds WHERE order_id = NEW.order_id;
    IF already + NEW.amount_cents > coalesce(refunded, 0) THEN
        RAISE EXCEPTION 'allowances would total % but only % has been refunded',
            already + NEW.amount_cents, coalesce(refunded, 0)
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_within_refund';
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
	CONSTRAINT invoice_document_lines_description_bounded CHECK (char_length(description) <= 100),
    CONSTRAINT invoice_document_lines_quantity_positive CHECK (quantity > 0),
    CONSTRAINT invoice_document_lines_amount_non_negative CHECK (amount_cents >= 0),
    -- A negative unit price would let an issued invoice be padded with a credit
    -- no note recorded. Header-equals-sum reconciliation belongs with a
    -- draft-to-issued flow and is not asked here.
    CONSTRAINT invoice_document_lines_unit_price_in_range
        CHECK (unit_price_cents >= 0 AND unit_price_cents <= 10000000000),
    CONSTRAINT invoice_document_lines_amount_in_range CHECK (amount_cents <= 10000000000),
    CONSTRAINT invoice_document_lines_tax_type_known
        CHECK (tax_type IN ('taxable', 'zero_rated', 'exempt')),
    CONSTRAINT invoice_document_lines_position_in_range
        CHECK (position BETWEEN 0 AND 998)
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
	CONSTRAINT payments_provider_ref_valid CHECK (
		char_length(provider_ref) BETWEEN 1 AND 255
		AND provider_ref !~ '[[:space:][:cntrl:]]'
	),
    -- There is deliberately no 'failed': Stripe returns a declined intent to
    -- requires_payment_method, and a terminal 'failed' would make a recoverable
    -- decline unrecoverable. requires_reconciliation is different: Stripe has
    -- already closed the Session, but a webhook/operator fact must converge
    -- before another payable identity may be opened. reconciled is that
    -- attempt's terminal, non-capture resolution; it is not provider expiry.
    CONSTRAINT payments_status_known
        CHECK (status IN ('requires_payment', 'requires_action', 'processing',
                          'requires_reconciliation', 'succeeded', 'cancelled',
                          'reconciled')),
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
-- A second provider session is a second place the customer can pay. Keep one
-- non-terminal attempt per order; replacement begins only after Stripe has
-- confirmed the old session expired and goen has cancelled its row.
CREATE UNIQUE INDEX payments_one_active_per_order
    ON payments (order_id)
    WHERE status IN ('requires_payment', 'requires_action', 'processing');

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
    IF OLD.status IN ('succeeded', 'cancelled', 'reconciled') THEN
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

-- A payment may only succeed against an order that is still complete and whose
-- stock was not returned to sale. A draft order can lose its lines between
-- orders_have_lines and payment, while a provider webhook can lag the hold
-- sweeper; this closes both windows at the moment money is taken, holding the
-- order locked.
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

    -- This is the card-funded transition into committed_orders. Count the
    -- exact canonical payload, including synthetic delivery and adjustment
    -- lines, before accepting money for a sale ECPay cannot represent.
    IF (SELECT count(*) FROM canonical_invoice_lines(o.id)) > 999 THEN
        RAISE EXCEPTION 'order % has more than 999 invoice items', o.order_number
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_issue_item_count';
    END IF;

    -- A CANCELLED order cannot be paid, and this is the only line that says so.
    -- Otherwise the two-tab sequence goes through: start a payment, cancel in the
    -- other tab, pay at Stripe, and the stock is already back on the shelf.
    IF o.fulfillment_status = 'cancelled' THEN
        RAISE EXCEPTION 'order % was cancelled and cannot be paid', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_refuse_cancelled_order';
    END IF;

    -- release_reservation and capture_payment both hold this order row, so this
    -- is the linearization point for a Session that completed near its stock
    -- deadline. If capture wins, the order becomes committed and release is
    -- refused. If the sweeper wins, its released row is durable evidence that
    -- some of the order's goods went back on sale; accepting late money would
    -- create a paid order the shop may no longer be able to fulfil. Do not pin
    -- every requires_payment row forever waiting for a possibly-lost expiry
    -- webhook. Refuse the late capture instead, keep it as an unreconciled
    -- provider event, and require a refund.
    IF EXISTS (
        SELECT 1 FROM inventory_reservations
        WHERE order_id = o.id AND state = 'released'
    ) THEN
        RAISE EXCEPTION 'order % has released stock and cannot be paid', o.order_number
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'payments_capture_refuses_released_stock';
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
    -- A hard provider failure is evidence, not a row to recycle. A retry appends
    -- the next generation and points back to the terminal attempt it follows.
    -- claim_return_refund_execution derives all three lineage facts.
    attempt_no   integer NOT NULL DEFAULT 1,
    previous_refund_id uuid,
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
	CONSTRAINT refunds_attempt_in_range CHECK (attempt_no BETWEEN 1 AND 1000000),
	CONSTRAINT refunds_attempt_lineage_shape CHECK (
		(attempt_no = 1) = (previous_refund_id IS NULL)
	),
	CONSTRAINT refunds_provider_ref_valid CHECK (
		provider_ref IS NULL OR (
			char_length(provider_ref) BETWEEN 1 AND 255
			AND provider_ref !~ '[[:space:][:cntrl:]]'
		)
	),
	CONSTRAINT refunds_provider_identity_required CHECK (
		status NOT IN ('requires_action', 'succeeded', 'cancelled')
		OR provider_ref IS NOT NULL
	),
    CONSTRAINT refunds_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT refunds_request_key_present CHECK (
        request_key ~ '[^[:space:]]' AND char_length(request_key) <= 255
    ),
    CONSTRAINT refunds_succeeded_has_time
        CHECK ((status = 'succeeded') = (succeeded_at IS NOT NULL)),
    CONSTRAINT refunds_failed_has_time
        CHECK ((status = 'failed') = (failed_at IS NOT NULL)),
    CONSTRAINT refunds_amount_in_range CHECK (amount_cents <= 10000000000),
    CONSTRAINT refunds_return_attempt_key UNIQUE (return_request_id, attempt_no),
    CONSTRAINT refunds_previous_attempt_key UNIQUE (previous_refund_id),
    CONSTRAINT refunds_return_id_key UNIQUE (return_request_id, id),
    CONSTRAINT refunds_previous_same_return
        FOREIGN KEY (return_request_id, previous_refund_id)
        REFERENCES refunds (return_request_id, id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX refunds_request_key_key ON refunds (request_key);
CREATE UNIQUE INDEX refunds_provider_ref_key ON refunds (provider_ref)
    WHERE provider_ref IS NOT NULL;
CREATE INDEX refunds_payment_id_idx ON refunds (payment_id);
CREATE INDEX refunds_return_request_idx ON refunds (return_request_id);
CREATE INDEX refunds_previous_same_return_idx
    ON refunds (return_request_id, previous_refund_id);
-- One provider identity may be ambiguous, but there is never a second attempt
-- until the latest one is known failed/cancelled. This is the final authority if
-- a future claim writer forgets the return-row lock.
CREATE UNIQUE INDEX refunds_one_open_return_attempt
    ON refunds (return_request_id)
    WHERE return_request_id IS NOT NULL
      AND status IN ('pending', 'requires_action');

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
    predecessor refunds%ROWTYPE;
BEGIN
    -- The identity of a refund is fixed once written: re-pointing it would let
    -- one capture's allowance be spent against a second.
    IF TG_OP = 'UPDATE' AND (NEW.payment_id <> OLD.payment_id
                             OR NEW.request_key <> OLD.request_key) THEN
        RAISE EXCEPTION 'a refund cannot be moved to another payment'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;
	IF TG_OP = 'UPDATE' AND (
		   NEW.return_request_id IS DISTINCT FROM OLD.return_request_id
		OR NEW.attempt_no <> OLD.attempt_no
		OR NEW.previous_refund_id IS DISTINCT FROM OLD.previous_refund_id
		OR NEW.amount_cents <> OLD.amount_cents
		OR NEW.reason IS DISTINCT FROM OLD.reason
	) THEN
		RAISE EXCEPTION 'refund % attempt identity is immutable', OLD.request_key
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_attempt_identity_immutable';
	END IF;
	IF TG_OP = 'UPDATE' AND OLD.provider_ref IS NOT NULL
	   AND NEW.provider_ref IS DISTINCT FROM OLD.provider_ref THEN
		RAISE EXCEPTION 'refund % already belongs to provider object %',
			OLD.request_key, OLD.provider_ref
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_provider_ref_immutable';
	END IF;
	IF TG_OP = 'INSERT' AND NEW.previous_refund_id IS NOT NULL THEN
		SELECT rf.* INTO predecessor
		FROM refunds rf WHERE rf.id = NEW.previous_refund_id;
		IF NOT FOUND
		   OR predecessor.return_request_id IS DISTINCT FROM NEW.return_request_id
		   OR predecessor.attempt_no + 1 <> NEW.attempt_no
		   OR predecessor.status NOT IN ('failed', 'cancelled') THEN
			RAISE EXCEPTION 'refund attempt % does not follow one terminal attempt',
				NEW.attempt_no
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'refunds_attempt_lineage';
		END IF;
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

    -- Approved returns reserve their frozen card source before a provider row is
    -- opened. Count that snapshot exactly once, and count only unrelated refund
    -- rows directly; otherwise a manual refund can consume room between approval
    -- and claim, or one return attempt is counted once as a reservation and once
    -- again as its provider row.
    SELECT
        coalesce((
            SELECT sum(rf.amount_cents)
            FROM refunds rf
            WHERE rf.payment_id = NEW.payment_id
              AND rf.return_request_id IS NULL
              AND rf.status IN ('pending', 'requires_action', 'succeeded')
              AND rf.id <> NEW.id
        ), 0)
        + coalesce((
            SELECT sum(rr.card_refund_cents)
            FROM return_requests rr
            WHERE rr.order_id = pay_order
              AND rr.status IN ('approved', 'completed')
        ), 0)
    INTO already;

    IF NEW.return_request_id IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM return_requests rr
           WHERE rr.id = NEW.return_request_id
             AND rr.status IN ('approved', 'completed')
             AND rr.card_refund_cents = NEW.amount_cents
       ) THEN
        RAISE EXCEPTION 'return refund amount % does not match its frozen card source',
            NEW.amount_cents
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'refunds_return_amount_frozen';
    END IF;
    IF NEW.return_request_id IS NULL
       AND NEW.status IN ('pending', 'requires_action', 'succeeded') THEN
        already := already + NEW.amount_cents;
    END IF;

    IF already > captured THEN
        RAISE EXCEPTION 'refunds would total % against a capture of %',
            already, captured
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
    IF TG_OP = 'DELETE' THEN
        IF OLD.return_request_id IS NOT NULL OR OLD.status = 'succeeded' THEN
            RAISE EXCEPTION 'a return/provider refund attempt is history and cannot be deleted'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_settled_is_history';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.status NOT IN ('succeeded', 'failed', 'cancelled') THEN
        RETURN NEW;
    END IF;
    IF NEW.amount_cents <> OLD.amount_cents
       OR NEW.payment_id <> OLD.payment_id
       OR NEW.request_key <> OLD.request_key
       OR NEW.return_request_id IS DISTINCT FROM OLD.return_request_id
       OR NEW.attempt_no <> OLD.attempt_no
       OR NEW.previous_refund_id IS DISTINCT FROM OLD.previous_refund_id
       OR NEW.reason IS DISTINCT FROM OLD.reason
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
    -- means "seen and refused" rather than "done". Canonical reason prefixes
    -- distinguish a known event whose object this binary could not read
    -- (unreadable_event), paid money with no local payment row to attribute it
    -- to (unattributed_capture), paid money for an order already cancelled
    -- (cancelled_order_capture), and verified money a stable local invariant
    -- refused to post (refused_capture). Each is still marked processed because
    -- Stripe would retry the same unresolvable facts; this durable reason is
    -- what makes the required human action visible on /admin/health.
    unreconciled        text,
    -- When somebody dealt with it. The alarm is monotone without this: once an
    -- event lands unreconciled, /admin/health is unhealthy forever, which is
    -- alarm fatigue on the page built to make failure visible — the objection
    -- expired_holds already carries. The row KEEPS its reason, because what
    -- happened is worth reading after it is handled; contact_messages.handled_at
    -- is the same shape.
    reconciled_at       timestamptz,
    PRIMARY KEY (provider, event_id),
	CONSTRAINT payment_webhook_events_provider_known CHECK (provider = 'stripe'),
	CONSTRAINT payment_webhook_events_event_id_valid CHECK (
		char_length(event_id) BETWEEN 1 AND 255
		AND event_id !~ '[[:space:][:cntrl:]]'
	),
	CONSTRAINT payment_webhook_events_object_ref_valid CHECK (
		object_ref IS NULL OR (
			char_length(object_ref) BETWEEN 1 AND 255
			AND object_ref !~ '[[:space:][:cntrl:]]'
		)
	),
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
    id                uuid PRIMARY KEY DEFAULT uuidv7(),
    -- The live relation makes current staff names displayable and may disappear
    -- on erasure. The non-FK snapshot is the durable answer to who acted; it is
    -- immutable because the complete audit row is append-only.
    actor_user_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_id_snapshot uuid NOT NULL,
    action            text NOT NULL,
    entity_table      text NOT NULL,
    entity_id         uuid,
    before            jsonb,
    after             jsonb,
    request_id        text,
    occurred_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_events_action_present CHECK (action ~ '[^[:space:]]'),
    CONSTRAINT audit_events_entity_present CHECK (entity_table ~ '[^[:space:]]'),
    CONSTRAINT audit_events_actor_snapshot_matches
        CHECK (actor_user_id IS NULL OR actor_user_id = actor_id_snapshot)
);

CREATE INDEX audit_events_entity_idx ON audit_events (entity_table, entity_id, occurred_at DESC);
CREATE INDEX audit_events_actor_idx ON audit_events (actor_id_snapshot, occurred_at DESC);
CREATE INDEX audit_events_actor_user_id_idx
    ON audit_events (actor_user_id) WHERE actor_user_id IS NOT NULL;

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
    CONSTRAINT membership_tiers_min_spend_non_negative
        CHECK (min_spend_cents >= 0 AND min_spend_cents <= 10000000000),
    -- A tier earning FEWER points than no tier would punish spending more.
    CONSTRAINT membership_tiers_multiplier_at_least_base
        CHECK (points_multiplier_bp >= 10000 AND points_multiplier_bp <= 30000)
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
    -- The stored value is a durable category, not free text. Keep this aligned
    -- with contact.subjects: every writer, including the store DB role, must be
    -- unable to persist a value the application cannot render or validate.
    CONSTRAINT contact_messages_subject_known CHECK (subject IN (
        '訂單問題', '退換貨', '保固維修', '商品諮詢', '合作提案'
    )),
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

-- Every path that can remove an administrator takes this transaction lock
-- before it locks a users row. The one stable key gives erase_user, the staff
-- upsert/revoke statements, and the users trigger one serialization domain.
CREATE FUNCTION lock_admin_roster() RETURNS void
LANGUAGE sql SET search_path = pg_catalog, public, pg_temp AS $$
    SELECT pg_advisory_xact_lock(hashtextextended(
        'users_admin_roster_guard', 700240291774116301::bigint));
$$;

-- This is the database invariant behind the staff page's "last admin" rule,
-- not merely a convention of its Store methods. INSERT is deliberately absent:
-- a new installation may go from zero administrators to its first one. The
-- count is made before the row changes, under the shared transaction lock.
CREATE FUNCTION users_keep_one_admin() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    PERFORM lock_admin_roster();
    IF (SELECT count(*) FROM users WHERE role = 'admin') <= 1 THEN
        RAISE EXCEPTION 'the last admin cannot lose that role'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'users_keep_one_admin';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER users_keep_one_admin_on_role
    BEFORE UPDATE OF role ON users
    FOR EACH ROW
    WHEN (OLD.role = 'admin' AND NEW.role <> 'admin')
    EXECUTE FUNCTION users_keep_one_admin();

CREATE TRIGGER users_keep_one_admin_on_delete
    BEFORE DELETE ON users
    FOR EACH ROW
    WHEN (OLD.role = 'admin')
    EXECUTE FUNCTION users_keep_one_admin();

-- Erasing an account. order_private_data keys on the ORDER and
-- stock_notifications carries a plaintext email, so a plain DELETE of a user
-- reaches neither.
CREATE FUNCTION erase_user(p_user_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    addr text;
    account_role text;
    address_verified boolean;
BEGIN
    -- Take the roster guard before the target row, matching staff role changes.
    -- This order avoids a user-row/advisory-lock cycle with a concurrent demotion.
    PERFORM lock_admin_roster();

    -- Lock and snapshot the account before reading any dependent rows. A logged-
    -- in checkout holds KEY SHARE on this same row from before it locks its cart:
    -- either its new order commits first and is included below, or this deletion
    -- wins and checkout can no longer create an order carrying fresh PII.
    -- Read the address here too: the newsletter keys on the ADDRESS, so nothing
    -- below could find it after the account goes.
    SELECT email, role, email_verified_at IS NOT NULL
    INTO addr, account_role, address_verified
    FROM users WHERE id = p_user_id FOR UPDATE;

    -- The LAST ADMIN cannot erase themselves, and this is the only place the
    -- question can be asked: /account/erase never consults the staff feature's
    -- guard, and there is no SQL recovery short of promoting somebody by hand.
    IF account_role = 'admin'
       AND (SELECT count(*) FROM users WHERE role = 'admin') <= 1 THEN
        RAISE EXCEPTION 'the last admin cannot be erased'
            USING CONSTRAINT = 'erase_user_keeps_one_admin';
    END IF;

    -- A return can be opened by an access grant as well as by the signed-in
    -- owner, so the user-row lock alone is not the complete race fence. Lock
    -- every owned order in deterministic order before deciding whether the
    -- aggregate unresolved value beyond unreserved card capacity would orphan
    -- credit that can only be posted to this account.
    PERFORM 1 FROM orders o
    WHERE o.user_id = p_user_id
    ORDER BY o.id
    FOR UPDATE OF o;

    IF EXISTS (
        SELECT 1
        FROM orders o
        WHERE o.user_id = p_user_id
          AND open_return_credit_exposure(o.id) > 0
    ) THEN
        RAISE EXCEPTION 'finish the open store-credit return before erasing this account'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'erase_user_open_return';
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

    -- Remove messages whose ownership is derived from a user-bound row before
    -- those rows cascade or lose their user_id. This remains safe even when the
    -- account never proved users.email: the reset digest, verification digest,
    -- order number and notification id are the identities of THIS account's
    -- intents, rather than claims over every message sent to that address.
    DELETE FROM outbox_messages m
    USING password_reset_tokens r
    WHERE r.user_id = p_user_id
      AND m.topic = 'account.password_reset'
      AND m.dedupe_key = 'reset:' || encode(r.token_hash, 'hex');

    DELETE FROM outbox_messages m
    USING email_verifications v
    WHERE v.user_id = p_user_id
      AND m.topic = 'account.email_verify'
      AND m.dedupe_key = 'verify:' || encode(v.digest, 'hex');

    DELETE FROM outbox_messages m
    USING orders o
    WHERE o.user_id = p_user_id
      AND m.topic IN ('order.placed', 'order.paid', 'order.shipped')
      AND coalesce(m.payload ->> 'order_number', m.payload ->> 'OrderNumber', '') =
          o.order_number;

    DELETE FROM outbox_messages m
    USING stock_notifications n
    WHERE n.user_id = p_user_id
      AND m.topic = 'catalogue.restocked'
      AND m.dedupe_key = n.id::text;

    -- The restock email is NOT NULL and cannot be blanked, so user-bound rows go
    -- after their queued messages. Address-only guest rows require verified
    -- mailbox ownership and are handled in the conditional block below.
    DELETE FROM stock_notifications WHERE user_id = p_user_id;

    -- invoice_preferences is the minimum immutable tax-filing snapshot. It is
    -- deliberately retained with the order: deleting it can make an already
    -- committed sale impossible to issue or a later refund impossible to
    -- allowance after the delivery record is erased. Remove duplicate contact
    -- data from terminal operation envelopes; pending/attention envelopes keep
    -- their exact frozen request until provider reconciliation settles them,
    -- and the settlement/rejection doors scrub those copies themselves.
    UPDATE invoice_operations op
    SET request_payload = request_payload - 'customer_name' - 'email',
        updated_at = now()
    FROM orders o
    WHERE op.order_id = o.id
      AND o.user_id = p_user_id
      AND op.status IN ('succeeded', 'rejected')
      AND (op.request_payload ? 'customer_name' OR op.request_payload ? 'email');

    -- Cross-table address ownership begins only after the mailbox is proved.
    -- Registration and a pending address change accept an arbitrary address;
    -- treating either as authority would let an attacker erase a victim's guest
    -- orders, newsletter, contact message, restock request and queued mail.
    -- For a proved current address, the newsletter confirmation goes too: a link
    -- already in the mailbox would let the erased address rejoin the list.
    IF addr IS NOT NULL AND address_verified THEN
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
        -- Match the RECIPIENT value exactly, not payload text: '%' and '_' are
        -- legal in an address but are wildcards in ILIKE, and another field
        -- (for example a customer's name) may happen to contain an address.
        -- Every current producer writes the explicit lower-case JSON tag; the
        -- legacy Go field name remains readable for rows queued by older code.
        DELETE FROM outbox_messages m
        WHERE lower(coalesce(m.payload ->> 'email', m.payload ->> 'Email', '')) =
              lower(addr);
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
-- hired, and keeps its credential; every existing session still ends, so the
-- newly promoted colleague must sign in again under the new authority.
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

-- The admin role has no direct INSERT/UPDATE privilege on users. These two
-- doors keep roster serialization, last-admin enforcement and session cleanup
-- inside the same database transaction as the role change.
CREATE FUNCTION upsert_staff(p_email text, p_full_name text, p_role text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    promoted_id uuid;
    credential_cleared boolean;
BEGIN
    PERFORM lock_admin_roster();
    INSERT INTO users (email, full_name, role)
    VALUES (p_email, nullif(p_full_name, ''), p_role)
    ON CONFLICT (lower(email)) DO UPDATE
    SET role = EXCLUDED.role,
        full_name = coalesce(nullif(EXCLUDED.full_name, ''), users.full_name)
    RETURNING id INTO promoted_id;

    credential_cleared := secure_promoted_account(promoted_id);
    RETURN credential_cleared;
END;
$$;

CREATE FUNCTION revoke_staff(p_user_id uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    target_role text;
BEGIN
    PERFORM lock_admin_roster();
    SELECT role INTO target_role FROM users WHERE id = p_user_id FOR UPDATE;
    IF NOT FOUND OR target_role NOT IN ('staff', 'admin') THEN
        RETURN false;
    END IF;
    IF target_role = 'admin'
       AND (SELECT count(*) FROM users WHERE role = 'admin') <= 1 THEN
        RETURN false;
    END IF;

    UPDATE users SET role = 'customer' WHERE id = p_user_id;
    DELETE FROM sessions WHERE user_id = p_user_id;
    RETURN true;
END;
$$;


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
    invoice_operations,
    newsletter_subscribers, outbox_messages, stock_notifications
    FROM reporting;

-- Tables whose integrity depends on going through a function; SELECT stays.
-- INSERT is revoked with UPDATE and DELETE, or store writes a born-succeeded
-- capture, or a variant carrying stock the ledger never posted.
REVOKE INSERT, UPDATE, DELETE ON
    inventory_movements, inventory_reservations, audit_events, store_credit_entries
    FROM store;
REVOKE ALL ON invoice_operations FROM store;
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
-- processed_at is bookkeeping. unreconciled is now a payment-admission gate,
-- so it is written only through mark_payment_event_unreconciled() below and
-- cannot be cleared or rewritten with the storefront role.
GRANT UPDATE (processed_at) ON payment_webhook_events TO store;
-- reconciled_at is the SHOP saying it investigated and resolved the event, so
-- it is admin's to write and not the storefront's. A whole-table INSERT would
-- carry it.
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
-- This row is the checkout-time filing snapshot, not a mutable address-book
-- preference. A correction is Void plus a new invoice, never rewriting what the
-- sale originally asked the provider to file.
REVOKE UPDATE ON invoice_preferences FROM store;


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

-- The goods half of a return: proportional discount rounded UP, so several
-- partial returns cannot sum past what the customer paid for the goods. Delivery
-- is deliberately absent and allocated only at the approval boundary below.
CREATE FUNCTION return_goods_refundable_amount(p_return_request_id uuid) RETURNS bigint
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT ret.gross
         - ceil(o.discount_cents::numeric * ret.gross::numeric
                / nullif(ord.subtotal, 0)::numeric)::bigint
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

COMMENT ON FUNCTION return_goods_refundable_amount(uuid) IS
    'The returned lines at purchased prices less their proportional discount, excluding delivery.';

-- Requested is a target-scoped preview: accepted prior returns plus THIS one.
-- Approved/completed is the immutable snapshot assigned under the order lock.
-- A rejected request never owns delivery. Approval time, not created_at or UUID
-- order, is the durable economic sequence; now() is transaction-start time.
CREATE FUNCTION return_refundable_amount(p_return_request_id uuid) RETURNS bigint
LANGUAGE sql
STABLE
PARALLEL SAFE
SET search_path = pg_catalog, public, pg_temp
AS $$
    SELECT CASE r.status
        WHEN 'approved' THEN r.goods_refund_cents + r.shipping_refund_cents
        WHEN 'completed' THEN r.goods_refund_cents + r.shipping_refund_cents
        WHEN 'requested' THEN return_goods_refundable_amount(r.id)
            + CASE WHEN NOT EXISTS (
                    SELECT 1 FROM order_lines ol
                    WHERE ol.order_id = r.order_id
                      AND ol.quantity > coalesce((
                          SELECT sum(rl.quantity)
                          FROM return_request_lines rl
                          JOIN return_requests rr ON rr.id = rl.return_request_id
                          WHERE rl.order_line_id = ol.id
                            AND (rr.status IN ('approved', 'completed') OR rr.id = r.id)
                      ), 0)
                ) AND NOT EXISTS (
                    SELECT 1 FROM return_requests accepted
                    WHERE accepted.order_id = r.order_id
                      AND accepted.shipping_refund_cents > 0
                ) THEN o.shipping_cents ELSE 0 END
        ELSE return_goods_refundable_amount(r.id)
    END::bigint
    FROM return_requests r JOIN orders o ON o.id = r.order_id
    WHERE r.id = p_return_request_id;
$$;

COMMENT ON FUNCTION return_refundable_amount(uuid) IS
    'Requested preview or frozen approved payout: discounted purchased goods plus delivery on the one approval that completed a full rescission.';

-- The amount of unresolved return value which still needs a LIVE store-credit
-- owner. Approved rows already own an immutable credit allocation; an exact
-- posting discharges it even if the card attempt later fails. Requested rows
-- compete only for card capacity left after every frozen approval and unrelated
-- refund. A per-return preview lets two partial returns each see the same room.
CREATE FUNCTION open_return_credit_exposure(p_order_id uuid)
RETURNS bigint
LANGUAGE sql STABLE
SET search_path = pg_catalog, public, pg_temp AS $$
    WITH approved AS (
        SELECT
            coalesce(sum(r.card_refund_cents), 0)::bigint AS card_reserved,
            coalesce(sum(greatest(
                r.credit_refund_cents - coalesce((
                    SELECT sum(e.amount_cents)
                    FROM store_credit_entries e
                    WHERE e.idempotency_key = 'return-credit:' || r.id::text
                ), 0),
                0
            )), 0)::bigint AS credit_unposted
        FROM return_requests r
        WHERE r.order_id = p_order_id
          AND r.status IN ('approved', 'completed')
    ), requested AS (
        SELECT coalesce(sum(return_refundable_amount(r.id)), 0)::bigint AS amount
        FROM return_requests r
        WHERE r.order_id = p_order_id AND r.status = 'requested'
    ), card AS (
        SELECT
            coalesce((
                SELECT sum(p.captured_amount_cents)
                FROM payments p
                WHERE p.order_id = p_order_id AND p.status = 'succeeded'
            ), 0)::bigint AS captured,
            coalesce((
                SELECT sum(rf.amount_cents)
                FROM refunds rf
                JOIN payments p ON p.id = rf.payment_id
                WHERE p.order_id = p_order_id
                  AND rf.return_request_id IS NULL
                  AND rf.status IN ('pending', 'requires_action', 'succeeded')
            ), 0)::bigint AS unrelated_refunded
    )
    SELECT (
        a.credit_unposted
        + greatest(
            q.amount - greatest(c.captured - a.card_reserved - c.unrelated_refunded, 0),
            0
          )
    )::bigint
    FROM approved a CROSS JOIN requested q CROSS JOIN card c;
$$;

COMMENT ON FUNCTION open_return_credit_exposure(uuid) IS
    'Unposted frozen return credit plus requested value beyond card capacity; zero means erasure cannot orphan a future credit posting.';

-- The store-credit half of an approved return is part of its decision snapshot,
-- not a value reconstructed from whichever refund attempt happens to be latest.
CREATE FUNCTION return_store_credit_allocation(p_return_request_id uuid)
RETURNS bigint
LANGUAGE sql STABLE
SET search_path = pg_catalog, public, pg_temp AS $$
    SELECT coalesce(r.credit_refund_cents, 0)::bigint
    FROM return_requests r
    WHERE r.id = p_return_request_id;
$$;

COMMENT ON FUNCTION return_store_credit_allocation(uuid) IS
    'The immutable store-credit source allocation frozen when a return was approved.';

CREATE FUNCTION order_is_committed(p_order_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM committed_orders WHERE id = p_order_id);
$$;

CREATE FUNCTION order_is_settled(p_order_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM settled_orders WHERE id = p_order_id);
$$;

-- What has gone back to the customer on one order, by source and in total.
--
-- This object intentionally lives ABOVE member_spend while its GRANT remains
-- with the role grants below. member_spend is LANGUAGE sql, so PostgreSQL
-- resolves this name when that function is created; admin does not exist yet,
-- so granting the view here would make the same migration fail for the opposite
-- ordering reason.
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

-- What a customer has spent on committed orders in a rolling window, net of
-- refunds. The floor is PER ORDER: over-compensating one purchase must not
-- erase the genuine spend on another.
-- p_exclude_order is the order being paid for RIGHT NOW: the capture commits it
-- before points are awarded, so it would otherwise raise its own tier.
CREATE FUNCTION member_spend(p_user_id uuid, p_days integer,
                             p_exclude_order uuid DEFAULT NULL)
RETURNS bigint LANGUAGE sql STABLE AS $$
    SELECT coalesce(sum(greatest(
        (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)
                   FROM order_lines ol WHERE ol.order_id = o.id), 0)
         - o.discount_cents + o.shipping_cents + o.tax_cents)
        - (rf.card_cents + rf.credit_cents), 0)), 0)::bigint
    FROM orders o
    JOIN committed_orders c ON c.id = o.id
    JOIN order_refunds rf ON rf.order_id = o.id
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
GRANT EXECUTE ON FUNCTION hold_inventory(uuid, uuid, integer, interval, text) TO store;
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
REVOKE INSERT, UPDATE, DELETE ON invoice_operations FROM admin;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM admin;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM admin;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM admin;
REVOKE UPDATE, DELETE ON store_credit_accounts FROM admin;
-- The back office issues coupons and still may not write a redemption: that row
-- is a fact about an order's money, posted by the same function the storefront
-- uses.
REVOKE INSERT, UPDATE, DELETE ON coupon_redemptions FROM admin;
-- The back office releases an event only through release_payment_event(), after
-- explicitly confirming full refund or an existing succeeded accounting. That
-- function lifts the alarm and terminates a linked live checkout in one
-- transaction; a reconciled_at column grant would create a second, unsafe door.
REVOKE UPDATE, DELETE ON payment_webhook_events FROM admin;
REVOKE UPDATE ON order_events, shipping_method_versions FROM admin;
REVOKE DELETE ON users FROM admin;
-- Verification is the customer answering a letter, and a staff member who could
-- write this table could mark any address proved.
REVOKE INSERT, UPDATE, DELETE ON email_verifications FROM admin;

-- ============================================================================
-- The back office may not become a customer.
--
-- With INSERT on sessions and UPDATE on users.password_hash, a staff member could
-- impersonate one silently, and admin holds no INSERT on audit_events. Roster
-- writes go only through upsert_staff/revoke_staff: direct column grants could
-- bypass credential neutralisation and would invert the roster/user lock order.
-- ============================================================================
REVOKE INSERT, UPDATE ON users FROM admin;

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
GRANT EXECUTE ON FUNCTION return_goods_refundable_amount(uuid) TO admin;
GRANT EXECUTE ON FUNCTION return_refundable_amount(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_is_settled(uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_spend(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_tier(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION erase_user(uuid) TO admin;
GRANT EXECUTE ON FUNCTION upsert_staff(text, text, text) TO admin;
GRANT EXECUTE ON FUNCTION revoke_staff(uuid) TO admin;

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'schema_migrations'
               AND relnamespace = 'public'::regnamespace) THEN
        EXECUTE 'REVOKE ALL ON schema_migrations FROM admin';
    END IF;
END
$$;

-- ============================================================================
-- Invoice persistence doors
--
-- The admin role may ask ECPay to file a document, but it may not write tax
-- history a column at a time.  Headers and their lines land in one call, and
-- allowance claims can only follow their small pending -> issued/released state
-- machine.  These doors constrain local authority; they do not pretend to prove
-- the remote provider fact, which still requires provider lookup/reconciliation.
-- ============================================================================

-- The exact itemisation filed at ECPay, reconstructed from the immutable order
-- snapshot.  Keeping this beside the persistence door lets that door reject a
-- role caller who supplies a different tax story that merely has the same total.
CREATE FUNCTION canonical_invoice_lines(p_order_id uuid)
RETURNS TABLE(
    description text,
    quantity integer,
    unit_price_cents bigint,
    amount_cents bigint,
    line_position integer
)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, public, pg_temp AS $$
    WITH header AS (
        SELECT o.discount_cents::numeric AS discount,
               o.shipping_cents::numeric AS shipping,
               (coalesce(sum(ol.unit_price_cents::numeric * ol.quantity), 0)
                - o.discount_cents + o.shipping_cents + o.tax_cents)::numeric AS total,
               coalesce(sum(ol.unit_price_cents::numeric * ol.quantity), 0) AS subtotal
        FROM orders o
        LEFT JOIN order_lines ol ON ol.order_id = o.id
        WHERE o.id = p_order_id
        GROUP BY o.id
    ), base AS (
        SELECT (row_number() OVER (ORDER BY ol.position, ol.id) - 1)::integer AS pos,
               left(ol.product_name || CASE
                   WHEN coalesce(ol.variant_label, '') = '' THEN ''
                   ELSE ' ' || ol.variant_label
               END, 100) AS description,
               ol.quantity,
               (ol.unit_price_cents::numeric * ol.quantity) AS gross,
               h.discount, h.subtotal, h.shipping, h.total
        FROM order_lines ol CROSS JOIN header h
        WHERE ol.order_id = p_order_id
    ), shares AS (
        SELECT b.*,
               CASE
                   WHEN discount <= 0 THEN 0::numeric
                   WHEN discount >= subtotal THEN gross
                   ELSE floor(gross * discount / subtotal)
               END AS cut,
               CASE
                   WHEN discount > 0 AND discount < subtotal
                       THEN gross * discount
                            - floor(gross * discount / subtotal) * subtotal
                   ELSE 0::numeric
               END AS remainder
        FROM base b
    ), ranked AS (
        SELECT s.*,
               row_number() OVER (ORDER BY remainder DESC, pos)::numeric AS remainder_rank,
               sum(cut) OVER () AS given_cut
        FROM shares s
    ), discounted AS (
        SELECT r.*,
               CASE
                   WHEN discount >= subtotal THEN 0::numeric
                   ELSE gross - cut - CASE
                       WHEN remainder_rank <= discount - given_cut THEN 1
                       ELSE 0
                   END
               END AS discounted_amount
        FROM ranked r
    ), snapped_items AS (
        SELECT description, quantity,
               (floor(discounted_amount / quantity / 100) * 100)::bigint
                   AS unit_price_cents,
               (floor(discounted_amount / quantity / 100) * 100 * quantity)::bigint
                   AS amount_cents,
               pos AS line_position
        FROM discounted
    ), shipping_line AS (
        SELECT '運費'::text AS description, 1::integer AS quantity,
               (floor(h.shipping / 100) * 100)::bigint AS unit_price_cents,
               (floor(h.shipping / 100) * 100)::bigint AS amount_cents,
               (SELECT count(*)::integer FROM base) AS line_position
        FROM header h WHERE h.shipping > 0
    ), before_adjustment AS (
        SELECT * FROM snapped_items
        UNION ALL
        SELECT * FROM shipping_line
    ), adjustment AS (
        SELECT '折扣尾數調整'::text AS description, 1::integer AS quantity,
               ((floor(h.total / 100) * 100)
                - coalesce(sum(b.amount_cents), 0))::bigint AS unit_price_cents,
               ((floor(h.total / 100) * 100)
                - coalesce(sum(b.amount_cents), 0))::bigint AS amount_cents,
               count(b.*)::integer AS line_position
        FROM header h LEFT JOIN before_adjustment b ON true
        GROUP BY h.total
        HAVING (floor(h.total / 100) * 100) - coalesce(sum(b.amount_cents), 0) > 0
    )
    SELECT * FROM before_adjustment
    UNION ALL
    SELECT * FROM adjustment
    ORDER BY line_position;
$$;

-- Checkout's payment trigger evaluates the canonical count as store, while the
-- company snapshot CHECK evaluates the current MOF checksum as store.
-- orders_check_transition is SECURITY INVOKER and counts the same lines on every
-- pending-to-picking move, which the back office performs, so admin needs it
-- too: without this no paid order can be picked and nothing that connects as the
-- owner can see that.
GRANT EXECUTE ON FUNCTION canonical_invoice_lines(uuid) TO store, admin;
GRANT EXECUTE ON FUNCTION valid_business_tax_id(text) TO store, admin;

-- The arrays supplied at settlement are provider evidence.  They must match the
-- DB-derived snapshot one field at a time: matching only their sum lets a shared
-- admin role forge tax itemisation while preserving the header total.
CREATE FUNCTION invoice_operation_lines_match(
    p_payload jsonb,
    p_descriptions text[],
    p_quantities integer[],
    p_unit_price_cents bigint[],
    p_amount_cents bigint[]
) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path = pg_catalog, public, pg_temp AS $$
DECLARE
    v_lines jsonb := p_payload -> 'lines';
    v_count integer;
    i integer;
BEGIN
    IF jsonb_typeof(v_lines) <> 'array' THEN RETURN false; END IF;
    v_count := jsonb_array_length(v_lines);
    IF v_count = 0 OR v_count > 999
       OR cardinality(p_descriptions) IS DISTINCT FROM v_count
       OR cardinality(p_quantities) IS DISTINCT FROM v_count
       OR cardinality(p_unit_price_cents) IS DISTINCT FROM v_count
       OR cardinality(p_amount_cents) IS DISTINCT FROM v_count THEN
        RETURN false;
    END IF;
    FOR i IN 1..v_count LOOP
        IF p_descriptions[i] IS DISTINCT FROM (v_lines -> (i - 1) ->> 'description')
           OR p_quantities[i] IS DISTINCT FROM
              ((v_lines -> (i - 1) ->> 'quantity')::integer)
           OR p_unit_price_cents[i] IS DISTINCT FROM
              ((v_lines -> (i - 1) ->> 'unit_price_cents')::bigint)
           OR p_amount_cents[i] IS DISTINCT FROM
              ((v_lines -> (i - 1) ->> 'amount_cents')::bigint) THEN
            RETURN false;
        END IF;
    END LOOP;
    RETURN true;
END;
$$;

CREATE FUNCTION claim_invoice_issue(
    p_order_number text,
    p_actor_user_id uuid,
    p_request_id text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_order orders%ROWTYPE;
    v_existing uuid;
    v_attempt integer;
    v_relate_number text;
    v_amount bigint;
    v_lines jsonb;
    v_payload jsonb;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'invoice claim requires a durable staff actor'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_actor';
    END IF;
    IF p_request_id IS NULL
       OR p_request_id !~ '[^[:space:]]'
       OR char_length(p_request_id) > 200 THEN
        RAISE EXCEPTION 'invoice claim requires a bounded request id'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_request';
    END IF;

    SELECT * INTO v_order FROM orders WHERE order_number = p_order_number FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'no order %', p_order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_issue_order';
    END IF;
    SELECT id INTO v_existing FROM invoice_operations
    WHERE order_id = v_order.id AND kind = 'issue'
      AND status IN ('pending', 'attention')
    ORDER BY created_at, id LIMIT 1;
    IF FOUND THEN RETURN v_existing; END IF;
    IF NOT order_is_committed(v_order.id) THEN
        RAISE EXCEPTION 'only a committed order can be invoiced'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_issue_committed';
    END IF;
    IF EXISTS (SELECT 1 FROM invoice_documents
               WHERE order_id = v_order.id AND kind = 'invoice' AND status = 'issued') THEN
        RAISE EXCEPTION 'order already has a live invoice'
            USING ERRCODE = 'unique_violation',
                  CONSTRAINT = 'invoice_documents_one_active_invoice_per_order';
    END IF;

    -- Every prior provider request consumes its RelateNumber, including an
    -- explicit provider rejection that produced no invoice document.
    SELECT count(*)::integer INTO v_attempt FROM invoice_operations
    WHERE order_id = v_order.id AND kind = 'issue';
    v_relate_number := replace(v_order.order_number, '-', '') ||
        CASE WHEN v_attempt = 0 THEN '' ELSE 'R' || v_attempt::text END;
    IF v_relate_number !~ '^[A-Za-z0-9]{1,30}$' THEN
        RAISE EXCEPTION 'order % cannot produce an ECPay RelateNumber',
            v_order.order_number
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_issue_relate_number';
    END IF;

    SELECT (floor(((coalesce(sum(ol.unit_price_cents::numeric * ol.quantity), 0)
                  - v_order.discount_cents + v_order.shipping_cents
                  + v_order.tax_cents) / 100)) * 100)::bigint
    INTO v_amount FROM order_lines ol WHERE ol.order_id = v_order.id;
    SELECT jsonb_agg(jsonb_build_object(
               'description', l.description, 'quantity', l.quantity,
               'unit_price_cents', l.unit_price_cents,
               'amount_cents', l.amount_cents)
               ORDER BY l.line_position)
    INTO v_lines FROM canonical_invoice_lines(v_order.id) l;
    IF v_amount <= 0 OR v_lines IS NULL
       OR jsonb_array_length(v_lines) > 999 THEN
        RAISE EXCEPTION 'invoice request has no positive authoritative itemisation'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_issue_itemisation';
    END IF;

    SELECT jsonb_build_object(
        'relate_number', v_relate_number,
        'customer_name', ip.customer_name,
        'email', ip.customer_email,
        'preference', ip.invoice_type,
        'carrier_code', coalesce(ip.carrier_code, ''),
        'tax_id', coalesce(ip.tax_id, ''),
        'amount_cents', v_amount,
        'lines', v_lines)
    INTO v_payload
    FROM orders o
    JOIN invoice_preferences ip ON ip.order_id = o.id
    WHERE o.id = v_order.id;
    IF v_payload IS NULL THEN
        RAISE EXCEPTION 'invoice filing snapshot is missing for order %', v_order.id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_issue_filing_snapshot';
    END IF;

    INSERT INTO invoice_operations
        (order_id, kind, provider_key, amount_cents, request_payload,
         actor_user_id, actor_id_snapshot, request_id)
    VALUES
        (v_order.id, 'issue', v_relate_number, v_amount, v_payload,
         p_actor_user_id, p_actor_user_id, p_request_id)
    RETURNING id INTO v_existing;
    RETURN v_existing;
END;
$$;

CREATE FUNCTION claim_invoice_allowance(
    p_original_id uuid,
    p_operation_id uuid,
    p_actor_user_id uuid,
    p_request_id text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_original invoice_documents%ROWTYPE;
    v_existing invoice_operations%ROWTYPE;
    v_already numeric;
    v_refunded numeric;
    v_amount numeric;
    v_payload jsonb;
BEGIN
    IF p_operation_id IS NULL
       OR p_operation_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
        RAISE EXCEPTION 'allowance claim requires a non-zero operation id'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_allowance_operation';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'allowance claim requires a durable staff actor'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_actor';
    END IF;
    IF p_request_id IS NULL
       OR p_request_id !~ '[^[:space:]]'
       OR char_length(p_request_id) > 200 THEN
        RAISE EXCEPTION 'allowance claim requires a bounded request id'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_request';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(
        p_operation_id::text, 390245294063994411::bigint));
    SELECT * INTO v_existing FROM invoice_operations WHERE id = p_operation_id FOR UPDATE;
    IF FOUND THEN
        IF v_existing.kind <> 'allowance'
           OR v_existing.target_document_id IS DISTINCT FROM p_original_id
           OR v_existing.order_id IS DISTINCT FROM
              (SELECT d.order_id FROM invoice_documents d WHERE d.id = p_original_id)
           OR v_existing.actor_id_snapshot <> p_actor_user_id THEN
            RAISE EXCEPTION 'allowance operation belongs to different facts or actor'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'invoice_allowance_claim_attribution';
        END IF;
        RETURN v_existing.id;
    END IF;

    SELECT * INTO v_original FROM invoice_documents
    WHERE id = p_original_id FOR UPDATE;
    IF NOT FOUND OR v_original.kind <> 'invoice' OR v_original.status <> 'issued' THEN
        RAISE EXCEPTION 'allowance original must be a live invoice'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_allowance_valid';
    END IF;
    IF EXISTS (SELECT 1 FROM invoice_operations
               WHERE target_document_id = p_original_id AND kind = 'allowance'
                 AND status IN ('pending', 'attention')) THEN
        RAISE EXCEPTION 'an allowance for this invoice is already unresolved'
            USING ERRCODE = 'unique_violation',
                  CONSTRAINT = 'invoice_operations_one_active_allowance';
    END IF;
    SELECT coalesce(sum(amount_cents), 0) INTO v_already
    FROM invoice_documents
    WHERE original_id = p_original_id AND status <> 'voided';
    SELECT (card_cents::numeric + credit_cents::numeric) INTO v_refunded
    FROM order_refunds WHERE order_id = v_original.order_id;
    -- ECPay files whole NT dollars. Derive the cumulative tax relief from
    -- settled refunds, cap it at the rounded original invoice, then subtract
    -- documents already filed. A later refund can expose another exact delta;
    -- no browser or operator chooses any part of this amount.
    v_amount := floor(least(
        v_original.amount_cents::numeric,
        coalesce(v_refunded, 0)
    ) / 100) * 100 - v_already;
    IF v_amount <= 0 THEN
        RAISE EXCEPTION 'no authoritative whole-dollar refunded room remains'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_within_refund';
    END IF;

    SELECT jsonb_build_object(
        'invoice_number', v_original.number,
        'invoice_date', to_char(v_original.issued_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'),
        'customer_name', ip.customer_name,
        'email', ip.customer_email,
        'amount_cents', v_amount,
        'lines', jsonb_build_array(jsonb_build_object(
            'description', '退貨折讓', 'quantity', 1,
            'unit_price_cents', v_amount, 'amount_cents', v_amount)))
    INTO v_payload
    FROM orders o JOIN invoice_preferences ip ON ip.order_id = o.id
    WHERE o.id = v_original.order_id;
    IF v_payload IS NULL THEN
        RAISE EXCEPTION 'invoice filing snapshot is missing for order %', v_original.order_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_filing_snapshot';
    END IF;

    INSERT INTO invoice_operations
        (id, order_id, kind, target_document_id, provider_key, amount_cents,
         request_payload, actor_user_id, actor_id_snapshot, request_id)
    VALUES
        (p_operation_id, v_original.order_id, 'allowance', p_original_id,
         v_original.number, v_amount, v_payload,
         p_actor_user_id, p_actor_user_id, p_request_id);
    RETURN p_operation_id;
END;
$$;

CREATE FUNCTION claim_invoice_void(
    p_document_id uuid,
    p_reason text,
    p_actor_user_id uuid,
    p_request_id text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_document invoice_documents%ROWTYPE;
    v_existing uuid;
    v_lines jsonb;
    v_relate_number text;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'void claim requires a durable staff actor'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_actor';
    END IF;
    IF p_request_id IS NULL
       OR p_request_id !~ '[^[:space:]]'
       OR char_length(p_request_id) > 200 THEN
        RAISE EXCEPTION 'void claim requires a bounded request id'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_audit_request';
    END IF;
    IF btrim(coalesce(p_reason, '')) = '' THEN
        RAISE EXCEPTION 'voiding an invoice requires a reason'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_void_reason';
    END IF;

    SELECT * INTO v_document FROM invoice_documents WHERE id = p_document_id FOR UPDATE;
    IF NOT FOUND OR v_document.kind <> 'invoice' THEN
        RAISE EXCEPTION 'void target is not an invoice'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_void_target';
    END IF;
    SELECT id INTO v_existing FROM invoice_operations
    WHERE target_document_id = p_document_id AND kind = 'void'
      AND status IN ('pending', 'attention')
    ORDER BY created_at, id LIMIT 1;
    IF FOUND THEN RETURN v_existing; END IF;
    IF v_document.status <> 'issued' THEN
        RAISE EXCEPTION 'invoice is already voided'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_void_target';
    END IF;
    SELECT jsonb_agg(jsonb_build_object(
               'description', l.description, 'quantity', l.quantity,
               'unit_price_cents', l.unit_price_cents,
               'amount_cents', l.amount_cents)
               ORDER BY l.position)
    INTO v_lines FROM invoice_document_lines l WHERE l.document_id = p_document_id;
    SELECT provider_key INTO v_relate_number FROM invoice_operations
    WHERE kind = 'issue' AND status = 'succeeded'
      AND result_document_id = p_document_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'invoice has no durable Issue identity'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_void_issue_operation';
    END IF;

    INSERT INTO invoice_operations
        (order_id, kind, target_document_id, provider_key, amount_cents,
         request_payload, actor_user_id, actor_id_snapshot, request_id)
    VALUES
        (v_document.order_id, 'void', p_document_id, v_document.number,
         v_document.amount_cents,
         jsonb_build_object(
             'invoice_number', v_document.number,
             'relate_number', v_relate_number,
             'invoice_date', to_char(v_document.issued_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'),
             'random_number', coalesce(v_document.provider_ref, ''),
             'reason', left(btrim(p_reason), 20),
             'amount_cents', v_document.amount_cents,
             'lines', coalesce(v_lines, '[]'::jsonb)),
         p_actor_user_id, p_actor_user_id, p_request_id)
    RETURNING id INTO v_existing;
    RETURN v_existing;
END;
$$;

-- A DB-clock lease is shared by request handlers and every process running the
-- background reconciler.  uuid.Nil means "oldest eligible operation" and a
-- uuid.Nil result means another worker owns it or there is no work.
CREATE FUNCTION lease_invoice_operation(
    p_operation_id uuid,
    p_owner uuid,
    p_lease_for interval
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_id uuid;
BEGIN
    IF p_operation_id IS NULL
       OR p_owner IS NULL
       OR p_owner = '00000000-0000-0000-0000-000000000000'::uuid
       OR p_lease_for IS NULL
       OR p_lease_for <= interval '0'
       OR p_lease_for > interval '10 minutes' THEN
        RAISE EXCEPTION 'invoice operation lease is invalid'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_operation_lease';
    END IF;
    SELECT id INTO v_id FROM invoice_operations
    WHERE status = 'pending' AND available_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())
      AND (p_operation_id = '00000000-0000-0000-0000-000000000000'::uuid
           OR id = p_operation_id)
    ORDER BY available_at, created_at, id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN '00000000-0000-0000-0000-000000000000'::uuid; END IF;
    UPDATE invoice_operations
    SET lease_owner = p_owner, lease_until = now() + p_lease_for,
        reconcile_attempts = reconcile_attempts + 1, updated_at = now()
    WHERE id = v_id;
    RETURN v_id;
END;
$$;

CREATE FUNCTION mark_invoice_operation_sent(p_id uuid, p_owner uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE invoice_operations
    SET send_attempts = send_attempts + 1, last_send_at = now(), updated_at = now()
    WHERE id = p_id AND status = 'pending' AND lease_owner = p_owner
      AND lease_until > now()
      -- Issue is keyed by a provider-safe RelateNumber and Void is an exact
      -- document operation. Allowance has no idempotency key: after its first
      -- send, SQL itself requires one fresh audited authorization per resend.
      AND (kind <> 'allowance' OR send_attempts <= resend_authorizations);
    RETURN FOUND;
END;
$$;

-- ECPay's Allowance API has neither an idempotency key nor a query by our own
-- request identity. An empty list after a marked send can be propagation lag or
-- a request that never reached ECPay, so the worker must not guess. Only after a
-- staff member independently checks ECPay, waits out the ordinary propagation
-- window, and confirms the allowance is absent may exactly one new send occur.
CREATE FUNCTION authorize_invoice_allowance_resend(
    p_operation_id uuid,
    p_actor_user_id uuid,
    p_request_id text
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_before integer;
    v_after integer;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'allowance resend authorization requires a durable staff actor'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_resend_actor';
    END IF;
    IF p_request_id IS NULL
       OR p_request_id !~ '[^[:space:]]'
       OR char_length(p_request_id) > 200 THEN
        RAISE EXCEPTION 'allowance resend authorization requires a bounded request id'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_resend_request';
    END IF;

    SELECT resend_authorizations INTO v_before
    FROM invoice_operations
    WHERE id = p_operation_id
      AND kind = 'allowance'
      AND status = 'pending'
      AND send_attempts > resend_authorizations
      AND last_error = 'allowance_not_yet_visible'
      AND last_send_at IS NOT NULL
      AND last_send_at <= now() - interval '15 minutes'
      AND (lease_until IS NULL OR lease_until <= now())
    FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;

    v_after := v_before + 1;
    UPDATE invoice_operations
    SET resend_authorizations = v_after,
        last_error = 'allowance_resend_authorized',
        available_at = now(),
        lease_owner = NULL,
        lease_until = NULL,
        updated_at = now()
    WHERE id = p_operation_id;

    INSERT INTO audit_events
        (actor_user_id, actor_id_snapshot, action, entity_table, entity_id,
         before, after, request_id)
    VALUES
        (p_actor_user_id, p_actor_user_id,
         'invoice.allowance_resend_authorized', 'invoice_operations', p_operation_id,
         jsonb_build_object('resend_authorizations', v_before),
         jsonb_build_object('resend_authorizations', v_after), p_request_id);
    RETURN true;
END;
$$;

CREATE FUNCTION reschedule_invoice_operation(
    p_id uuid, p_owner uuid, p_error text, p_backoff interval
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    IF p_backoff IS NULL
       OR p_backoff < interval '0'
       OR p_backoff > interval '1 day' THEN
        RAISE EXCEPTION 'invoice reconciliation backoff is invalid'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_operation_backoff';
    END IF;
    UPDATE invoice_operations
    SET last_error = left(nullif(p_error, ''), 2000), available_at = now() + p_backoff,
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = p_id AND status = 'pending' AND lease_owner = p_owner;
    RETURN FOUND;
END;
$$;

-- GetAllowanceList can authoritatively report that an allowance which was
-- previously settled here is now invalid. Only a different, still-unsent
-- allowance operation may absorb that provider-status change: once its send
-- stamp exists, changing its amount would make an ambiguous remote effect
-- impossible to identify safely.
--
-- The provider facts are repeated at this door even though the Go reconciler
-- already compared them. This keeps a stale read or a caller using the shared
-- admin role from voiding tax history after any header/line fact changed. The
-- document rows themselves are immutable, but status can move one way to
-- voided, so both the operation and document are locked in the same transaction.
CREATE FUNCTION reconcile_invalid_invoice_allowance(
    p_operation_id uuid,
    p_owner uuid,
    p_document_id uuid,
    p_invoice_number text,
    p_allowance_number text,
    p_issued_at timestamptz,
    p_amount_cents bigint,
    p_descriptions text[],
    p_quantities integer[],
    p_unit_price_cents bigint[],
    p_line_amount_cents bigint[]
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_original invoice_documents%ROWTYPE;
    v_document invoice_documents%ROWTYPE;
    v_local_lines jsonb;
    v_already numeric;
    v_refunded numeric;
    v_amount bigint;
    v_order_number text;
BEGIN
    SELECT * INTO v_operation FROM invoice_operations
    WHERE id = p_operation_id AND kind = 'allowance' AND status = 'pending'
      AND send_attempts = 0 AND lease_owner = p_owner AND lease_until > now()
    FOR UPDATE;
    IF NOT FOUND THEN RETURN 0; END IF;

    SELECT * INTO v_original FROM invoice_documents
    WHERE id = v_operation.target_document_id FOR UPDATE;
    IF NOT FOUND OR v_original.kind <> 'invoice' OR v_original.status <> 'issued'
       OR v_original.order_id <> v_operation.order_id
       OR v_original.number <> v_operation.provider_key
       OR v_original.number <> p_invoice_number THEN
        RAISE EXCEPTION 'allowance invalidation has a different original invoice'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_original';
    END IF;

    SELECT * INTO v_document FROM invoice_documents
    WHERE id = p_document_id FOR UPDATE;
    IF NOT FOUND OR v_document.kind <> 'allowance'
       OR v_document.original_id IS DISTINCT FROM v_original.id
       OR v_document.order_id <> v_operation.order_id
       OR v_document.status <> 'issued'
       OR v_document.number <> p_allowance_number
       OR v_document.amount_cents <> p_amount_cents
       OR v_document.issued_at <> p_issued_at THEN
        RAISE EXCEPTION 'provider invalidation differs from the issued allowance header'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_header';
    END IF;

    SELECT coalesce(jsonb_agg(jsonb_build_object(
               'description', l.description, 'quantity', l.quantity,
               'unit_price_cents', l.unit_price_cents,
               'amount_cents', l.amount_cents)
               ORDER BY l.position), '[]'::jsonb)
    INTO v_local_lines
    FROM invoice_document_lines l
    WHERE l.document_id = v_document.id AND l.tax_type = 'taxable';
    IF NOT invoice_operation_lines_match(
            jsonb_build_object('lines', v_local_lines),
            p_descriptions, p_quantities, p_unit_price_cents,
            p_line_amount_cents)
       OR EXISTS (SELECT 1 FROM invoice_document_lines
                  WHERE document_id = v_document.id AND tax_type <> 'taxable') THEN
        RAISE EXCEPTION 'provider invalidation differs from the issued allowance lines'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_lines';
    END IF;

    UPDATE invoice_documents
    SET status = 'voided', voided_at = now()
    WHERE id = v_document.id;

    SELECT coalesce(sum(amount_cents), 0) INTO v_already
    FROM invoice_documents
    WHERE original_id = v_original.id AND status <> 'voided';
    SELECT (card_cents::numeric + credit_cents::numeric) INTO v_refunded
    FROM order_refunds WHERE order_id = v_operation.order_id;
    v_amount := (floor(least(
        v_original.amount_cents::numeric,
        coalesce(v_refunded, 0)
    ) / 100) * 100 - v_already)::bigint;
    IF v_amount <= 0 THEN
        RAISE EXCEPTION 'provider invalidation left no authoritative replacement amount'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_amount';
    END IF;

    SELECT order_number INTO v_order_number
    FROM orders WHERE id = v_operation.order_id;
    INSERT INTO audit_events
        (actor_user_id, actor_id_snapshot, action, entity_table, entity_id,
         before, after, request_id)
    VALUES
        (v_operation.actor_user_id, v_operation.actor_id_snapshot,
         'invoice.allowance_provider_invalid', 'invoice_documents', v_document.id,
         jsonb_build_object(
             'order', v_order_number, 'allowance', v_document.number,
             'status', 'issued', 'amount_cents', v_document.amount_cents),
         jsonb_build_object(
             'order', v_order_number, 'allowance', v_document.number,
             'status', 'voided', 'provider_status', 'invalid',
             'replacement_operation', v_operation.id,
             'refrozen_amount_cents', v_amount),
         v_operation.request_id);

    UPDATE invoice_operations
    SET amount_cents = v_amount,
        request_payload = jsonb_set(
            jsonb_set(request_payload, '{amount_cents}', to_jsonb(v_amount)),
            '{lines}', jsonb_build_array(jsonb_build_object(
                'description', '退貨折讓', 'quantity', 1,
                'unit_price_cents', v_amount, 'amount_cents', v_amount))),
        last_error = 'allowance_provider_invalid_refrozen',
        available_at = now(), lease_owner = NULL, lease_until = NULL,
        updated_at = now()
    WHERE id = v_operation.id;
    RETURN v_amount;
END;
$$;

-- The other invalid-provider state has no local document yet: ECPay accepted a
-- stamped Allowance, this process lost the success before settlement, and the
-- provider document was later invalidated. It is unsafe either to discard that
-- history or to settle it as active. Record the exact frozen provider document
-- already voided, then reject this operation so a newly attributed claim can
-- derive and file the amount which remains unrelieved.
CREATE FUNCTION record_invalid_invoice_allowance(
    p_operation_id uuid,
    p_owner uuid,
    p_invoice_number text,
    p_allowance_number text,
    p_issued_at timestamptz,
    p_amount_cents bigint,
    p_descriptions text[],
    p_quantities integer[],
    p_unit_price_cents bigint[],
    p_line_amount_cents bigint[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_original invoice_documents%ROWTYPE;
    v_document_id uuid;
    v_order_number text;
    i integer;
BEGIN
    SELECT * INTO v_operation FROM invoice_operations
    WHERE id = p_operation_id AND kind = 'allowance' AND status = 'pending'
      AND send_attempts > 0 AND last_send_at IS NOT NULL
      AND lease_owner = p_owner AND lease_until > now()
    FOR UPDATE;
    IF NOT FOUND THEN RETURN '00000000-0000-0000-0000-000000000000'::uuid; END IF;

    SELECT * INTO v_original FROM invoice_documents
    WHERE id = v_operation.target_document_id FOR UPDATE;
    IF NOT FOUND OR v_original.kind <> 'invoice' OR v_original.status <> 'issued'
       OR v_original.order_id <> v_operation.order_id
       OR v_original.number <> v_operation.provider_key
       OR v_original.number <> p_invoice_number THEN
        RAISE EXCEPTION 'invalid provider allowance has a different original invoice'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_original';
    END IF;
    IF p_allowance_number !~ '^[0-9]{16}$' OR p_issued_at IS NULL
       OR p_amount_cents <> v_operation.amount_cents THEN
        RAISE EXCEPTION 'invalid provider allowance differs from the frozen header'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_header';
    END IF;
    IF NOT invoice_operation_lines_match(
            v_operation.request_payload, p_descriptions, p_quantities,
            p_unit_price_cents, p_line_amount_cents) THEN
        RAISE EXCEPTION 'invalid provider allowance differs from the frozen lines'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_invalidation_lines';
    END IF;

    INSERT INTO invoice_documents
        (order_id, kind, original_id, number, amount_cents, status,
         request_key, issued_at, voided_at)
    VALUES
        (v_operation.order_id, 'allowance', v_operation.target_document_id,
         p_allowance_number, v_operation.amount_cents, 'voided',
         'allowance:' || v_operation.id::text, p_issued_at, now())
    RETURNING id INTO v_document_id;
    FOR i IN 1..cardinality(p_descriptions) LOOP
        INSERT INTO invoice_document_lines
            (document_id, description, quantity, unit_price_cents,
             amount_cents, tax_type, position)
        VALUES
            (v_document_id, p_descriptions[i], p_quantities[i],
             p_unit_price_cents[i], p_line_amount_cents[i], 'taxable', i - 1);
    END LOOP;

    SELECT order_number INTO v_order_number
    FROM orders WHERE id = v_operation.order_id;
    INSERT INTO audit_events
        (actor_user_id, actor_id_snapshot, action, entity_table, entity_id,
         before, after, request_id)
    VALUES
        (v_operation.actor_user_id, v_operation.actor_id_snapshot,
         'invoice.allowance_provider_invalid', 'invoice_documents', v_document_id,
         NULL,
         jsonb_build_object(
             'order', v_order_number, 'allowance', p_allowance_number,
             'status', 'voided', 'provider_status', 'invalid',
             'amount_cents', v_operation.amount_cents,
             'operation', v_operation.id),
         v_operation.request_id);
    UPDATE invoice_operations
    SET status = 'rejected', result_document_id = NULL,
        request_payload = request_payload - 'customer_name' - 'email',
        last_error = 'allowance_provider_invalid',
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = v_operation.id;
    RETURN v_document_id;
END;
$$;

CREATE FUNCTION alarm_invoice_operation(p_id uuid, p_owner uuid, p_error text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE invoice_operations
    SET status = 'attention', last_error = left(nullif(p_error, ''), 2000),
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = p_id AND status = 'pending' AND lease_owner = p_owner;
    RETURN FOUND;
END;
$$;

CREATE FUNCTION reject_invoice_operation(p_id uuid, p_owner uuid, p_error text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE invoice_operations
    SET status = 'rejected', last_error = left(nullif(p_error, ''), 2000),
        request_payload = request_payload - 'customer_name' - 'email',
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = p_id AND status = 'pending' AND lease_owner = p_owner;
    RETURN FOUND;
END;
$$;

-- Settlement may finish after the filing employee has been erased. Derive both
-- forms of attribution from the durable operation: the live FK is nullable, but
-- the UUID snapshot and request identity cannot be supplied by a reconciler.
-- This helper is deliberately ungranted; the final privilege sweep also removes
-- PUBLIC EXECUTE, and only the three settlement doors below call it.
CREATE FUNCTION record_invoice_operation_audit(
    p_operation_id uuid,
    p_entity_id uuid,
    p_after jsonb
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_audit_id uuid;
BEGIN
    SELECT * INTO v_operation
    FROM invoice_operations
    WHERE id = p_operation_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'no invoice operation % to audit', p_operation_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_audit_operation';
    END IF;

    INSERT INTO audit_events
        (actor_user_id, actor_id_snapshot, action, entity_table, entity_id,
         before, after, request_id)
    VALUES
        (v_operation.actor_user_id, v_operation.actor_id_snapshot,
         'invoice.' || v_operation.kind, 'invoice_documents', p_entity_id,
         NULL, p_after, v_operation.request_id)
    RETURNING id INTO v_audit_id;
    RETURN v_audit_id;
END;
$$;

CREATE FUNCTION settle_invoice_issue(
    p_operation_id uuid,
    p_owner uuid,
    p_number text,
    p_random_number text,
    p_issued_at timestamptz,
    p_descriptions text[],
    p_quantities integer[],
    p_unit_price_cents bigint[],
    p_amount_cents bigint[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_document_id uuid;
    v_order_number text;
    i integer;
BEGIN
    SELECT * INTO v_operation FROM invoice_operations
    WHERE id = p_operation_id AND kind = 'issue' AND status = 'pending'
      AND lease_owner = p_owner AND lease_until > now() FOR UPDATE;
    IF NOT FOUND THEN RETURN '00000000-0000-0000-0000-000000000000'::uuid; END IF;
    IF p_number !~ '^[A-Z]{2}[0-9]{8}$' OR p_random_number !~ '^[0-9]{4}$'
       OR p_issued_at IS NULL THEN
        RAISE EXCEPTION 'provider invoice identity is malformed'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_issue_provider_identity';
    END IF;
    IF NOT invoice_operation_lines_match(v_operation.request_payload,
            p_descriptions, p_quantities, p_unit_price_cents, p_amount_cents) THEN
        RAISE EXCEPTION 'invoice lines differ from the frozen authoritative request'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_lines_authoritative';
    END IF;

    INSERT INTO invoice_documents
        (order_id, kind, number, amount_cents, provider_ref, issued_at)
    VALUES
        (v_operation.order_id, 'invoice', p_number, v_operation.amount_cents,
         p_random_number, p_issued_at)
    RETURNING id INTO v_document_id;
    FOR i IN 1..cardinality(p_descriptions) LOOP
        INSERT INTO invoice_document_lines
            (document_id, description, quantity, unit_price_cents,
             amount_cents, tax_type, position)
        VALUES
            (v_document_id, p_descriptions[i], p_quantities[i],
             p_unit_price_cents[i], p_amount_cents[i], 'taxable', i - 1);
    END LOOP;
    SELECT order_number INTO v_order_number FROM orders WHERE id = v_operation.order_id;
    PERFORM record_invoice_operation_audit(
        v_operation.id, v_document_id,
        jsonb_build_object('order', v_order_number, 'invoice', p_number,
                           'operation', v_operation.id));
    UPDATE invoice_operations
    SET status = 'succeeded', result_document_id = v_document_id,
        request_payload = request_payload - 'customer_name' - 'email',
        completed_at = now(), last_error = NULL,
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = v_operation.id;
    RETURN v_document_id;
END;
$$;

CREATE FUNCTION settle_invoice_allowance(
    p_operation_id uuid,
    p_owner uuid,
    p_number text,
    p_issued_at timestamptz,
    p_descriptions text[],
    p_quantities integer[],
    p_unit_price_cents bigint[],
    p_amount_cents bigint[]
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_document_id uuid;
    v_order_number text;
BEGIN
    SELECT * INTO v_operation FROM invoice_operations
    WHERE id = p_operation_id AND kind = 'allowance' AND status = 'pending'
      AND lease_owner = p_owner AND lease_until > now() FOR UPDATE;
    IF NOT FOUND THEN RETURN '00000000-0000-0000-0000-000000000000'::uuid; END IF;
    IF p_number !~ '^[0-9]{16}$' OR p_issued_at IS NULL THEN
        RAISE EXCEPTION 'provider allowance identity is malformed'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_allowance_provider_identity';
    END IF;
    IF NOT invoice_operation_lines_match(v_operation.request_payload,
            p_descriptions, p_quantities, p_unit_price_cents, p_amount_cents) THEN
        RAISE EXCEPTION 'allowance line differs from the frozen claimed amount'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_allowance_line_authoritative';
    END IF;

    INSERT INTO invoice_documents
        (order_id, kind, original_id, number, amount_cents, request_key, issued_at)
    VALUES
        (v_operation.order_id, 'allowance', v_operation.target_document_id,
         p_number, v_operation.amount_cents,
         'allowance:' || v_operation.id::text, p_issued_at)
    RETURNING id INTO v_document_id;
    INSERT INTO invoice_document_lines
        (document_id, description, quantity, unit_price_cents,
         amount_cents, tax_type, position)
    VALUES
        (v_document_id, p_descriptions[1], p_quantities[1],
         p_unit_price_cents[1], p_amount_cents[1], 'taxable', 0);
    SELECT order_number INTO v_order_number FROM orders WHERE id = v_operation.order_id;
    PERFORM record_invoice_operation_audit(
        v_operation.id, v_document_id,
        jsonb_build_object('order', v_order_number, 'allowance', p_number,
                           'amount_cents', v_operation.amount_cents,
                           'operation', v_operation.id));
    UPDATE invoice_operations
    SET status = 'succeeded', result_document_id = v_document_id,
        request_payload = request_payload - 'customer_name' - 'email',
        completed_at = now(), last_error = NULL,
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = v_operation.id;
    RETURN v_document_id;
END;
$$;

CREATE FUNCTION settle_invoice_void(p_operation_id uuid, p_owner uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_operation invoice_operations%ROWTYPE;
    v_number text;
    v_order_number text;
BEGIN
    SELECT * INTO v_operation FROM invoice_operations
    WHERE id = p_operation_id AND kind = 'void' AND status = 'pending'
      AND lease_owner = p_owner AND lease_until > now() FOR UPDATE;
    IF NOT FOUND THEN RETURN '00000000-0000-0000-0000-000000000000'::uuid; END IF;
    UPDATE invoice_documents SET status = 'voided', voided_at = now()
    WHERE id = v_operation.target_document_id AND kind = 'invoice' AND status = 'issued'
    RETURNING number INTO v_number;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'void operation target is no longer a live invoice'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_void_target';
    END IF;
    SELECT order_number INTO v_order_number FROM orders WHERE id = v_operation.order_id;
    PERFORM record_invoice_operation_audit(
        v_operation.id, v_operation.target_document_id,
        jsonb_build_object('order', v_order_number, 'invoice', v_number,
                           'reason', v_operation.request_payload ->> 'reason',
                           'operation', v_operation.id));
    UPDATE invoice_operations
    SET status = 'succeeded', result_document_id = v_operation.target_document_id,
        request_payload = request_payload - 'customer_name' - 'email',
        completed_at = now(), last_error = NULL,
        lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = v_operation.id;
    RETURN v_operation.target_document_id;
END;
$$;

-- ============================================================================
-- Payment posting
--
-- store holds no INSERT on payments, so these are the door. A capture takes the
-- amount the PROVIDER reports and lets payments_capture_matches_order refuse it
-- if that disagrees with what the order is owed.
-- ============================================================================

-- Stripe can deliver a lifecycle event before the request that persists the
-- Checkout Session returns. Both paths take this transaction-scoped lock before
-- they inspect or write the local identity, so either the payment is visible to
-- the webhook or the event history is visible to open_payment. Hash collisions
-- only serialize unrelated references; they cannot weaken the fence.
CREATE FUNCTION lock_payment_provider_ref(p_provider text, p_provider_ref text)
RETURNS void
LANGUAGE sql VOLATILE STRICT AS $$
    SELECT pg_advisory_xact_lock(hashtextextended(
        length(p_provider)::text || ':' || p_provider || ':' || p_provider_ref,
        419583820019120301::bigint
    ));
$$;

COMMENT ON FUNCTION lock_payment_provider_ref(text, text) IS
    'Transaction lock shared by provider webhook claims and payment identity creation.';

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
    order_status text;
BEGIN
    -- Canonical payment lock order is provider reference -> order -> payment.
    -- ProcessWebhook takes this same first lock before it records an event.
    PERFORM lock_payment_provider_ref('stripe', p_provider_ref);

    -- The session is already payable at Stripe by the time this runs, and the
    -- cancelling transaction reads its expire-list from payments — a row it
    -- cannot see does not get closed. FOR UPDATE makes the two mutually
    -- exclusive: either this refuses, or Cancel's UPDATE waits and then finds
    -- this row in OpenSessionsForOrder.
    SELECT fulfillment_status INTO order_status
    FROM orders WHERE id = p_order_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'no order %', p_order_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_needs_order';
    END IF;
    IF order_status <> 'pending' THEN
        RAISE EXCEPTION 'order % is % and cannot open a checkout', p_order_id, order_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_refuses_settled_order';
    END IF;
    IF EXISTS (
        SELECT 1 FROM payments
        WHERE order_id = p_order_id AND status = 'succeeded'
    ) THEN
        RAISE EXCEPTION 'order % already has captured money', p_order_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_refuses_funded_order';
    END IF;

    SELECT id INTO payment_id FROM payments
    WHERE order_id = p_order_id AND provider_ref = p_provider_ref;
    IF FOUND THEN
        RETURN payment_id;
    END IF;

    -- An event that arrived before this row could not be attributed to a local
    -- payment. Reconciliation resolves the money/operator alarm and permits a
    -- NEW Stripe Session, but it must never make the already-observed provider
    -- identity eligible to become an active payment after the fact.
    IF EXISTS (
        SELECT 1
        FROM payment_webhook_events
        WHERE provider = 'stripe' AND object_ref = p_provider_ref
    ) THEN
        RAISE EXCEPTION 'provider reference % already has webhook history', p_provider_ref
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'payments_open_refuses_seen_provider_ref';
    END IF;

    -- The amount can move while the network call creating the Stripe session is
    -- in flight. Recheck under the order lock; the caller will expire the new
    -- remote session when this named refusal is returned.
    IF order_amount_owed(p_order_id) <> p_intended_amount_cents THEN
        RAISE EXCEPTION 'order % no longer owes %', p_order_id, p_intended_amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_open_matches_order';
    END IF;

    -- A provider event linked through its immutable session reference says
    -- this order needs a person. An alarm without this gate allows the customer
    -- to pay a replacement session while the first capture awaits a refund or
    -- manual posting.
    IF EXISTS (
        SELECT 1 FROM payments
        WHERE order_id = p_order_id AND status = 'requires_reconciliation'
    ) OR EXISTS (
        SELECT 1
        FROM payment_webhook_events e
        JOIN payments p
          ON p.provider = e.provider AND p.provider_ref = e.object_ref
        WHERE p.order_id = p_order_id
          AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
    ) THEN
        RAISE EXCEPTION 'order % has an unresolved provider event', p_order_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'payments_open_needs_reconciliation';
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
    payment_order_id uuid;
    current_status text;
BEGIN
    -- Match open_payment's order -> payment lock order. Reading the immutable
    -- order_id first is safe: the store role can move no payment between
    -- orders, and settled rows are additionally frozen by trigger.
    SELECT order_id INTO payment_order_id
    FROM payments WHERE provider_ref = p_provider_ref;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'no payment for provider reference %', p_provider_ref
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_provider_ref_known';
    END IF;

    PERFORM 1 FROM orders WHERE id = payment_order_id FOR UPDATE;

    SELECT id, status INTO payment_id, current_status
    FROM payments WHERE provider_ref = p_provider_ref
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'payment for provider reference % disappeared', p_provider_ref
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

-- Record a Checkout Session that Stripe has explicitly expired after local
-- admission failed. This is a tombstone, not another admission attempt: the
-- order may now be cancelled, funded, repriced or awaiting reconciliation.
-- Keeping the provider reference makes the idempotency generation durable and
-- makes an uncertain prior open idempotently converge to cancelled.
CREATE FUNCTION record_expired_payment(
    p_order_id uuid,
    p_provider_ref text,
    p_intended_amount_cents bigint
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    payment_id uuid;
    recorded_order_id uuid;
    recorded_amount bigint;
    recorded_status text;
BEGIN
    -- This is another creator of a local provider identity. It follows the
    -- same provider-ref-first order as open_payment and webhook processing,
    -- even though the row it creates is terminal rather than payable.
    PERFORM lock_payment_provider_ref('stripe', p_provider_ref);
    PERFORM 1 FROM orders WHERE id = p_order_id FOR UPDATE;

    INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
    VALUES (p_order_id, p_provider_ref, 'cancelled', p_intended_amount_cents)
    ON CONFLICT (provider, provider_ref) DO NOTHING;

    SELECT id, order_id, intended_amount_cents, status
    INTO payment_id, recorded_order_id, recorded_amount, recorded_status
    FROM payments
    WHERE provider = 'stripe' AND provider_ref = p_provider_ref
    FOR UPDATE;

    IF recorded_order_id <> p_order_id OR recorded_amount <> p_intended_amount_cents THEN
        RAISE EXCEPTION 'expired provider reference % disagrees with its recorded payment',
            p_provider_ref
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'payments_expired_identity_matches';
    END IF;

    -- A transport error can hide a successful open_payment commit. Stripe has
    -- now confirmed expiry, so that existing non-captured row must converge to
    -- the same terminal fact. Never regress captured money.
    IF recorded_status NOT IN ('succeeded', 'cancelled', 'reconciled') THEN
        UPDATE payments SET status = 'cancelled' WHERE id = payment_id;
    END IF;
    RETURN payment_id;
END;
$$;

-- A provider-complete Session cannot be expired and must not be described as
-- cancelled: `complete` does not itself prove either paid or unpaid. Consume
-- the idempotency generation in a non-payable reconciliation state. A capture
-- webhook that follows can still advance it to succeeded; an operator resolving
-- an already-durable event advances it to the distinct reconciled terminal.
CREATE FUNCTION record_complete_payment(
    p_order_id uuid,
    p_provider_ref text,
    p_intended_amount_cents bigint
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    payment_id uuid;
    recorded_order_id uuid;
    recorded_amount bigint;
    recorded_status text;
    target_status text;
BEGIN
    PERFORM lock_payment_provider_ref('stripe', p_provider_ref);
    PERFORM 1 FROM orders WHERE id = p_order_id FOR UPDATE;

    IF EXISTS (
        SELECT 1 FROM payment_webhook_events
        WHERE provider = 'stripe' AND object_ref = p_provider_ref
          AND unreconciled IS NOT NULL AND reconciled_at IS NULL
    ) THEN
        target_status := 'requires_reconciliation';
    ELSIF EXISTS (
        SELECT 1 FROM payment_webhook_events
        WHERE provider = 'stripe' AND object_ref = p_provider_ref
          AND unreconciled IS NOT NULL AND reconciled_at IS NOT NULL
    ) THEN
        -- Reconciliation is an authoritative safe-release resolution. The
        -- shared provider lock makes this exhaustive with release_payment_event:
        -- resolution either sees and closes this row, or this insert sees the
        -- already-resolved event and is born terminal.
        target_status := 'reconciled';
    ELSE
        -- The provider is complete but its webhook may still be in flight.
        -- Waiting is safer than opening another place to pay.
        target_status := 'requires_reconciliation';
    END IF;

    INSERT INTO payments (order_id, provider_ref, status, intended_amount_cents)
    VALUES (p_order_id, p_provider_ref, target_status, p_intended_amount_cents)
    ON CONFLICT (provider, provider_ref) DO NOTHING;

    SELECT id, order_id, intended_amount_cents, status
    INTO payment_id, recorded_order_id, recorded_amount, recorded_status
    FROM payments
    WHERE provider = 'stripe' AND provider_ref = p_provider_ref
    FOR UPDATE;

    IF recorded_order_id <> p_order_id OR recorded_amount <> p_intended_amount_cents THEN
        RAISE EXCEPTION 'complete provider reference % disagrees with its recorded payment',
            p_provider_ref
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'payments_complete_identity_matches';
    END IF;

    IF recorded_status NOT IN ('succeeded', 'cancelled', 'reconciled')
       AND recorded_status <> target_status THEN
        UPDATE payments SET status = target_status WHERE id = payment_id;
    END IF;
    RETURN payment_id;
END;
$$;

-- 'cancelled', not 'failed': payments_status_known does not admit the latter.
CREATE FUNCTION cancel_payment(p_provider_ref text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE payments SET status = 'cancelled'
    WHERE provider_ref = p_provider_ref
      AND status NOT IN ('succeeded', 'reconciled');
END;
$$;

-- Mark a claimed Stripe event as requiring a person. Once set, the reason is
-- immutable to the store role: it is evidence and, more importantly, blocks a
-- replacement Checkout Session until the admin reconciliation door below also
-- terminates the linked active payment.
CREATE FUNCTION mark_payment_event_unreconciled(
    p_event_id text,
    p_reason text
) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE payment_webhook_events
    SET processed_at = now(), unreconciled = p_reason
    WHERE provider = 'stripe' AND event_id = p_event_id
      AND processed_at IS NULL
      AND unreconciled IS NULL AND reconciled_at IS NULL;
    RETURN FOUND;
END;
$$;

-- Lock the catalogue roots a checkout will use without granting the storefront
-- UPDATE merely to satisfy PostgreSQL's FOR UPDATE privilege rule. Variants
-- come first in UUID order for cross-cart stock safety; products follow in UUID
-- order. That matches the existing variant -> product order of catalogue
-- integrity triggers while still making publication/retirement linearizable
-- with checkout. The cart row is already locked, so both sets are stable.
CREATE FUNCTION lock_cart_catalogue(p_cart_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    PERFORM 1
    FROM product_variants pv
    JOIN cart_items ci ON ci.variant_id = pv.id
    WHERE ci.cart_id = p_cart_id
    ORDER BY pv.id
    FOR UPDATE OF pv;

    PERFORM 1
    FROM products p
    WHERE p.id IN (
        SELECT pv.product_id
        FROM cart_items ci
        JOIN product_variants pv ON pv.id = ci.variant_id
        WHERE ci.cart_id = p_cart_id
    )
    ORDER BY p.id
    FOR UPDATE;
END;
$$;

-- An account can sign in concurrently from two browser tabs, each carrying a
-- different guest cart. The partial unique index on carts.user_id detects two
-- first adopters only after both have already decided that no account cart
-- exists; serialize that decision on the stable account row instead. store has
-- only narrow authentication-column UPDATE grants, not authority for a general
-- users row lock, so the lock lives behind this narrow door.
CREATE FUNCTION lock_user_for_cart_adoption(p_user_id uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    -- NO KEY UPDATE conflicts with another adoption's identical row lock but is
    -- compatible with the KEY SHARE that a checkout's user_id foreign keys take.
    -- Checkout owns the cart before it writes its order; UPDATE here would make
    -- adoption own user -> wait cart while checkout owns cart -> wait user.
    PERFORM 1 FROM users WHERE id = p_user_id FOR NO KEY UPDATE;
    RETURN FOUND;
END;
$$;

COMMENT ON FUNCTION lock_user_for_cart_adoption(uuid) IS
    'Serialize the choice and merge of the one cart an account may own.';

-- A logged-in checkout will later write orders.user_id and related foreign
-- keys. Take their natural KEY SHARE lock before the cart, not halfway through
-- the order write, so erasure/adoption and checkout all use user -> cart order.
-- KEY SHARE is compatible with adoption's NO KEY UPDATE but conflicts with the
-- UPDATE/DELETE that erasure holds across its complete PII snapshot.
CREATE FUNCTION lock_user_for_checkout(p_user_id uuid) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    PERFORM 1 FROM users WHERE id = p_user_id FOR KEY SHARE;
    RETURN FOUND;
END;
$$;

COMMENT ON FUNCTION lock_user_for_checkout(uuid) IS
    'Keep a checkout account alive before locking its cart and writing user foreign keys.';

-- Releasing a provider event is the operator's explicit statement that every
-- provider-side cent was fully refunded, or that a succeeded local payment
-- already accounts for it. End a still-active linked attempt in the same
-- transaction, so cleared money cannot leave a resumable Session after the
-- alarm leaves /admin/health. A succeeded payment is terminal and untouched.
CREATE FUNCTION release_payment_event(p_event_id text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    ref text;
    payment_order_id uuid;
BEGIN
    -- Read the immutable provider identity without taking the event row first:
    -- webhook processing locks provider-ref before its INSERT, so reversing
    -- those locks here would create an event-row/provider-ref ABBA cycle.
    SELECT object_ref INTO ref
    FROM payment_webhook_events
    WHERE provider = 'stripe' AND event_id = p_event_id
      AND unreconciled IS NOT NULL AND reconciled_at IS NULL;
    IF NOT FOUND THEN
        RETURN false;
    END IF;

    IF ref IS NOT NULL THEN
        PERFORM lock_payment_provider_ref('stripe', ref);

        -- If recovery has linked the complete Session since the event arrived,
        -- serialize lifting its order-level gate with open_payment.
        SELECT order_id INTO payment_order_id
        FROM payments WHERE provider = 'stripe' AND provider_ref = ref;
        IF FOUND THEN
            PERFORM 1 FROM orders WHERE id = payment_order_id FOR UPDATE;
        END IF;
    END IF;

    UPDATE payment_webhook_events
    SET reconciled_at = now()
    WHERE provider = 'stripe' AND event_id = p_event_id
      AND unreconciled IS NOT NULL AND reconciled_at IS NULL
    RETURNING object_ref INTO ref;

    IF NOT FOUND THEN
        RETURN false;
    END IF;

    UPDATE payments
    SET status = CASE
        WHEN status = 'requires_reconciliation' THEN 'reconciled'
        ELSE 'cancelled'
    END
    WHERE provider = 'stripe' AND provider_ref = ref
      AND status IN ('requires_payment', 'requires_action', 'processing',
                     'requires_reconciliation');
    RETURN true;
END;
$$;

-- A complete Session can precede its capture webhook. When an operator confirms
-- at Stripe that it was PAID, post the immutable intended amount through the
-- same capture_payment invariants as a signed webhook. The function returns the
-- payment identity so its query can derive the order facts Go needs to append
-- the paid timeline event, loyalty award and receipt in this same caller
-- transaction. It never accepts an amount from the form: the payment row is the
-- only amount Stripe was asked to take.
CREATE FUNCTION attribute_complete_payment_paid(p_provider_ref text) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    target_payment_id uuid;
    target_order_id uuid;
    target_amount bigint;
BEGIN
    PERFORM lock_payment_provider_ref('stripe', p_provider_ref);

    -- Read the immutable parent before taking locks, then follow the canonical
    -- provider-ref -> order -> payment order used by webhook/open/recovery.
    SELECT p.order_id INTO target_order_id
    FROM payments p
    WHERE p.provider = 'stripe' AND p.provider_ref = p_provider_ref
      AND p.status = 'requires_reconciliation'
      AND NOT EXISTS (
          SELECT 1 FROM payment_webhook_events e
          WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
            AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      );
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    PERFORM 1 FROM orders WHERE id = target_order_id FOR UPDATE;

    SELECT p.id, p.order_id, p.intended_amount_cents
    INTO target_payment_id, target_order_id, target_amount
    FROM payments p
    WHERE p.provider = 'stripe' AND p.provider_ref = p_provider_ref
      AND p.status = 'requires_reconciliation'
      AND NOT EXISTS (
          SELECT 1 FROM payment_webhook_events e
            WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
              AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      )
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    PERFORM capture_payment(p_provider_ref, target_amount, NULL, NULL);
    RETURN target_payment_id;
END;
$$;

-- The other complete-Session outcome is explicitly non-capture: staff have
-- confirmed at Stripe that no money was taken, or that it was fully refunded.
-- Only that fact may lift the admission gate and permit a later generation.
-- Keeping it separate from the paid function makes it impossible for one vague
-- "handled" button to silently choose the money outcome.
CREATE FUNCTION release_complete_payment(p_provider_ref text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    payment_order_id uuid;
BEGIN
    PERFORM lock_payment_provider_ref('stripe', p_provider_ref);

    SELECT order_id INTO payment_order_id
    FROM payments p
    WHERE p.provider = 'stripe' AND p.provider_ref = p_provider_ref
      AND p.status = 'requires_reconciliation'
      AND NOT EXISTS (
          SELECT 1 FROM payment_webhook_events e
          WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
            AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      );
    IF NOT FOUND THEN
        RETURN false;
    END IF;

    PERFORM 1 FROM orders WHERE id = payment_order_id FOR UPDATE;

    UPDATE payments p
    SET status = 'reconciled'
    WHERE p.provider = 'stripe' AND p.provider_ref = p_provider_ref
      AND p.status = 'requires_reconciliation'
      AND NOT EXISTS (
          SELECT 1 FROM payment_webhook_events e
          WHERE e.provider = p.provider AND e.object_ref = p.provider_ref
            AND e.unreconciled IS NOT NULL AND e.reconciled_at IS NULL
      );
    RETURN FOUND;
END;
$$;

-- The shared ledger primitive is intentionally ungranted.  Role-callable doors
-- below own the sign, attribution and durable object that justify a posting;
-- otherwise one broad SECURITY DEFINER function is a mint for `store` and an
-- unaudited debit facility for `admin`.
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
    existing store_credit_entries%ROWTYPE;
BEGIN
    -- An account is created on first use rather than at registration: most
    -- customers never have store credit.
    INSERT INTO store_credit_accounts (user_id) VALUES (p_user_id)
    ON CONFLICT (user_id) DO UPDATE SET user_id = excluded.user_id
    RETURNING id INTO account_id;

    INSERT INTO store_credit_entries
        (account_id, amount_cents, reason, order_id, idempotency_key, actor_user_id)
    VALUES (account_id, p_amount_cents, p_reason, p_order_id, p_idempotency_key, p_actor_user_id)
    ON CONFLICT (idempotency_key) DO NOTHING
    RETURNING id INTO entry_id;
    IF entry_id IS NULL THEN
        SELECT e.* INTO existing FROM store_credit_entries e
        WHERE e.idempotency_key = p_idempotency_key;
        IF existing.account_id <> account_id
           OR existing.amount_cents <> p_amount_cents
           OR existing.reason <> p_reason
           OR existing.order_id IS DISTINCT FROM p_order_id
           OR existing.actor_user_id IS DISTINCT FROM p_actor_user_id
           OR existing.reverses_id IS NOT NULL THEN
            RAISE EXCEPTION 'store credit key collides with another posting'
                USING ERRCODE = 'check_violation',
                      CONSTRAINT = 'store_credit_idempotency_attribution';
        END IF;
        entry_id := existing.id;
    END IF;
    RETURN entry_id;
END;
$$;

-- Checkout may only debit the account belonging to the still-open order.  The
-- debit amount is chosen by the customer, but its sign, reason, key and actor
-- are not caller-owned facts.
CREATE FUNCTION spend_store_credit(
    p_order_id uuid,
    p_amount_cents bigint
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_owner uuid;
    v_owed bigint;
    v_existing store_credit_entries%ROWTYPE;
BEGIN
    IF p_amount_cents >= 0 OR p_amount_cents < -10000000000 THEN
        RAISE EXCEPTION 'checkout credit must be a debit, got %', p_amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_checkout_debit';
    END IF;
    SELECT user_id INTO v_owner FROM orders
    WHERE id = p_order_id AND fulfillment_status = 'pending'
      AND NOT EXISTS (SELECT 1 FROM payments
                      WHERE order_id = p_order_id AND status = 'succeeded')
    FOR UPDATE;
    IF NOT FOUND OR v_owner IS NULL THEN
        RAISE EXCEPTION 'credit spend requires a customer-owned open unpaid order %', p_order_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_checkout_owner';
    END IF;

    SELECT e.* INTO v_existing FROM store_credit_entries e
    WHERE e.idempotency_key = 'order:' || p_order_id::text;
    IF FOUND THEN
        IF v_existing.order_id = p_order_id
           AND v_existing.amount_cents = p_amount_cents
           AND EXISTS (SELECT 1 FROM store_credit_accounts a
                       WHERE a.id = v_existing.account_id AND a.user_id = v_owner) THEN
            RETURN v_existing.id;
        END IF;
        RAISE EXCEPTION 'checkout credit key collides with another posting'
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'store_credit_checkout_attribution';
    END IF;

    v_owed := order_amount_owed(p_order_id);
    IF -p_amount_cents > v_owed THEN
        RAISE EXCEPTION 'credit debit % exceeds order % room %',
            -p_amount_cents, p_order_id, v_owed
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_checkout_amount';
    END IF;

    -- Refuse an oversized fully credited checkout immediately, before it leaves
    -- the customer with a paid order fulfilment cannot accept. The authoritative
    -- pending-to-picking transition repeats this check; a partial debit is
    -- checked later by the card-capture guard above.
    IF -p_amount_cents = v_owed
       AND (SELECT count(*) FROM canonical_invoice_lines(p_order_id)) > 999 THEN
        RAISE EXCEPTION 'order % has more than 999 invoice items', p_order_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'invoice_issue_item_count';
    END IF;

    RETURN post_store_credit(v_owner, p_amount_cents, '訂單折抵',
                             p_order_id, 'order:' || p_order_id::text, NULL);
END;
$$;

-- A staff grant is always a bounded positive, orderless posting.  Requiring a
-- durable staff/admin user prevents a caller from naming a customer as actor or
-- leaving the append-only ledger unattributed.
CREATE FUNCTION grant_store_credit(
    p_user_id uuid,
    p_amount_cents bigint,
    p_reason text,
    p_actor_user_id uuid,
    p_operation_id uuid
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_key text;
    v_existing store_credit_entries%ROWTYPE;
	v_entry_id uuid;
BEGIN
    IF p_amount_cents <= 0 OR p_amount_cents > 10000000 THEN
        RAISE EXCEPTION 'staff credit grant is outside its positive ceiling: %', p_amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_grant_amount';
    END IF;
    IF p_reason !~ '[^[:space:]]' OR char_length(p_reason) > 200 THEN
        RAISE EXCEPTION 'staff credit grant reason must contain at most 200 characters'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_grant_reason';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'credit grant requires a durable staff actor'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_grant_actor';
    END IF;
	IF p_operation_id IS NULL
	   OR p_operation_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
		RAISE EXCEPTION 'credit grant requires a non-zero operation id'
			USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_grant_operation';
	END IF;

	v_key := 'grant:' || p_operation_id::text;
	-- One durable operation is one posting.  The lock makes a concurrent retry
	-- observe the first insert before deciding whether an audit-worthy effect
	-- happened, while a later operation with identical business values remains a
	-- distinct grant under its own request identity.
	PERFORM pg_advisory_xact_lock(hashtextextended(v_key, 744970110345955117::bigint));
	SELECT e.* INTO v_existing FROM store_credit_entries e
	WHERE e.idempotency_key = v_key;
	IF FOUND THEN
		IF v_existing.amount_cents <> p_amount_cents
		   OR v_existing.reason <> p_reason
		   OR v_existing.order_id IS NOT NULL
		   OR v_existing.actor_user_id IS DISTINCT FROM p_actor_user_id
		   OR NOT EXISTS (SELECT 1 FROM store_credit_accounts a
		                  WHERE a.id = v_existing.account_id AND a.user_id = p_user_id) THEN
			RAISE EXCEPTION 'credit grant operation key collides with another grant'
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'store_credit_idempotency_attribution';
		END IF;
		-- A non-null sentinel keeps the sqlc contract a plain uuid: scanning SQL
		-- NULL into google/uuid fails before Go can decide this was an exact retry.
		RETURN '00000000-0000-0000-0000-000000000000'::uuid;
	END IF;

	v_entry_id := post_store_credit(p_user_id, p_amount_cents, p_reason, NULL,
		v_key, p_actor_user_id);
	RETURN v_entry_id;
END;
$$;

-- Return compensation is tied to the approved return, its order and customer.
-- It may only post the credit-funded remainder after card capacity, and the key
-- is derived from that return rather than supplied by the role caller.
CREATE FUNCTION compensate_return_with_credit(
    p_return_request_id uuid,
    p_amount_cents bigint,
    p_actor_user_id uuid
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_expected bigint;
    v_user_id uuid;
    v_order_id uuid;
BEGIN
    -- Read the immutable relation, then lock the user before the order. Erasure
    -- uses the same user -> orders order: either this payout lands and erasure
    -- subsequently sees the still-open return, or erasure wins and no posting
    -- can be made to an orphaned account.
    SELECT o.user_id, o.id INTO v_user_id, v_order_id FROM orders o
    JOIN return_requests r ON r.order_id = o.id
    WHERE r.id = p_return_request_id AND r.status = 'approved'
      AND o.user_id IS NOT NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'return % is not an approved customer claim',
            p_return_request_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_owner';
    END IF;
    PERFORM 1 FROM users WHERE id = v_user_id FOR KEY SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'return % no longer has a live customer account',
            p_return_request_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_owner';
    END IF;
    PERFORM 1 FROM orders o
    JOIN return_requests r ON r.order_id = o.id
    WHERE r.id = p_return_request_id AND r.status = 'approved'
      AND o.id = v_order_id AND o.user_id = v_user_id
    FOR UPDATE OF o, r;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'return % is not an approved customer claim',
            p_return_request_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_owner';
    END IF;
    IF p_amount_cents <= 0 THEN
        RAISE EXCEPTION 'return compensation must be a positive return posting'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_amount';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users
                   WHERE id = p_actor_user_id AND role IN ('staff', 'admin')) THEN
        RAISE EXCEPTION 'return compensation requires a durable staff actor'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_actor';
    END IF;

    v_expected := return_store_credit_allocation(p_return_request_id);
    IF p_amount_cents <> v_expected OR v_expected = 0 THEN
        RAISE EXCEPTION 'return credit %, expected % for return %',
            p_amount_cents, v_expected, p_return_request_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_return_amount';
    END IF;

    RETURN post_store_credit(v_user_id, v_expected, '退貨退回購物金',
        v_order_id, 'return-credit:' || p_return_request_id::text, p_actor_user_id);
END;
$$;

-- Read and hold checkout's one credit account at the same linearization point.
-- Store can call this narrow door but retains no UPDATE privilege on accounts
-- and no INSERT privilege on the append-only ledger.
CREATE FUNCTION lock_store_credit_for_checkout(p_user_id uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    locked_account_id uuid;
    balance bigint;
BEGIN
    SELECT id INTO locked_account_id
    FROM store_credit_accounts
    WHERE user_id = p_user_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN 0;
    END IF;

    SELECT coalesce(sum(amount_cents), 0) INTO balance
    FROM store_credit_entries
    WHERE account_id = locked_account_id;
    RETURN greatest(balance, 0);
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
    'Return store credit spent on an order that will not ship. Legal only after '
    'the order has durably become cancelled — '
    'store_credit_guard enforces that, so calling this on a shipped order is '
    'refused rather than quietly paying twice.';

GRANT EXECUTE ON FUNCTION open_payment(uuid, text, bigint) TO store;
GRANT EXECUTE ON FUNCTION capture_payment(text, bigint, text, text) TO store;
GRANT EXECUTE ON FUNCTION record_expired_payment(uuid, text, bigint) TO store;
GRANT EXECUTE ON FUNCTION record_complete_payment(uuid, text, bigint) TO store;
GRANT EXECUTE ON FUNCTION cancel_payment(text) TO store;
GRANT EXECUTE ON FUNCTION mark_payment_event_unreconciled(text, text) TO store;
GRANT EXECUTE ON FUNCTION lock_payment_provider_ref(text, text) TO store;
GRANT EXECUTE ON FUNCTION lock_cart_catalogue(uuid) TO store;
GRANT EXECUTE ON FUNCTION lock_user_for_cart_adoption(uuid) TO store;
GRANT EXECUTE ON FUNCTION lock_user_for_checkout(uuid) TO store;
GRANT EXECUTE ON FUNCTION release_payment_event(text) TO admin;
GRANT EXECUTE ON FUNCTION attribute_complete_payment_paid(text) TO admin;
GRANT EXECUTE ON FUNCTION release_complete_payment(text) TO admin;
GRANT EXECUTE ON FUNCTION spend_store_credit(uuid, bigint) TO store;
GRANT EXECUTE ON FUNCTION lock_store_credit_for_checkout(uuid) TO store;
-- A customer cancels their own unpaid order, so the storefront role needs this.
GRANT EXECUTE ON FUNCTION reverse_order_credit(uuid) TO store;
GRANT EXECUTE ON FUNCTION grant_store_credit(uuid, bigint, text, uuid, uuid) TO admin;
GRANT EXECUTE ON FUNCTION compensate_return_with_credit(uuid, bigint, uuid) TO admin;
-- And the back office cancels on a customer's behalf.
GRANT EXECUTE ON FUNCTION reverse_order_credit(uuid) TO admin;

-- The sweep at the foot of this file revokes EXECUTE from PUBLIC, so an ungranted
-- function is a 500 on every storefront page — and green in every test, because
-- the tests connect as the owner.
GRANT EXECUTE ON FUNCTION localized_name(text, text, text) TO store, admin, reporting;
GRANT EXECUTE ON FUNCTION shop_day(timestamptz) TO store, admin, reporting;
GRANT EXECUTE ON FUNCTION shop_today() TO store, admin, reporting;


-- A provider refund is a two-commit state machine. This first door derives the
-- frozen payment-source amount, generation, lineage, key and reason from the
-- approved return. An ambiguous attempt reuses its own key; a known
-- failed/cancelled attempt remains immutable and gets one successor. The claim
-- and THIS call's staff attribution commit before any network call.
CREATE FUNCTION claim_return_refund_execution(
	p_return_request_id uuid,
	p_actor uuid,
	p_request_id text
) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
	v_order_id uuid;
	v_user_id uuid;
	v_payment_id uuid;
	v_reason text;
	v_key text;
	v_returnable bigint;
	v_captured bigint;
	v_card_amount bigint;
	v_credit_amount bigint;
	v_credit_posted bigint;
	v_next_attempt integer;
	v_existing refunds%ROWTYPE;
	v_previous refunds%ROWTYPE;
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM users u
		WHERE u.id = p_actor AND u.role IN ('staff', 'admin')
	) THEN
		RAISE EXCEPTION 'refund execution requires a durable staff actor'
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_execution_actor';
	END IF;
	IF p_request_id IS NULL
	   OR p_request_id !~ '^[A-Za-z0-9-]{1,64}$' THEN
		RAISE EXCEPTION 'refund execution requires a valid request id'
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_execution_request';
	END IF;

	-- The return and order are the aggregate claim. Taking them before the
	-- payment follows compensate_return_with_credit's order and serialises two
	-- staff members retrying the same approved return.
	SELECT r.order_id, o.user_id, nullif(r.resolution, ''),
	       (r.goods_refund_cents + r.shipping_refund_cents)::bigint,
	       r.card_refund_cents, r.credit_refund_cents
	INTO v_order_id, v_user_id, v_reason, v_returnable,
	     v_card_amount, v_credit_amount
	FROM return_requests r
	JOIN orders o ON o.id = r.order_id
	WHERE r.id = p_return_request_id AND r.status = 'approved'
	FOR UPDATE OF o, r;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'return % is not approved', p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_return_approved';
	END IF;

	IF v_card_amount + v_credit_amount <> v_returnable THEN
		RAISE EXCEPTION 'return % does not fit its frozen payment sources',
			p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_sources_cover_return';
	END IF;
	SELECT coalesce(sum(sc.amount_cents), 0)::bigint
	INTO v_credit_posted
	FROM store_credit_entries sc
	WHERE sc.idempotency_key = 'return-credit:' || p_return_request_id::text;
	IF v_credit_posted NOT IN (0, v_credit_amount) THEN
		RAISE EXCEPTION 'return % has credit posting %, expected %',
			p_return_request_id, v_credit_posted, v_credit_amount
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_credit_attribution';
	END IF;
	-- Once the exact credit half is posted it no longer needs a live account. This
	-- is what lets a known-failed card attempt get a successor after lawful account
	-- erasure without trying to recreate or re-credit the erased customer.
	IF v_credit_amount > 0 AND v_user_id IS NULL
	   AND v_credit_posted <> v_credit_amount THEN
		RAISE EXCEPTION 'return % still needs a live store-credit destination',
			p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_sources_cover_return';
	END IF;
	IF v_returnable <= 0 OR v_card_amount <= 0 THEN
		RAISE EXCEPTION 'return % has no positive card amount', p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_card_amount_positive';
	END IF;

	SELECT p.id, p.captured_amount_cents
	INTO v_payment_id, v_captured
	FROM payments p
	WHERE p.order_id = v_order_id AND p.status = 'succeeded'
	FOR UPDATE;
	IF NOT FOUND OR v_captured IS NULL THEN
		RAISE EXCEPTION 'return % has no captured card payment', p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_return_captured';
	END IF;
	IF v_card_amount > v_captured THEN
		RAISE EXCEPTION 'return % card source % exceeds capture %',
			p_return_request_id, v_card_amount, v_captured
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_sources_cover_return';
	END IF;

	SELECT rf.* INTO v_existing
	FROM refunds rf
	WHERE rf.return_request_id = p_return_request_id
	ORDER BY rf.attempt_no DESC
	LIMIT 1
	FOR UPDATE;
	IF FOUND THEN
		v_key := CASE v_existing.attempt_no
			WHEN 1 THEN 'return:' || p_return_request_id::text
			ELSE 'return:' || p_return_request_id::text
			     || ':attempt:' || v_existing.attempt_no::text
		END;
		IF v_existing.payment_id <> v_payment_id
		   OR v_existing.return_request_id IS DISTINCT FROM p_return_request_id
		   OR v_existing.amount_cents <> v_card_amount
		   OR v_existing.reason IS DISTINCT FROM v_reason
		   OR v_existing.request_key <> v_key THEN
			RAISE EXCEPTION 'refund key % collides with another attribution', v_key
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'refunds_request_attribution';
		END IF;
		IF v_existing.status = 'succeeded' THEN
			RAISE EXCEPTION 'refund % already succeeded', v_existing.id
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'refunds_execution_settled';
		ELSIF v_existing.status IN ('failed', 'cancelled') THEN
			v_previous := v_existing;
			v_next_attempt := v_previous.attempt_no + 1;
			v_key := 'return:' || p_return_request_id::text
			         || ':attempt:' || v_next_attempt::text;
			INSERT INTO refunds (
				payment_id, return_request_id, attempt_no, previous_refund_id,
				request_key, status, amount_cents, reason
			) VALUES (
				v_payment_id, p_return_request_id, v_next_attempt, v_previous.id,
				v_key, 'pending', v_card_amount, v_reason
			)
			RETURNING refunds.* INTO v_existing;
		END IF;
	ELSE
		v_next_attempt := 1;
		v_key := 'return:' || p_return_request_id::text;
		INSERT INTO refunds (
			payment_id, return_request_id, attempt_no, previous_refund_id,
			request_key, status, amount_cents, reason
		) VALUES (
			v_payment_id, p_return_request_id, v_next_attempt, NULL,
			v_key, 'pending', v_card_amount, v_reason
		)
		RETURNING refunds.* INTO v_existing;
	END IF;

	PERFORM record_audit_event(
		p_actor, 'refund.provider_attempt', 'refunds', v_existing.id,
		NULL,
			jsonb_build_object(
				'status', v_existing.status,
				'request_key', v_existing.request_key,
				'attempt_no', v_existing.attempt_no,
				'previous_refund_id', v_existing.previous_refund_id,
				'amount_cents', v_card_amount
		),
		p_request_id
	);

	RETURN v_existing.id;
END;
$$;

-- Private transition engine. The final boolean distinguishes a provider object
-- outcome from an explicit CREATE rejection; only the latter may be failed with
-- no provider identity. It is deliberately not granted to an application role.
CREATE FUNCTION apply_refund_provider_outcome(
	p_refund_id uuid,
	p_provider_ref text,
	p_status text,
	p_actor uuid,
	p_request_id text,
	p_api_rejection boolean
) RETURNS boolean
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
	v_existing refunds%ROWTYPE;
	v_payment_id uuid;
	v_action text;
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM users u
		WHERE u.id = p_actor AND u.role IN ('staff', 'admin')
	) THEN
		RAISE EXCEPTION 'refund outcome requires a durable staff actor'
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_outcome_actor';
	END IF;
	IF p_request_id IS NULL
	   OR p_request_id !~ '^[A-Za-z0-9-]{1,64}$' THEN
		RAISE EXCEPTION 'refund outcome requires a valid request id'
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_outcome_request';
	END IF;
	IF p_status NOT IN ('pending', 'requires_action', 'succeeded', 'failed', 'cancelled') THEN
		RAISE EXCEPTION 'unknown refund provider status %', p_status
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_provider_outcome_known';
	END IF;
	IF p_api_rejection THEN
		IF p_status <> 'failed' OR p_provider_ref IS NOT NULL THEN
			RAISE EXCEPTION 'an API rejection is failed without a provider object'
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'refunds_api_rejection_shape';
		END IF;
		v_action := 'refund.provider_rejected';
	ELSE
		IF p_provider_ref IS NULL
		   OR char_length(p_provider_ref) NOT BETWEEN 1 AND 255
		   OR p_provider_ref ~ '[[:space:][:cntrl:]]' THEN
			RAISE EXCEPTION 'provider-object refund outcome needs a valid identity'
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'refunds_provider_ref_valid';
		END IF;
		v_action := 'refund.provider_' || p_status;
	END IF;

	-- Read the immutable relation, then take payment before refund. A concurrent
	-- retry claim owns order/return -> payment -> latest refund; taking refund
	-- first here and letting refunds_guard acquire payment during UPDATE creates
	-- a payment <-> refund deadlock and can roll a provider success back to
	-- pending.
	SELECT rf.payment_id INTO v_payment_id
	FROM refunds rf
	WHERE rf.id = p_refund_id;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'no refund for id %', p_refund_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_request_key_known';
	END IF;
	PERFORM 1 FROM payments p WHERE p.id = v_payment_id FOR UPDATE;
	SELECT rf.* INTO v_existing
	FROM refunds rf
	WHERE rf.id = p_refund_id AND rf.payment_id = v_payment_id
	FOR UPDATE;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'refund % changed payment identity while its outcome was claimed',
			p_refund_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_request_attribution';
	END IF;
	IF v_existing.provider_ref IS NOT NULL
	   AND v_existing.provider_ref IS DISTINCT FROM p_provider_ref THEN
		RAISE EXCEPTION 'refund % belongs to provider object %, not %',
			p_refund_id, v_existing.provider_ref, p_provider_ref
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_provider_ref_immutable';
	END IF;

	-- An exact replay proves no new transition and must preserve both timestamps
	-- and the one outcome audit row. Any other terminal replay is a contradiction.
	IF v_existing.status IN ('succeeded', 'failed', 'cancelled') THEN
		IF v_existing.status = p_status
		   AND v_existing.provider_ref IS NOT DISTINCT FROM p_provider_ref THEN
			RETURN false;
		END IF;
		RAISE EXCEPTION 'refund % is terminal as %, cannot become %',
			p_refund_id, v_existing.status, p_status
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'refunds_no_regression';
	END IF;
	IF v_existing.status = p_status
	   AND v_existing.provider_ref IS NOT DISTINCT FROM p_provider_ref THEN
		RETURN false;
	END IF;

	UPDATE refunds
	SET status = p_status,
	    provider_ref = coalesce(provider_ref, p_provider_ref),
	    succeeded_at = CASE WHEN p_status = 'succeeded' THEN now() ELSE succeeded_at END,
	    failed_at = CASE WHEN p_status = 'failed' THEN now() ELSE failed_at END
	WHERE id = p_refund_id;

	PERFORM record_audit_event(
		p_actor, v_action, 'refunds', p_refund_id,
		jsonb_build_object(
			'status', v_existing.status,
			'provider_ref', v_existing.provider_ref
		),
		jsonb_build_object(
			'status', p_status,
			'provider_ref', p_provider_ref,
			'evidence', CASE WHEN p_api_rejection
			                 THEN 'api_rejection' ELSE 'provider_object' END
		),
		p_request_id
	);
	RETURN true;
END;
$$;

CREATE FUNCTION record_refund_pending(uuid, text, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, $2, 'pending', $3, $4, false);
$$;

CREATE FUNCTION record_refund_requires_action(uuid, text, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, $2, 'requires_action', $3, $4, false);
$$;

CREATE FUNCTION record_refund_succeeded(uuid, text, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, $2, 'succeeded', $3, $4, false);
$$;

CREATE FUNCTION record_refund_failed(uuid, text, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, $2, 'failed', $3, $4, false);
$$;

CREATE FUNCTION record_refund_cancelled(uuid, text, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, $2, 'cancelled', $3, $4, false);
$$;

CREATE FUNCTION record_refund_api_rejection(uuid, uuid, text) RETURNS boolean
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
	SELECT apply_refund_provider_outcome($1, NULL::text, 'failed', $2, $3, true);
$$;

GRANT EXECUTE ON FUNCTION claim_return_refund_execution(uuid, uuid, text) TO admin;
GRANT EXECUTE ON FUNCTION record_refund_pending(uuid, text, uuid, text),
	record_refund_requires_action(uuid, text, uuid, text),
	record_refund_succeeded(uuid, text, uuid, text),
	record_refund_failed(uuid, text, uuid, text),
	record_refund_cancelled(uuid, text, uuid, text),
	record_refund_api_rejection(uuid, uuid, text)
TO admin;


-- Decide and hold the variant behind one restock request. SELECT ... FOR UPDATE
-- requires table UPDATE privilege even though no column changes; keeping the
-- lock in this narrow SECURITY DEFINER door avoids granting the storefront a
-- general product-variant writer merely to close a notice/restock race.
CREATE FUNCTION lock_stock_notice_variant(p_variant_id uuid, p_slug text)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_variant_id uuid;
BEGIN
    SELECT pv.id INTO v_variant_id
    FROM product_variants pv
    JOIN products p ON p.id = pv.product_id
    WHERE pv.id = p_variant_id
      AND p.slug = p_slug
      AND p.status = 'active'
      AND pv.is_active
      AND pv.stock_quantity <= pv.safety_stock
    FOR UPDATE OF pv;
    RETURN v_variant_id;
END;
$$;

GRANT EXECUTE ON FUNCTION lock_stock_notice_variant(uuid, text) TO store;


-- Hold exactly the coupon definition checkout is about to quote and redeem.
-- Store may already SELECT coupon definitions, but receives no table UPDATE
-- privilege merely because PostgreSQL requires it for FOR UPDATE.
CREATE FUNCTION lock_coupon_for_checkout(p_code text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    PERFORM 1 FROM coupons WHERE upper(code) = upper(p_code) FOR UPDATE;
END;
$$;

GRANT EXECUTE ON FUNCTION lock_coupon_for_checkout(text) TO store;

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
--
-- This file is still the disposable, never-deployed 001. A deployed ledger
-- would require a 002 that first added lot_id nullable, allocated every old
-- spend FIFO across that account's awards (splitting rows as needed), then
-- filled expires_on and made both columns mandatory. Any account whose old
-- spends exceeded its awards would require manual reconciliation: no automatic
-- rule can truthfully invent the historical lot those points consumed.
-- ---------------------------------------------------------------------------

CREATE TABLE loyalty_entries (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    -- At the ACCOUNT, not the user, so erasure leaves the ledger balanced.
    account_id      uuid NOT NULL REFERENCES store_credit_accounts (id) ON DELETE RESTRICT,
    -- Structural vocabulary, separate from the human/business reason. In
    -- particular a return clawback is not a redemption merely because both
    -- may carry a negative balance effect.
    kind            text NOT NULL,
    points          bigint NOT NULL,
    reason          text NOT NULL,
    -- The caller's name for this posting: a retried award submits the same key
    -- and meets the unique index.
    idempotency_key text NOT NULL,
    order_id        uuid REFERENCES orders (id) ON DELETE RESTRICT,
    -- A spend or clawback names the award lot it settles. It is always a new
    -- row: the award itself is never updated with a consumed counter.
    lot_id          uuid REFERENCES loyalty_entries (id) ON DELETE RESTRICT,
    -- Only a clawback carries these. requested_points records what the refund
    -- called for even when the lot was wholly consumed and points is therefore
    -- zero; the difference is a durable shortfall rather than a silent writeoff.
    requested_points  bigint,
    return_request_id uuid REFERENCES return_requests (id) ON DELETE RESTRICT,
    -- Every child carries its award lot's expiry, so the award and every fact
    -- settled against it leave the spendable balance together.
    expires_on      date NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT loyalty_entries_points_nonzero
        CHECK (points <> 0 OR kind = 'clawback'),
    CONSTRAINT loyalty_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT loyalty_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]'),
    -- Kind, never sign, defines the row. A clawback may be zero so a wholly
    -- consumed lot still records the requested reversal and its full shortfall.
    CONSTRAINT loyalty_entries_kind_shape CHECK (
        (kind = 'award' AND points >= 0 AND lot_id IS NULL
            AND requested_points IS NULL AND return_request_id IS NULL)
        OR
        (kind = 'spend' AND points <= 0 AND lot_id IS NOT NULL
            AND requested_points IS NULL AND return_request_id IS NULL)
        OR
        (kind = 'clawback' AND points <= 0 AND lot_id IS NOT NULL
            AND requested_points > 0 AND -points <= requested_points
            AND return_request_id IS NOT NULL)
        OR
        (kind = 'clawback' AND points <= 0 AND lot_id IS NOT NULL
            AND requested_points > 0 AND -points <= requested_points
            AND return_request_id IS NULL AND order_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX loyalty_entries_idempotency_key
    ON loyalty_entries (idempotency_key);
CREATE INDEX loyalty_entries_account_idx
    ON loyalty_entries (account_id, expires_on);
CREATE INDEX loyalty_entries_order_id_idx ON loyalty_entries (order_id);
CREATE INDEX loyalty_entries_lot_idx
    ON loyalty_entries (lot_id) WHERE lot_id IS NOT NULL;
CREATE INDEX loyalty_entries_return_request_idx
    ON loyalty_entries (return_request_id) WHERE return_request_id IS NOT NULL;

-- One submitted redemption form is one durable operation. First POST binds its
-- hidden UUID to the customer in the ledger transaction, so failed/unsubmitted
-- forms leave no rows; a replay converges and another account cannot reuse it.
CREATE TABLE loyalty_redemption_operations (
	id uuid PRIMARY KEY DEFAULT uuidv7(),
	user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	points bigint,
	credit_cents bigint,
	created_at timestamptz NOT NULL DEFAULT now(),
	completed_at timestamptz,
	CONSTRAINT loyalty_redemption_operation_completed_together CHECK (
		(points IS NULL AND credit_cents IS NULL AND completed_at IS NULL)
		OR (points > 0 AND credit_cents > 0 AND completed_at IS NOT NULL)
	)
);

CREATE INDEX loyalty_redemption_operations_user_idx
	ON loyalty_redemption_operations (user_id, created_at DESC);

-- The operation row is the idempotent ownership fence inside
-- redeem_loyalty_points. No pool reads or writes it directly, and its hidden
-- form UUID is not reporting data.
REVOKE ALL ON loyalty_redemption_operations FROM store, admin, reporting;

CREATE TRIGGER loyalty_entries_append_only
    BEFORE UPDATE OR DELETE ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION forbid_change('loyalty_entries_append_only');

-- The spendable balance: every live award plus the spends and clawbacks paired
-- with it. The old escape clause counted a spend forever while its award
-- lapsed, so an account drifted negative merely by the passage of time. Every
-- child now carries the lot's expiry and all of them leave together. Expiry is
-- applied HERE rather than by a job that might not have run.
CREATE VIEW loyalty_balances AS
    SELECT a.id AS account_id,
           -- The scalar subquery makes the stable shop_today() an InitPlan,
           -- evaluated once rather than once for every row in the ledger.
           coalesce(sum(e.points) FILTER (
               WHERE e.expires_on >= (SELECT shop_today())
           ), 0)::bigint AS points
    FROM store_credit_accounts a
    LEFT JOIN loyalty_entries e ON e.account_id = a.id
    GROUP BY a.id;

COMMENT ON VIEW loyalty_balances IS
    'Spendable points per account: each unexpired award lot net of the spends '
    'and clawbacks paired with it. Expiry is applied on read, never by a job '
    'that might not have run.';

-- A child entry may settle only the award lot it names, may not be dated away
-- from that lot, and the lot may never be overdrawn. A spend also cannot take
-- from an expired lot; a clawback may be recorded later for audit, but with the
-- same expired date it cannot alter today's balance.
CREATE FUNCTION loyalty_lot_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lot loyalty_entries%ROWTYPE;
    taken bigint;
BEGIN
    IF NEW.kind = 'award' THEN
        RETURN NEW;
    END IF;

    -- Every posting door takes this lock first. The trigger repeats it as the
    -- database authority so a future writer cannot race the existing doors.
    PERFORM 1 FROM store_credit_accounts WHERE id = NEW.account_id FOR UPDATE;

    SELECT * INTO lot FROM loyalty_entries WHERE id = NEW.lot_id;
    IF lot.kind IS DISTINCT FROM 'award' OR lot.account_id IS DISTINCT FROM NEW.account_id THEN
        RAISE EXCEPTION 'entry % names % which is not an award on this account', NEW.id, NEW.lot_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_is_an_award';
    END IF;

    IF NEW.expires_on IS DISTINCT FROM lot.expires_on THEN
        RAISE EXCEPTION 'entry % expires % against a lot expiring %',
            NEW.id, NEW.expires_on, lot.expires_on
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_expiry_matches';
    END IF;

    IF NEW.kind = 'spend' AND lot.expires_on < shop_today() THEN
        RAISE EXCEPTION 'lot % lapsed on %', lot.id, lot.expires_on
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_not_expired';
    END IF;

    SELECT coalesce(sum(-points), 0)::bigint INTO taken
    FROM loyalty_entries WHERE lot_id = NEW.lot_id;
    IF taken > lot.points THEN
        RAISE EXCEPTION 'lot % holds % and % has been settled against it', lot.id, lot.points, taken
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_lot_not_overdrawn';
    END IF;
    RETURN NEW;
END;
$$;

-- AFTER, so the sum it reads includes the row being checked.
CREATE CONSTRAINT TRIGGER loyalty_lot_guard
    AFTER INSERT ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION loyalty_lot_guard();

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

-- The view is created above member_spend so that LANGUAGE sql can resolve it;
-- the grant stays here because the admin role is created between those points.
GRANT SELECT ON order_refunds TO store, admin, reporting;

-- The points the next PAID return posting may claim. Payout timestamps cannot
-- define a durable order: now() is transaction-start time, so an early
-- transaction may commit after a later one and be sorted before an already
-- immutable clawback. Instead, reverse_return_points takes the account lock and
-- appends one delta: the points earned on all durably paid return money, less
-- requested_points already persisted by earlier clawbacks. Existing rows keep
-- their stored slice forever. The retry projection and posting door share this
-- one definition.
CREATE FUNCTION return_loyalty_points_allocation(p_return_request_id uuid)
RETURNS bigint
LANGUAGE sql STABLE AS $$
    WITH target AS (
        SELECT r.id, r.order_id,
               return_refundable_amount(r.id)::numeric AS amount,
               coalesce((
                   SELECT sum(rf.amount_cents) FROM refunds rf
                   WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
               ), 0)::numeric + coalesce((
                   SELECT sum(e.amount_cents) FROM store_credit_entries e
                   WHERE e.idempotency_key = 'return-credit:' || r.id::text
               ), 0)::numeric AS paid
        FROM return_requests r
        WHERE r.id = p_return_request_id
    ), award AS (
        SELECT e.points
        FROM loyalty_entries e JOIN target t ON t.order_id = e.order_id
        WHERE e.kind = 'award'
        ORDER BY e.created_at, e.id
        LIMIT 1
    ), order_total AS (
        SELECT greatest(
            coalesce(sum(ol.unit_price_cents::numeric * ol.quantity), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents, 0) AS amount
        FROM orders o JOIN target t ON t.order_id = o.id
        LEFT JOIN order_lines ol ON ol.order_id = o.id
        GROUP BY o.id
    ), return_money AS (
        SELECT r.id,
               return_refundable_amount(r.id)::numeric AS amount,
               coalesce(card.amount, 0) + coalesce(credit.amount, 0) AS paid
        FROM return_requests r JOIN target t ON t.order_id = r.order_id
        LEFT JOIN LATERAL (
            SELECT coalesce(sum(rf.amount_cents), 0)::numeric AS amount
            FROM refunds rf
            WHERE rf.return_request_id = r.id AND rf.status = 'succeeded'
        ) card ON true
        LEFT JOIN LATERAL (
            SELECT coalesce(sum(e.amount_cents), 0)::numeric AS amount
            FROM store_credit_entries e
            WHERE e.idempotency_key = 'return-credit:' || r.id::text
        ) credit ON true
        WHERE r.status IN ('approved', 'completed')
    ), paid_total AS (
        SELECT coalesce(sum(amount) FILTER (WHERE amount > 0 AND paid >= amount), 0) AS amount
        FROM return_money
    ), prior AS (
        SELECT coalesce(sum(e.requested_points), 0)::numeric AS points
        FROM loyalty_entries e JOIN target t ON t.order_id = e.order_id
        WHERE e.kind = 'clawback'
    ), existing AS (
        SELECT e.requested_points::bigint AS points
        FROM loyalty_entries e
        WHERE e.return_request_id = p_return_request_id AND e.kind = 'clawback'
        LIMIT 1
    )
    SELECT coalesce(
        (SELECT points FROM existing),
        (SELECT CASE
            WHEN t.amount <= 0 OR t.paid < t.amount OR ot.amount <= 0 THEN 0
            ELSE greatest(
                floor(a.points::numeric * least(pt.amount, ot.amount) / ot.amount)
                - pr.points,
                0
            )::bigint
         END
         FROM target t CROSS JOIN award a CROSS JOIN order_total ot
         CROSS JOIN paid_total pt CROSS JOIN prior pr),
        0
    )::bigint;
$$;

GRANT EXECUTE ON FUNCTION return_loyalty_points_allocation(uuid) TO admin;

-- Whether an approved return still has useful recovery work. This is the
-- ordering authority for the bounded back-office queue: without doing this
-- before LIMIT, fifty newer requests can hide an older customer whose approved
-- refund is still unpaid forever. Exact equality also keeps malformed overpaid
-- source rows visible for investigation instead of treating "at least paid" as
-- settled.
CREATE FUNCTION return_payout_outstanding(p_return_request_id uuid)
RETURNS boolean
LANGUAGE sql STABLE
SET search_path = pg_catalog, public, pg_temp AS $$
    WITH target AS (
        SELECT r.id, r.order_id, r.card_refund_cents, r.credit_refund_cents,
               return_refundable_amount(r.id)::bigint AS refundable
        FROM return_requests r
        WHERE r.id = p_return_request_id AND r.status = 'approved'
    ), facts AS (
        SELECT coalesce(t.card_refund_cents, 0)::bigint AS card_expected,
               coalesce(t.credit_refund_cents, 0)::bigint AS credit_expected,
               t.refundable,
               coalesce((
                   SELECT sum(rf.amount_cents)
                   FROM refunds rf
                   WHERE rf.return_request_id = t.id
                     AND rf.status = 'succeeded'
               ), 0)::bigint AS card_paid,
               coalesce((
                   SELECT sum(e.amount_cents)
                   FROM store_credit_entries e
                   WHERE e.idempotency_key = 'return-credit:' || t.id::text
               ), 0)::bigint AS credit_paid,
               EXISTS (
                   SELECT 1 FROM order_events e
                   WHERE e.return_request_id = t.id AND e.kind = 'refunded'
               ) AS event_recorded,
               return_loyalty_points_allocation(t.id) > 0
                   AND EXISTS (
                       SELECT 1 FROM loyalty_entries e
                       WHERE e.order_id = t.order_id AND e.kind = 'award'
                   )
                   AND NOT EXISTS (
                       SELECT 1 FROM loyalty_entries e
                       WHERE e.return_request_id = t.id AND e.kind = 'clawback'
                   ) AS points_outstanding
        FROM target t
    )
    SELECT coalesce((
        SELECT
            card_expected < 0
            OR credit_expected < 0
            OR card_expected + credit_expected <> refundable
            OR card_paid <> card_expected
            OR credit_paid <> credit_expected
            OR ((card_paid > 0 OR credit_paid > 0) AND NOT event_recorded)
            OR points_outstanding
        FROM facts
    ), false);
$$;

GRANT EXECUTE ON FUNCTION return_payout_outstanding(uuid) TO admin;

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
    SELECT mine.product_id, other.product_id, count(DISTINCT c.id)
    FROM committed_orders c
    JOIN order_lines mine ON mine.order_id = c.id
    JOIN order_lines other ON other.order_id = c.id
    WHERE mine.product_id IS NOT NULL
      AND other.product_id IS NOT NULL
      AND mine.product_id <> other.product_id
    GROUP BY mine.product_id, other.product_id;

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
    CONSTRAINT media_objects_size_positive
        CHECK (byte_size > 0 AND byte_size <= 8388608),
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

    INSERT INTO audit_events
        (actor_user_id, actor_id_snapshot, action, entity_table, entity_id,
         before, after, request_id)
    VALUES
        (p_actor, p_actor, p_action, p_entity_table, p_entity_id,
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
CREATE FUNCTION award_loyalty_points(p_order_id uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    v_account uuid;
    v_user uuid;
    v_total bigint;
    v_multiplier integer;
    v_points bigint;
BEGIN
    -- The order, not the role caller, owns every value carrying economic
    -- authority.  The fixed 365-day window and expiry are programme policy;
    -- changing them is a schema change, not an extra argument on a mint.
    SELECT o.user_id,
           (coalesce((SELECT sum(ol.unit_price_cents * ol.quantity)::bigint
                      FROM order_lines ol WHERE ol.order_id = o.id), 0)
            - o.discount_cents + o.shipping_cents + o.tax_cents)::bigint
    INTO v_user, v_total
    FROM orders o
    WHERE o.id = p_order_id AND order_is_committed(o.id)
    FOR UPDATE OF o;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'loyalty may only be awarded from a committed order'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_award_committed_order';
    END IF;

    IF v_user IS NULL THEN
        RETURN 0;
    END IF;

    -- This account row is the per-member linearization point. Capture already
    -- holds its own order, but two different orders have no common lock; both
    -- could otherwise read the same prior spend and award at the old tier. The
    -- no-op conflict update both creates the first account and waits on an
    -- existing one before member_spend observes committed payment facts.
    INSERT INTO store_credit_accounts (user_id)
    VALUES (v_user)
    ON CONFLICT (user_id) DO UPDATE SET user_id = excluded.user_id
    RETURNING id INTO v_account;

    SELECT coalesce((SELECT t.points_multiplier_bp FROM membership_tiers t
                     WHERE t.id = member_tier(v_user, 365, p_order_id)), 10000)
    INTO v_multiplier;
    v_points := floor(floor(greatest(v_total, 0)::numeric / 10000)
                      * v_multiplier::numeric / 10000)::bigint;
    -- An order that earns nothing is not an error. Raising here aborts the
    -- capture transaction and makes a valid provider webhook retry forever.
    IF v_points = 0 THEN
        RETURN 0;
    END IF;

    INSERT INTO loyalty_entries (account_id, kind, points, reason, idempotency_key,
                                 order_id, expires_on)
    VALUES (v_account, 'award', v_points, 'order', 'earn:' || p_order_id::text,
            p_order_id, shop_today() + 365)
    ON CONFLICT (idempotency_key) DO NOTHING;

    IF NOT FOUND THEN
        RETURN 0;
    END IF;
    RETURN v_points;
END;
$$;

-- Reverse only what remains of the award lot behind a return whose payout has
-- durably landed. The admin role names one return, never an order/points tuple:
-- those are economic facts derived under the return lock. That makes an
-- unrelated-return attachment and an inflated clawback unrepresentable at the
-- SECURITY DEFINER boundary.
CREATE FUNCTION reverse_return_points(p_return_request_id uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    lot record;
	v_order_id uuid;
	v_refundable bigint;
	v_paid bigint;
	v_points bigint;
    v_remaining bigint;
    v_actual bigint;
BEGIN
	-- Resolve the aggregate without taking its child lock, then own the order
	-- before re-locking and revalidating the return. CompleteReturn and erasure
	-- use the same order -> return direction. The later loyalty INSERT takes an
	-- order-FK KEY SHARE lock, while erasure's user deletion may update the
	-- account FK; taking account before order here creates an account <-> order
	-- deadlock with erase_user.
	SELECT r.order_id INTO v_order_id
	FROM return_requests r
	WHERE r.id = p_return_request_id;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'points can only be reversed for an approved return %',
			p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_return_approved';
	END IF;

	PERFORM 1 FROM orders o WHERE o.id = v_order_id FOR UPDATE;

	SELECT return_refundable_amount(r.id)
	INTO v_refundable
	FROM return_requests r
	WHERE r.id = p_return_request_id
	  AND r.order_id = v_order_id
	  AND r.status IN ('approved', 'completed')
	FOR UPDATE OF r;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'points can only be reversed for an approved return %',
			p_return_request_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_return_approved';
	END IF;

	SELECT coalesce((
		SELECT sum(r.amount_cents) FROM refunds r
		WHERE r.return_request_id = p_return_request_id
		  AND r.status = 'succeeded'
	), 0) + coalesce((
		SELECT sum(e.amount_cents) FROM store_credit_entries e
		WHERE e.idempotency_key = 'return-credit:' || p_return_request_id::text
	), 0)
	INTO v_paid;
	IF v_refundable <= 0 OR v_paid < v_refundable THEN
		RAISE EXCEPTION 'return % has paid %, below its refundable %',
			p_return_request_id, v_paid, v_refundable
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_return_paid';
	END IF;

    SELECT e.id, e.account_id, e.points, e.expires_on INTO lot
    FROM loyalty_entries e
    WHERE e.order_id = v_order_id AND e.kind = 'award'
    ORDER BY e.created_at, e.id
    LIMIT 1;
    IF NOT FOUND THEN
        RETURN 0;
    END IF;

    -- The strongest lock is taken before the remaining amount is read. Every
    -- posting door takes this lock, and the lot trigger repeats the authority
    -- for a future direct writer.
    PERFORM 1 FROM store_credit_accounts WHERE id = lot.account_id FOR UPDATE;
	IF EXISTS (
		SELECT 1 FROM loyalty_entries e
		WHERE e.return_request_id = p_return_request_id AND e.kind = 'clawback'
	) THEN
		RETURN 0;
	END IF;
	v_points := return_loyalty_points_allocation(p_return_request_id);
	IF coalesce(v_points, 0) <= 0 THEN
		RETURN 0;
	END IF;

    SELECT greatest(lot.points + coalesce(sum(e.points), 0), 0)::bigint
    INTO v_remaining
    FROM loyalty_entries e
    WHERE e.lot_id = lot.id;
    v_actual := least(v_points, v_remaining);

    INSERT INTO loyalty_entries (
        account_id, kind, points, reason, idempotency_key, order_id,
        lot_id, requested_points, return_request_id, expires_on
    ) VALUES (
        lot.account_id, 'clawback', -v_actual, 'return',
        'return:' || p_return_request_id::text, v_order_id,
        lot.id, v_points, p_return_request_id, lot.expires_on
    )
    ON CONFLICT (idempotency_key) DO NOTHING;

    IF NOT FOUND THEN
        RETURN 0;
    END IF;
    RETURN v_actual;
END;
$$;

-- Claw back the award lot behind an order the back office has cancelled. Unlike
-- reverse_return_points, this keys on the order alone: no return exists.
CREATE FUNCTION reverse_order_points(p_order_id uuid) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    lot record;
    v_status text;
    v_points bigint;
    v_remaining bigint;
    v_actual bigint;
BEGIN
    SELECT o.fulfillment_status INTO v_status
    FROM orders o
    WHERE o.id = p_order_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN 0;
    END IF;
    IF v_status <> 'cancelled' THEN
        RAISE EXCEPTION 'loyalty can only be reversed on a cancelled order %',
            p_order_id
            USING ERRCODE = 'check_violation',
                  CONSTRAINT = 'loyalty_clawback_cancelled_order';
    END IF;

    SELECT e.id, e.account_id, e.points, e.expires_on INTO lot
    FROM loyalty_entries e
    WHERE e.order_id = p_order_id AND e.kind = 'award'
    ORDER BY e.created_at, e.id
    LIMIT 1;
    IF NOT FOUND THEN
        RETURN 0;
    END IF;

    PERFORM 1 FROM store_credit_accounts WHERE id = lot.account_id FOR UPDATE;
    IF EXISTS (
        SELECT 1 FROM loyalty_entries e
        WHERE e.idempotency_key = 'cancel:' || p_order_id::text
    ) THEN
        RETURN 0;
    END IF;

    v_points := lot.points;
    IF v_points <= 0 THEN
        RETURN 0;
    END IF;

    SELECT greatest(lot.points + coalesce(sum(e.points), 0), 0)::bigint
    INTO v_remaining
    FROM loyalty_entries e
    WHERE e.lot_id = lot.id;
    v_actual := least(v_points, v_remaining);

    INSERT INTO loyalty_entries (
        account_id, kind, points, reason, idempotency_key, order_id,
        lot_id, requested_points, expires_on
    ) VALUES (
        lot.account_id, 'clawback', -v_actual, 'cancelled',
        'cancel:' || p_order_id::text, p_order_id,
        lot.id, v_points, lot.expires_on
    )
    ON CONFLICT (idempotency_key) DO NOTHING;

    IF NOT FOUND THEN
        RETURN 0;
    END IF;
    RETURN v_actual;
END;
$$;

COMMENT ON FUNCTION reverse_order_points(uuid) IS
    'Claw back the points a cancelled order awarded. Idempotent on the order; '
    'records requested_points even when the lot was wholly consumed.';

-- Spend points and post the store credit they bought, in ONE transaction. Two
-- statements would let the points go and the credit not arrive.
CREATE FUNCTION redeem_loyalty_points(
    p_user_id    uuid,
    p_points     bigint,
    p_operation_id uuid
) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    lot record;
	v_operation loyalty_redemption_operations%ROWTYPE;
	v_key text;
    v_left bigint;
    v_take bigint;
    v_cents bigint;
    v_account_id uuid;
    v_n integer := 0;
BEGIN
	IF p_operation_id IS NULL
	   OR p_operation_id = '00000000-0000-0000-0000-000000000000'::uuid THEN
		RAISE EXCEPTION 'redemption requires a non-zero operation id'
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_redemption_operation_owner';
	END IF;
	IF NOT EXISTS (SELECT 1 FROM store_credit_accounts WHERE user_id = p_user_id) THEN
		RAISE EXCEPTION 'user % has no loyalty account', p_user_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_redemption_owner';
	END IF;
	-- The hidden UUID is read-only form state, so GET does not write an unbounded
	-- pile of abandoned operations. First POST binds it to this owner in the same
	-- transaction as the ledger effects; any failure rolls the claim back.
	INSERT INTO loyalty_redemption_operations (id, user_id)
	VALUES (p_operation_id, p_user_id)
	ON CONFLICT (id) DO NOTHING;
	SELECT op.* INTO v_operation
	FROM loyalty_redemption_operations op
	WHERE op.id = p_operation_id AND op.user_id = p_user_id
	FOR UPDATE;
	IF NOT FOUND THEN
		RAISE EXCEPTION 'redemption operation % is not owned by user %',
			p_operation_id, p_user_id
			USING ERRCODE = 'check_violation',
			      CONSTRAINT = 'loyalty_redemption_operation_owner';
	END IF;
	IF v_operation.completed_at IS NOT NULL THEN
		IF v_operation.points <> p_points THEN
			RAISE EXCEPTION 'redemption operation % collides with different terms',
				p_operation_id
				USING ERRCODE = 'check_violation',
				      CONSTRAINT = 'loyalty_redemption_attribution';
		END IF;
		RETURN v_operation.credit_cents;
	END IF;

    -- The database owns the exchange rate.  Points must be a whole NT$1 unit,
    -- meet the published minimum, and fit the store-credit ledger ceiling.
    IF p_points < 100 OR p_points % 10 <> 0 OR p_points > 1000000000 THEN
        RAISE EXCEPTION 'invalid redemption amount: % points', p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_points_nonzero';
    END IF;
    v_cents := (p_points / 10) * 100;
	v_key := 'points:' || p_operation_id::text;

    -- The account is locked HERE, before anything is inserted. An INSERT takes
    -- FOR KEY SHARE for the foreign key and the AFTER trigger then wants FOR
    -- UPDATE — an upgrade, and a deadlock that LOOKS like the guard working.
    SELECT id INTO v_account_id FROM store_credit_accounts
    WHERE user_id = p_user_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'user % has no loyalty account', p_user_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_redemption_owner';
    END IF;

    -- FIFO by soonest expiry costs the customer least. Two awards can expire on
    -- the same day, so created_at then id are the deterministic tie-break. One
    -- redemption spanning lots is one INSERT per settled fact; no award row is
    -- ever updated.
    v_left := p_points;
    FOR lot IN
        SELECT e.id, e.expires_on,
               (e.points + coalesce((SELECT sum(s.points)
                                      FROM loyalty_entries s
                                      WHERE s.lot_id = e.id), 0))::bigint AS remaining
        FROM loyalty_entries e
        WHERE e.account_id = v_account_id
          AND e.kind = 'award'
          AND e.expires_on >= shop_today()
        ORDER BY e.expires_on, e.created_at, e.id
    LOOP
        EXIT WHEN v_left = 0;
        CONTINUE WHEN lot.remaining <= 0;
        v_take := least(v_left, lot.remaining);
        v_n := v_n + 1;
        INSERT INTO loyalty_entries (account_id, kind, points, reason,
                                     idempotency_key, expires_on, lot_id)
        VALUES (v_account_id, 'spend', -v_take, 'redeem',
                v_key || '#' || v_n::text, lot.expires_on, lot.id);
        v_left := v_left - v_take;
    END LOOP;

    IF v_left > 0 THEN
        RAISE EXCEPTION 'account % is short % of % points', v_account_id, v_left, p_points
            USING ERRCODE = 'check_violation', CONSTRAINT = 'loyalty_entries_within_balance';
    END IF;

    -- The credit, in the same transaction, with a prefixed key so a redemption
    -- and an award of the same id cannot collide.
    INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
	VALUES (v_account_id, v_cents, 'points', 'redeem:' || v_key);

	UPDATE loyalty_redemption_operations
	SET points = p_points, credit_cents = v_cents, completed_at = now()
	WHERE id = p_operation_id;

    RETURN v_cents;
END;
$$;

GRANT EXECUTE ON FUNCTION award_loyalty_points(uuid) TO store;
GRANT EXECUTE ON FUNCTION reverse_return_points(uuid) TO admin;
GRANT EXECUTE ON FUNCTION reverse_order_points(uuid) TO admin;
GRANT EXECUTE ON FUNCTION redeem_loyalty_points(uuid, bigint, uuid) TO store;

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
    order_shipments, order_shipment_lines, invoice_documents, invoice_operations,
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
    invoice_documents, invoice_document_lines, invoice_operations, user_identities
    FROM admin;
REVOKE INSERT ON payment_webhook_events FROM admin;

-- Tax history is never writable a column at a time.  The SECURITY DEFINER doors
-- above file header+lines atomically and expose only the allowance state changes
-- used by the provider workflow.
GRANT EXECUTE ON FUNCTION
    claim_invoice_issue(text, uuid, text),
    claim_invoice_allowance(uuid, uuid, uuid, text),
    claim_invoice_void(uuid, text, uuid, text),
    lease_invoice_operation(uuid, uuid, interval),
    mark_invoice_operation_sent(uuid, uuid),
    authorize_invoice_allowance_resend(uuid, uuid, text),
    reschedule_invoice_operation(uuid, uuid, text, interval),
    reconcile_invalid_invoice_allowance(uuid, uuid, uuid, text, text,
                                        timestamptz, bigint, text[], integer[],
                                        bigint[], bigint[]),
    record_invalid_invoice_allowance(uuid, uuid, text, text, timestamptz,
                                     bigint, text[], integer[], bigint[], bigint[]),
    alarm_invoice_operation(uuid, uuid, text),
    reject_invoice_operation(uuid, uuid, text),
    settle_invoice_issue(uuid, uuid, text, text, timestamptz,
                         text[], integer[], bigint[], bigint[]),
    settle_invoice_allowance(uuid, uuid, text, timestamptz,
                             text[], integer[], bigint[], bigint[]),
    settle_invoice_void(uuid, uuid)
    TO admin;

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
GRANT INSERT (id, question_id, user_id, body, created_at),
      UPDATE (id, question_id, user_id, body, created_at)
    ON product_answers TO store;

REVOKE INSERT, UPDATE ON return_requests FROM store;
GRANT INSERT (id, order_id, requested_by_user_id, reason, created_at),
      UPDATE (id, order_id, requested_by_user_id, reason, created_at)
    ON return_requests TO store;

-- Checkout appends the purchased snapshot once. Catalogue identities are
-- retained for the lifetime of the line; retirement never rewrites them.
REVOKE INSERT, UPDATE ON order_lines FROM store;
GRANT INSERT (id, order_id, product_id, variant_id, sku, product_name,
              variant_label, warranty_note, warranty_months,
              unit_price_cents, quantity, position)
    ON order_lines TO store;

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

-- The back office can retire live catalogue rows; it cannot rewrite or append
-- the immutable purchased snapshots which reference them.
REVOKE INSERT, UPDATE ON order_lines FROM admin;

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
