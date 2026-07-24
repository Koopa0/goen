-- goen — initial schema.
--
-- This file defines the whole database. goen has not shipped, so there is no
-- deployed schema to evolve from and no reason to spread the design across a
-- migration per feature; later migrations change this design, they do not
-- assemble it.
--
--
-- WHAT THE DATABASE IS RESPONSIBLE FOR
--
-- Application code is where rules go to be forgotten: a second write path
-- appears, a retry runs a step twice, a background job skips the check the
-- handler did. So every rule that would corrupt money, stock or history if
-- broken is enforced here, where there is no second path.
--
-- That means three kinds of guard, and the difference matters when reading:
--
--   CHECK            a single row cannot be wrong on its own terms.
--   UNIQUE / FK      a row cannot contradict another row's existence.
--   TRIGGER          a rule that spans rows and therefore needs a lock to be
--                    correct under concurrency. Each locks its aggregate root
--                    before it reads, so two concurrent writers serialise
--                    instead of both passing a stale test. Each raises with an
--                    explicit CONSTRAINT name so a caller — and a test — can
--                    name the rule that refused it.
--
--
-- NORMALISATION
--
-- Relations are in BCNF apart from two deliberate exceptions, both recorded
-- where they occur:
--
--   * Singleton discriminators. `currency` is always TWD and `provider` is
--     always stripe, so ∅ → currency holds and ∅ is not a superkey. The
--     columns stay because a financial row that does not say what it is
--     denominated in is worse than a formal blemish, and both are pinned by a
--     CHECK so the value cannot drift while the column is decorative.
--
--   * Read projections. `product_variants.stock_quantity` and
--     `product_search_documents` are derived, and say so. They exist because
--     the alternative is aggregating a ledger on every page view. Each has
--     exactly one writer, named at the definition.
--
-- Otherwise nothing derivable is stored: no order total, no line total, no
-- credit balance, no rating average. Those would introduce a dependency whose
-- determinant is not a key, and — the reason that matters — would let a row
-- contradict the rows it claims to summarise.
--
-- Snapshots are not redundancy. `order_lines.product_name` is not a copy of
-- the catalogue; the catalogue says what a product IS, the snapshot says what
-- was BOUGHT. There is no functional dependency from variant_id to the
-- snapshot, because the same variant appears in two orders under two names.
--
--
-- CONVENTIONS
--
-- Keys        uuid v7 via PostgreSQL 18's uuidv7(). Time-ordered, so inserts
--             stay at the right of the index instead of scattering the way v4
--             does, while still not being guessable from outside.
-- Money       bigint minor units. Stripe charges TWD with two decimals, so
--             NT$33,900 is 3390000. Every amount column is named *_cents.
--             bigint rather than integer because int32 stops at NT$21,474,836.
-- Time        timestamptz everywhere.
-- Text        text, never varchar(n).
-- Presence    `~ '[^[:space:]]'` rather than `length(btrim(x)) > 0`. btrim
--             strips spaces only, so a lone tab passes the second test.
-- Closed sets text + CHECK, not an enum type: adding a value is a constraint
--             swap rather than a type migration, and \d shows what is legal.
-- Deletion    RESTRICT by default. Financial and fulfilment history is not
--             deletable at all — see the append-only section.

-- squawk-ignore-file prefer-bigint-over-int
-- squawk-ignore-file prefer-bigint-over-smallint
-- Every money column here is bigint, which is the case those rules exist for.
-- What remains integer is quantities, stock levels, sort positions and pixel
-- dimensions; smallint holds a 1-to-5 rating and a unit ordinal. All are bound
-- to three or four digits by their own CHECKs.
-- squawk-ignore-file adding-foreign-key-constraint
-- squawk-ignore-file constraint-missing-not-valid
-- Those two rules exist for a migration that alters a table people are already
-- writing to. This file creates the whole schema from nothing, so the scan a
-- new constraint triggers has no rows to scan and its lock has no writer to
-- block. NOT VALID would only leave the constraints unverified. The rules stay
-- in force for every later migration, which is a different file.
-- squawk-ignore-file require-concurrent-index-creation
-- Every index is on a table created a few statements above it, so there are no
-- rows to lock. CONCURRENTLY additionally cannot run inside the transaction
-- golang-migrate wraps this file in.

SET lock_timeout = '3s';
SET statement_timeout = '120s';

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- ============================================================================
-- Shared machinery
-- ============================================================================

-- Keeping updated_at truthful in application code means every write path has
-- to remember. A trigger cannot forget.
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;

-- Refuses any UPDATE or DELETE. Applied to the tables whose rows are history:
-- once written, a ledger entry, an audit record or an issued document is a
-- fact about the past, and correcting it means writing another row.
CREATE FUNCTION forbid_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; correct it with a new row', TG_TABLE_NAME
        USING ERRCODE = 'check_violation', CONSTRAINT = TG_ARGV[0];
END;
$$;

-- ============================================================================
-- Catalogue
-- ============================================================================

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

-- Categories are a tree: 配件 is the navigation parent of 充電配件 and 周邊,
-- which is why the header offers six entries while the home tiles show six
-- different ones.
CREATE TABLE categories (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    parent_id  uuid REFERENCES categories (id) ON DELETE RESTRICT,
    slug       text NOT NULL,
    name       text NOT NULL,
    icon_key   text,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT categories_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT categories_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT categories_not_own_parent CHECK (parent_id IS DISTINCT FROM id)
);

CREATE UNIQUE INDEX categories_slug_key ON categories (slug);
CREATE INDEX categories_parent_id_idx ON categories (parent_id);

CREATE TRIGGER categories_set_updated_at
    BEFORE UPDATE ON categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- categories_not_own_parent stops A → A and nothing else. A → B → A passes it
-- row by row and then hangs every recursive walk of the tree, so the cycle has
-- to be tested against the ancestors the new parent already has.
--
-- The advisory lock is what makes it correct under concurrency: without it,
-- "set A.parent = B" and "set B.parent = A" each look acyclic in their own
-- snapshot and commit into a cycle.
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
    warranty_note text,
    status        text NOT NULL DEFAULT 'draft',
    published_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT products_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT products_status_known CHECK (status IN ('draft', 'active', 'archived')),
    -- 本週新品 is a query over published_at, so an active product without one
    -- is invisible rather than merely untidy.
    CONSTRAINT products_active_is_published
        CHECK (status <> 'active' OR published_at IS NOT NULL)
);

CREATE UNIQUE INDEX products_slug_key ON products (slug);
-- Referenced by the composite foreign key that keeps a variant's options on
-- the same product as the variant.
CREATE UNIQUE INDEX products_id_self_key ON products (id, id);
CREATE INDEX products_brand_id_idx ON products (brand_id);
CREATE INDEX products_category_id_idx ON products (category_id);

-- The listing page's default read: active products in a category, newest
-- first. id is the tie-breaker — a batch published in one transaction shares a
-- timestamp to the microsecond, and without it keyset pagination silently
-- skips the rest of the batch.
CREATE INDEX products_category_published_idx
    ON products (category_id, published_at DESC, id DESC)
    WHERE status = 'active';

-- The same read narrowed by the brand facet.
CREATE INDEX products_category_brand_published_idx
    ON products (category_id, brand_id, published_at DESC, id DESC)
    WHERE status = 'active';

CREATE TRIGGER products_set_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE product_images (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id  uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    storage_key text NOT NULL,
    -- Required, not nullable: the admin upload control states that every image
    -- needs alt text, and a nullable column would make that a suggestion.
    alt_text    text NOT NULL,
    width       integer,
    height      integer,
    position    integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_images_storage_key_present CHECK (storage_key ~ '[^[:space:]]'),
    CONSTRAINT product_images_alt_present CHECK (alt_text ~ '[^[:space:]]'),
    CONSTRAINT product_images_width_positive CHECK (width IS NULL OR width > 0),
    CONSTRAINT product_images_height_positive CHECK (height IS NULL OR height > 0),
    CONSTRAINT product_images_position_non_negative CHECK (position >= 0)
);

CREATE UNIQUE INDEX product_images_position_key ON product_images (product_id, position);
-- One stored object is one set of pixels. Without this, the same key could be
-- recorded as 800x800 on one row and 1200x1200 on another, which is a
-- dependency on a non-key column and a bug the first time either is trusted.
CREATE UNIQUE INDEX product_images_storage_key_key ON product_images (storage_key);

-- 顏色 / 容量: the axes a product varies along.
CREATE TABLE product_options (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    name       text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_options_name_present CHECK (name ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX product_options_name_key ON product_options (product_id, name);
-- Referenced by variant_option_values, so an option can only be paired with a
-- variant of the same product.
CREATE UNIQUE INDEX product_options_product_key ON product_options (product_id, id);

-- 星霧藍 / 256GB: the values on one axis.
CREATE TABLE product_option_values (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Denormalised from product_options so the composite key below can carry
    -- the product through to variant_option_values. Pinned by the composite
    -- foreign key, so it cannot disagree with the option's own product.
    product_id uuid NOT NULL,
    option_id  uuid NOT NULL,
    value      text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_option_values_value_present CHECK (value ~ '[^[:space:]]'),
    CONSTRAINT product_option_values_option_fk
        FOREIGN KEY (product_id, option_id) REFERENCES product_options (product_id, id)
        ON DELETE CASCADE
);

CREATE UNIQUE INDEX product_option_values_value_key ON product_option_values (option_id, value);
CREATE UNIQUE INDEX product_option_values_option_key
    ON product_option_values (product_id, option_id, id);

-- The sellable unit. Price and stock live here, never on the product: the
-- admin variant table prices 星霧藍 256GB and 曜石黑 256GB independently.
CREATE TABLE product_variants (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id             uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    sku                    text NOT NULL,
    price_cents            bigint NOT NULL,
    -- The struck-through "was" price. NULL means not on sale; the discount
    -- percentage is computed from the pair, never stored.
    compare_at_price_cents bigint,
    -- A projection of inventory_movements, maintained only by
    -- record_inventory_movement(). Nothing else may write it.
    stock_quantity         integer NOT NULL DEFAULT 0,
    safety_stock           integer NOT NULL DEFAULT 0,
    preorder_release_on    date,
    position               integer NOT NULL DEFAULT 0,
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_variants_sku_format CHECK (sku ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'),
    -- The ceiling is a real one: NT$100,000,000 is far above any 3C product
    -- and far below the point where quantity x price can overflow bigint.
    CONSTRAINT product_variants_price_in_range
        CHECK (price_cents >= 0 AND price_cents <= 10000000000),
    CONSTRAINT product_variants_stock_non_negative CHECK (stock_quantity >= 0),
    CONSTRAINT product_variants_safety_stock_non_negative CHECK (safety_stock >= 0),
    -- A "was" price at or below the price it replaced is not a discount;
    -- rendering one would be a false claim about a saving.
    CONSTRAINT product_variants_compare_at_is_higher
        CHECK (compare_at_price_cents IS NULL OR compare_at_price_cents > price_cents)
);

CREATE UNIQUE INDEX product_variants_sku_key ON product_variants (sku);
CREATE UNIQUE INDEX product_variants_position_key ON product_variants (product_id, position);
CREATE UNIQUE INDEX product_variants_product_key ON product_variants (product_id, id);

-- "Sellable, cheapest first" — the listing page's price facet and price sort.
CREATE INDEX product_variants_sellable_price_idx
    ON product_variants (product_id, price_cents, id)
    WHERE is_active AND stock_quantity > safety_stock;

-- The admin's low-stock queue. The predicate has to be the column comparison
-- itself; an index on stock_quantity alone cannot answer "below its own
-- safety level", which is what the screen asks.
CREATE INDEX product_variants_low_stock_idx
    ON product_variants (product_id, stock_quantity)
    WHERE is_active AND stock_quantity <= safety_stock;

CREATE TRIGGER product_variants_set_updated_at
    BEFORE UPDATE ON product_variants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Which point on each axis a variant sits at.
--
-- product_id and option_id are carried explicitly so the composite foreign
-- keys can enforce what a pair of surrogate keys cannot: that the value
-- belongs to an option of the same product as the variant. The primary key is
-- (variant_id, option_id), so one variant cannot be two colours at once.
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

-- The facet lookup runs the other way: given 256GB, which variants have it.
CREATE INDEX variant_option_values_value_variant_idx
    ON variant_option_values (option_value_id, variant_id);
-- The primary key leads with variant_id but the composite foreign key is
-- (product_id, variant_id), so the key cannot serve it.
CREATE INDEX variant_option_values_product_variant_idx
    ON variant_option_values (product_id, variant_id);
CREATE INDEX variant_option_values_product_option_idx
    ON variant_option_values (product_id, option_id, option_value_id);

-- The rows behind 完整規格 and the comparison table.
CREATE TABLE product_specs (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    label      text NOT NULL,
    value      text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_specs_label_present CHECK (label ~ '[^[:space:]]'),
    CONSTRAINT product_specs_value_present CHECK (value ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX product_specs_position_key ON product_specs (product_id, position);
CREATE INDEX product_specs_label_idx ON product_specs (label);

-- A projection: product name, brand, SKUs, option values and spec text
-- flattened into one searchable document.
--
-- It is derived, and exists because the header search matches substrings
-- across five tables — "搜尋商品、品牌或規格" — and no B-tree over the
-- normalised tables can answer that. Rebuild it from its sources at any time.
CREATE TABLE product_search_documents (
    product_id  uuid PRIMARY KEY REFERENCES products (id) ON DELETE CASCADE,
    body        text NOT NULL,
    refreshed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_search_documents_body_present CHECK (body ~ '[^[:space:]]')
);

-- Trigram rather than tsvector: goen's corpus is Traditional Chinese product
-- copy mixed with model numbers, where the useful query is a substring
-- ("Pixelight 9", "65W") and there are no word boundaries for a text-search
-- parser to find.
CREATE INDEX product_search_documents_body_idx
    ON product_search_documents USING gin (body gin_trgm_ops);

-- ============================================================================
-- People
-- ============================================================================

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
    -- Stored trimmed, so the unique index below cannot be sidestepped with a
    -- leading space.
    CONSTRAINT users_email_trimmed CHECK (email = btrim(email)),
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
    secret_encrypted bytea NOT NULL,
    confirmed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- Sessions store a hash, never the cookie value: a leaked table must not be a
-- set of usable credentials.
CREATE TABLE sessions (
    token_hash bytea PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent text,
    ip         inet,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CONSTRAINT sessions_expiry_after_creation CHECK (expires_at > created_at)
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

CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

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
-- "Default" is singular, said in the one place a forgetful code path cannot
-- bypass.
CREATE UNIQUE INDEX addresses_one_default_per_user
    ON addresses (user_id)
    WHERE is_default;

CREATE TRIGGER addresses_set_updated_at
    BEFORE UPDATE ON addresses
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ============================================================================
-- Store credit — 購物金
--
-- A ledger, so the balance is the sum of its explanations and cannot drift
-- from them. The account row exists to be locked: without a single row to take
-- FOR UPDATE, two concurrent spends each read the same balance and both pass.
-- ============================================================================

CREATE TABLE store_credit_accounts (
    user_id    uuid PRIMARY KEY REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE store_credit_entries (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id        uuid NOT NULL REFERENCES store_credit_accounts (user_id) ON DELETE RESTRICT,
    amount_cents   bigint NOT NULL,
    reason         text NOT NULL,
    -- The caller's name for this posting. A retried checkout submits the same
    -- key and gets a unique violation instead of a second debit.
    idempotency_key text NOT NULL,
    -- The order this posting settles, when there is one. The foreign key is
    -- added after `orders` is created, further down.
    order_id       uuid,
    reverses_id    uuid REFERENCES store_credit_entries (id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT store_credit_entries_amount_non_zero CHECK (amount_cents <> 0),
    CONSTRAINT store_credit_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT store_credit_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX store_credit_entries_idempotency_key
    ON store_credit_entries (idempotency_key);
CREATE INDEX store_credit_entries_user_idx ON store_credit_entries (user_id, created_at DESC);
CREATE INDEX store_credit_entries_reverses_idx ON store_credit_entries (reverses_id);
CREATE INDEX store_credit_entries_order_idx ON store_credit_entries (order_id);

-- Locks the account before it sums, so two concurrent debits serialise rather
-- than both reading a balance that is about to be spent.
CREATE FUNCTION store_credit_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    balance bigint;
BEGIN
    PERFORM 1 FROM store_credit_accounts WHERE user_id = NEW.user_id FOR UPDATE;

    SELECT coalesce(sum(amount_cents), 0) INTO balance
    FROM store_credit_entries
    WHERE user_id = NEW.user_id AND id <> NEW.id;

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
-- The movements table is the truth; product_variants.stock_quantity is a
-- projection of it. Both are written by one function, which holds the variant
-- row while it does so — the reason a bare `SET stock = stock - 1` is not
-- enough is that two sessions can each read 1 and each write 0.
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
        'receipt',      -- 進貨
        'sale',         -- 出貨扣減
        'release',      -- 取消或逾期釋放
        'return',       -- 退貨入庫
        'adjustment'    -- 人工盤點
    )),
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

-- The only writer of stock_quantity.
--
-- The UPDATE carries the availability test in its own WHERE clause, so the
-- read and the write are one statement against one locked row: a second
-- session cannot observe the stock this one is about to take. Zero rows
-- updated means there was not enough, and that is reported rather than
-- silently allowed to go negative.
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
    UPDATE product_variants
    SET stock_quantity = stock_quantity + p_delta
    WHERE id = p_variant_id
      AND stock_quantity + p_delta >= 0
    RETURNING stock_quantity INTO remaining;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'variant % cannot absorb a movement of %', p_variant_id, p_delta
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_never_negative';
    END IF;

    INSERT INTO inventory_movements
        (variant_id, delta, reason, source_type, source_id, idempotency_key, actor_user_id)
    VALUES
        (p_variant_id, p_delta, p_reason, p_source_type, p_source_id, p_idempotency_key, p_actor);

    RETURN remaining;
END;
$$;

-- A hold taken while the visitor is on the Stripe Payment Element. Stock is
-- decremented when the reservation is created and returned when it expires, so
-- an abandoned checkout cannot keep an item out of the shop indefinitely and a
-- completed one never finds the stock gone.
CREATE TABLE inventory_reservations (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    -- The foreign key is added after `orders` is created, further down: a hold
    -- is taken during checkout, so this table has to exist before the order it
    -- will belong to.
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
        CHECK ((state = 'held') = (settled_at IS NULL))
);

CREATE UNIQUE INDEX inventory_reservations_order_variant_key
    ON inventory_reservations (order_id, variant_id);
CREATE INDEX inventory_reservations_variant_idx ON inventory_reservations (variant_id);
-- The sweeper's read: holds that have run out of time.
CREATE INDEX inventory_reservations_expiring_idx
    ON inventory_reservations (expires_at)
    WHERE state = 'held';

-- ============================================================================
-- Cart and wishlist
-- ============================================================================

-- A cart exists before an account does: guest checkout is supported, so the
-- cart is identified by a cookie token and adopted on sign-in.
CREATE TABLE carts (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX carts_token_hash_key ON carts (token_hash);
-- One cart per account. Without this a merge that runs twice leaves the
-- customer with two carts and no way to say which is theirs.
CREATE UNIQUE INDEX carts_one_per_user ON carts (user_id) WHERE user_id IS NOT NULL;
-- The index above is partial, so it cannot serve the delete of an account that
-- never had a cart adopted.
CREATE INDEX carts_user_id_idx ON carts (user_id);

CREATE TRIGGER carts_set_updated_at
    BEFORE UPDATE ON carts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One row per variant: adding the same variant twice raises the quantity
-- rather than making a second line, so the pair is the key.
CREATE TABLE cart_items (
    cart_id    uuid NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
    variant_id uuid NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    quantity   integer NOT NULL,
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cart_id, variant_id),
    CONSTRAINT cart_items_quantity_in_range CHECK (quantity > 0 AND quantity <= 999)
);

CREATE INDEX cart_items_variant_id_idx ON cart_items (variant_id);

-- Server-issued, so a double-click or an HTTP retry resolves to the order it
-- already created instead of a second order and a second PaymentIntent.
CREATE TABLE checkout_attempts (
    idempotency_key text PRIMARY KEY,
    cart_id         uuid REFERENCES carts (id) ON DELETE SET NULL,
    -- Filled in once the attempt has produced an order. The foreign key is
    -- added after `orders` exists, further down.
    order_id        uuid,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT checkout_attempts_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

CREATE UNIQUE INDEX checkout_attempts_order_key ON checkout_attempts (order_id)
    WHERE order_id IS NOT NULL;
-- The index above is partial, so it cannot serve the foreign key's own lookup.
CREATE INDEX checkout_attempts_order_id_idx ON checkout_attempts (order_id);
CREATE INDEX checkout_attempts_cart_idx ON checkout_attempts (cart_id);

CREATE TABLE wishlist_items (
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, product_id)
);

CREATE INDEX wishlist_items_product_id_idx ON wishlist_items (product_id);

-- 到貨通知. Keyed by variant and address so a guest can ask too.
CREATE TABLE stock_notifications (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    variant_id  uuid NOT NULL REFERENCES product_variants (id) ON DELETE CASCADE,
    user_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    email       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    notified_at timestamptz,
    CONSTRAINT stock_notifications_email_present CHECK (email ~ '[^[:space:]]')
);

-- Asking twice for the same restock is one request.
CREATE UNIQUE INDEX stock_notifications_pending_key
    ON stock_notifications (variant_id, lower(email))
    WHERE notified_at IS NULL;
-- The partial index above covers only pending rows, so it cannot serve the
-- delete of a variant that has already notified someone.
CREATE INDEX stock_notifications_variant_id_idx ON stock_notifications (variant_id);
CREATE INDEX stock_notifications_user_id_idx ON stock_notifications (user_id);

-- ============================================================================
-- Reviews
-- ============================================================================

CREATE TABLE product_reviews (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id           uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    -- Nullable: a customer exercising their right to erasure must not take a
    -- published review with them, and must not be blocked by having left one.
    user_id              uuid REFERENCES users (id) ON DELETE SET NULL,
    rating               smallint NOT NULL,
    title                text,
    body                 text NOT NULL,
    is_verified_purchase boolean NOT NULL DEFAULT false,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_reviews_rating_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT product_reviews_body_present CHECK (body ~ '[^[:space:]]')
);

-- One review per product per person. The average and the count are computed
-- from these rows and never stored on the product.
CREATE UNIQUE INDEX product_reviews_author_key ON product_reviews (product_id, user_id);
CREATE INDEX product_reviews_product_created_idx ON product_reviews (product_id, created_at DESC);
CREATE INDEX product_reviews_user_id_idx ON product_reviews (user_id);

-- ============================================================================
-- Shipping
--
-- Methods are versioned because a fee is a promise made at a moment. Changing
-- 宅配 from NT$80 to NT$100 must not make last month's orders unexplainable.
-- ============================================================================

CREATE TABLE shipping_methods (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    code       text NOT NULL,
    is_active  boolean NOT NULL DEFAULT true,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shipping_methods_code_format CHECK (code ~ '^[a-z0-9]+(_[a-z0-9]+)*$')
);

CREATE UNIQUE INDEX shipping_methods_code_key ON shipping_methods (code);

CREATE TABLE shipping_method_versions (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    method_id       uuid NOT NULL REFERENCES shipping_methods (id) ON DELETE RESTRICT,
    name            text NOT NULL,
    carrier         text,
    fee_cents       bigint NOT NULL,
    -- The order value at or above which this method ships free. NULL means the
    -- fee always applies.
    free_over_cents bigint,
    effective_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shipping_method_versions_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT shipping_method_versions_fee_non_negative CHECK (fee_cents >= 0),
    CONSTRAINT shipping_method_versions_free_over_non_negative
        CHECK (free_over_cents IS NULL OR free_over_cents >= 0)
);

CREATE UNIQUE INDEX shipping_method_versions_effective_key
    ON shipping_method_versions (method_id, effective_at);

CREATE TRIGGER shipping_method_versions_append_only
    BEFORE UPDATE OR DELETE ON shipping_method_versions
    FOR EACH ROW EXECUTE FUNCTION forbid_change('shipping_method_versions_append_only');

-- ============================================================================
-- Orders
--
-- One column per lifecycle. The previous design put payment, fulfilment and
-- returns in a single `status`, which cannot express a shipped order with a
-- refund request open — and, worse, made two concurrent writers of unrelated
-- facts overwrite each other. Payment state lives in `payments`, return state
-- in `return_requests`, and what remains here is fulfilment.
--
-- The money columns are the ones that are NOT derivable: a discount granted, a
-- fee quoted, a tax assessed. Subtotal and total are computed from the lines.
-- ============================================================================

-- One row per business day, incremented atomically. A MAX()+1 in application
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
    -- The number a customer quotes to support. Separate from the primary key
    -- so nobody has to read a uuid aloud.
    order_number         text NOT NULL,
    -- NULL for a guest order, and NULL again once an account is erased. The
    -- order itself survives either way: it is a financial record.
    user_id              uuid REFERENCES users (id) ON DELETE SET NULL,
    fulfillment_status   text NOT NULL DEFAULT 'pending',
    currency             text NOT NULL DEFAULT 'TWD',
    discount_cents       bigint NOT NULL DEFAULT 0,
    shipping_cents       bigint NOT NULL DEFAULT 0,
    tax_cents            bigint NOT NULL DEFAULT 0,
    discount_code        text,
    -- The version that was in force, plus its name as shown. The FK explains
    -- the price; the snapshot survives the version being superseded.
    shipping_version_id  uuid REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
    shipping_method_code text NOT NULL,
    shipping_method_name text NOT NULL,
    customer_note        text,
    staff_note           text,
    placed_at            timestamptz NOT NULL DEFAULT now(),
    cancelled_at         timestamptz,
    completed_at         timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_number_format CHECK (order_number ~ '^GO-[0-9]{6}-[0-9]{6}$'),
    CONSTRAINT orders_currency_is_twd CHECK (currency = 'TWD'),
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
    -- An order cannot both have been called off and have run to completion.
    CONSTRAINT orders_not_both_ended
        CHECK (cancelled_at IS NULL OR completed_at IS NULL),
    -- Each ending, if recorded, happened after the order existed.
    CONSTRAINT orders_cancelled_after_placed
        CHECK (cancelled_at IS NULL OR cancelled_at >= placed_at),
    CONSTRAINT orders_completed_after_placed
        CHECK (completed_at IS NULL OR completed_at >= placed_at),
    -- One-way, deliberately. The status may move on — a cancelled order can
    -- later be refunded — but the moment it was called off is history and
    -- keeps its timestamp. The previous two-way form made "cancelled then
    -- refunded" impossible to express at all.
    CONSTRAINT orders_cancelled_has_time
        CHECK (fulfillment_status <> 'cancelled' OR cancelled_at IS NOT NULL),
    CONSTRAINT orders_completed_has_time
        CHECK (fulfillment_status <> 'completed' OR completed_at IS NOT NULL)
);

CREATE UNIQUE INDEX orders_number_key ON orders (order_number);
CREATE INDEX orders_user_placed_idx ON orders (user_id, placed_at DESC);
CREATE INDEX orders_shipping_version_idx ON orders (shipping_version_id);
-- The admin queue: everything still owed work, oldest first.
CREATE INDEX orders_open_idx
    ON orders (placed_at)
    WHERE fulfillment_status IN ('pending', 'picking');

CREATE TRIGGER orders_set_updated_at
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The two tables that are written during checkout, before the order they will
-- belong to exists, and so could not carry the reference inline. Without these
-- a hold or an attempt can point at an order that was never created.
-- All three tables are created earlier in this same migration and hold no
-- rows, so the scan these constraints trigger has nothing to scan and the lock
-- has nothing to block. NOT VALID would leave them unvalidated for no gain.
ALTER TABLE store_credit_entries
    ADD CONSTRAINT store_credit_entries_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE RESTRICT;

ALTER TABLE inventory_reservations
    ADD CONSTRAINT inventory_reservations_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE RESTRICT;

ALTER TABLE checkout_attempts
    ADD CONSTRAINT checkout_attempts_order_fk
    FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE SET NULL;

-- Fulfilment moves forward. Reviving a cancelled order or un-shipping a
-- shipped one is not a correction, it is a lost fact.
CREATE FUNCTION orders_check_transition() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    legal boolean;
BEGIN
    IF NEW.fulfillment_status = OLD.fulfillment_status THEN
        RETURN NEW;
    END IF;

    legal := CASE OLD.fulfillment_status
        WHEN 'pending'   THEN NEW.fulfillment_status IN ('picking', 'cancelled')
        WHEN 'picking'   THEN NEW.fulfillment_status IN ('shipped', 'cancelled')
        WHEN 'shipped'   THEN NEW.fulfillment_status IN ('delivered', 'completed')
        WHEN 'delivered' THEN NEW.fulfillment_status = 'completed'
        ELSE false  -- completed and cancelled are終點
    END;

    IF NOT legal THEN
        RAISE EXCEPTION 'fulfilment cannot move from % to %',
            OLD.fulfillment_status, NEW.fulfillment_status
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_legal_transition';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_legal_transition
    BEFORE UPDATE OF fulfillment_status ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_check_transition();

-- What was bought, as it was at the moment of buying. variant_id points back
-- at the catalogue for reordering and stock, and may become NULL — the line
-- still reads correctly because the name, sku and price are its own.
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

-- Where it went, and who to tell. Separate from `orders` because this is the
-- personal data: erasure empties this table and leaves the financial record
-- whole.
CREATE TABLE order_private_data (
    order_id       uuid PRIMARY KEY REFERENCES orders (id) ON DELETE RESTRICT,
    email          text,
    recipient_name text,
    phone          text,
    postal_code    text,
    city           text,
    district       text,
    street         text,
    erased_at      timestamptz,
    -- Erasure is all-or-nothing: a row with a name but no street is a
    -- half-finished deletion nobody can reason about.
    CONSTRAINT order_private_data_erased_is_empty CHECK (
        (erased_at IS NOT NULL) = (
            email IS NULL AND recipient_name IS NULL AND phone IS NULL
            AND postal_code IS NULL AND city IS NULL AND district IS NULL
            AND street IS NULL
        )
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

-- Which lines, and how many of each, went in this parcel. A two-item order
-- shipped in two boxes has two shipments and four rows here.
CREATE TABLE order_shipment_lines (
    shipment_id   uuid NOT NULL REFERENCES order_shipments (id) ON DELETE RESTRICT,
    order_line_id uuid NOT NULL REFERENCES order_lines (id) ON DELETE RESTRICT,
    quantity      integer NOT NULL,
    PRIMARY KEY (shipment_id, order_line_id),
    CONSTRAINT order_shipment_lines_quantity_positive CHECK (quantity > 0)
);

CREATE INDEX order_shipment_lines_order_line_idx ON order_shipment_lines (order_line_id);

-- The timeline the customer sees. Append-only: an event that happened does not
-- stop having happened when the order moves on.
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
-- somewhere to go, and its arithmetic must not come out below zero.
--
-- The constraint is DEFERRABLE INITIALLY DEFERRED so the check runs at commit:
-- the lines are necessarily inserted after the order row they reference.
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
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_have_lines';
    END IF;

    IF subtotal - o.discount_cents + o.shipping_cents + o.tax_cents < 0 THEN
        RAISE EXCEPTION 'order % totals below zero', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_have_lines';
    END IF;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER orders_have_lines
    AFTER INSERT ON orders
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION orders_check_complete();

-- Once money has been captured, the itemisation that justified it is history.
-- Editing a price afterwards makes the payment unexplainable; a correction is
-- a refund, not an UPDATE.
CREATE FUNCTION order_lines_freeze() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target uuid := coalesce(NEW.order_id, OLD.order_id);
BEGIN
    IF EXISTS (
        SELECT 1 FROM payments
        WHERE order_id = target AND status = 'succeeded'
    ) THEN
        RAISE EXCEPTION 'order % is paid; its lines are settled', target
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_paid';
    END IF;
    RETURN coalesce(NEW, OLD);
END;
$$;

CREATE TRIGGER order_lines_frozen_once_paid
    BEFORE UPDATE OR DELETE ON order_lines
    FOR EACH ROW EXECUTE FUNCTION order_lines_freeze();

CREATE FUNCTION orders_freeze_money() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.discount_cents = OLD.discount_cents
       AND NEW.shipping_cents = OLD.shipping_cents
       AND NEW.tax_cents = OLD.tax_cents
       AND NEW.currency = OLD.currency
       AND NEW.order_number = OLD.order_number THEN
        RETURN NEW;
    END IF;

    IF EXISTS (
        SELECT 1 FROM payments
        WHERE order_id = NEW.id AND status = 'succeeded'
    ) THEN
        RAISE EXCEPTION 'order % is paid; its totals are settled', NEW.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_money_frozen_once_paid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_money_frozen_once_paid
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_freeze_money();

-- ============================================================================
-- Returns — 退換貨
-- ============================================================================

CREATE TABLE return_requests (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id           uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    -- Who asked, which is not the same question as who owns the order: support
    -- raises these too. The owner is reached through order_id, so this column
    -- carries no duplicate of it.
    requested_by_user_id uuid REFERENCES users (id) ON DELETE SET NULL,
    status             text NOT NULL DEFAULT 'requested',
    reason             text NOT NULL,
    resolution         text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    decided_at         timestamptz,
    CONSTRAINT return_requests_status_known
        CHECK (status IN ('requested', 'approved', 'rejected', 'completed')),
    CONSTRAINT return_requests_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT return_requests_decided_has_time
        CHECK ((status = 'requested') = (decided_at IS NULL))
);

CREATE INDEX return_requests_order_id_idx ON return_requests (order_id);
CREATE INDEX return_requests_requester_idx ON return_requests (requested_by_user_id);
CREATE INDEX return_requests_open_idx ON return_requests (created_at) WHERE status = 'requested';

CREATE TABLE return_request_lines (
    return_request_id uuid NOT NULL REFERENCES return_requests (id) ON DELETE CASCADE,
    order_line_id     uuid NOT NULL REFERENCES order_lines (id) ON DELETE RESTRICT,
    quantity          integer NOT NULL,
    PRIMARY KEY (return_request_id, order_line_id),
    CONSTRAINT return_request_lines_quantity_positive CHECK (quantity > 0)
);

CREATE INDEX return_request_lines_order_line_idx ON return_request_lines (order_line_id);

-- You cannot return more than you bought, counting every request against the
-- line. The line's order is locked first so two requests cannot both pass.
CREATE FUNCTION return_lines_within_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    bought integer;
    already integer;
BEGIN
    SELECT ol.quantity INTO bought
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    WHERE ol.id = NEW.order_line_id
    FOR UPDATE OF o;

    SELECT coalesce(sum(rl.quantity), 0) INTO already
    FROM return_request_lines rl
    JOIN return_requests r ON r.id = rl.return_request_id
    WHERE rl.order_line_id = NEW.order_line_id
      AND r.status <> 'rejected'
      AND rl.return_request_id <> NEW.return_request_id;

    IF already + NEW.quantity > bought THEN
        RAISE EXCEPTION 'returning % of a line that had % (already claimed %)',
            NEW.quantity, bought, already
            USING ERRCODE = 'check_violation', CONSTRAINT = 'return_within_purchase';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_within_purchase
    BEFORE INSERT OR UPDATE ON return_request_lines
    FOR EACH ROW EXECUTE FUNCTION return_lines_within_purchase();

-- 保固中裝置. One row per unit, because buying two phones registers two
-- warranties — the previous one-row-per-line design could not say that.
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
-- Invoices — 電子發票
--
-- Two things, previously one. What the customer asked for is a preference and
-- can be edited; what was issued to the tax authority is a document and cannot.
-- A return issues a 折讓證明單 rather than altering the original.
-- ============================================================================

CREATE TABLE invoice_preferences (
    order_id     uuid PRIMARY KEY REFERENCES orders (id) ON DELETE RESTRICT,
    invoice_type text NOT NULL,
    carrier_code text,
    tax_id       text,
    CONSTRAINT invoice_preferences_type_known
        CHECK (invoice_type IN ('mobile_carrier', 'member_carrier', 'company')),
    CONSTRAINT invoice_preferences_company_has_tax_id
        CHECK (invoice_type <> 'company' OR tax_id ~ '^[0-9]{8}$'),
    CONSTRAINT invoice_preferences_mobile_has_carrier
        CHECK (invoice_type <> 'mobile_carrier' OR carrier_code ~ '[^[:space:]]')
);

CREATE TABLE invoice_documents (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id     uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    kind         text NOT NULL,
    -- The invoice a 折讓 relieves. NULL for the invoice itself.
    original_id  uuid REFERENCES invoice_documents (id) ON DELETE RESTRICT,
    number       text NOT NULL,
    amount_cents bigint NOT NULL,
    status       text NOT NULL DEFAULT 'issued',
    provider_ref text,
    issued_at    timestamptz NOT NULL DEFAULT now(),
    voided_at    timestamptz,
    CONSTRAINT invoice_documents_kind_known CHECK (kind IN ('invoice', 'allowance')),
    CONSTRAINT invoice_documents_status_known CHECK (status IN ('issued', 'voided')),
    CONSTRAINT invoice_documents_number_present CHECK (number ~ '[^[:space:]]'),
    CONSTRAINT invoice_documents_amount_positive CHECK (amount_cents > 0),
    CONSTRAINT invoice_documents_voided_has_time
        CHECK ((status = 'voided') = (voided_at IS NOT NULL)),
    -- An allowance relieves a specific invoice; an invoice relieves nothing.
    CONSTRAINT invoice_documents_allowance_has_original
        CHECK ((kind = 'allowance') = (original_id IS NOT NULL))
);

CREATE UNIQUE INDEX invoice_documents_number_key ON invoice_documents (number);
CREATE INDEX invoice_documents_order_idx ON invoice_documents (order_id);
CREATE INDEX invoice_documents_original_idx ON invoice_documents (original_id);

-- Issued documents are filed, not edited. Voiding sets status and voided_at
-- through the one path allowed below.
CREATE FUNCTION invoice_documents_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'invoice documents are filed, not deleted'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
    END IF;

    IF NEW.id <> OLD.id OR NEW.order_id <> OLD.order_id OR NEW.kind <> OLD.kind
       OR NEW.number <> OLD.number OR NEW.amount_cents <> OLD.amount_cents
       OR NEW.original_id IS DISTINCT FROM OLD.original_id
       OR NEW.issued_at <> OLD.issued_at THEN
        RAISE EXCEPTION 'an issued document may only be voided, not rewritten'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'invoice_documents_only_void';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER invoice_documents_only_void
    BEFORE UPDATE OR DELETE ON invoice_documents
    FOR EACH ROW EXECUTE FUNCTION invoice_documents_guard();

-- ============================================================================
-- Payments
-- ============================================================================

CREATE TABLE payments (
    id                     uuid PRIMARY KEY DEFAULT uuidv7(),
    order_id               uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    provider               text NOT NULL DEFAULT 'stripe',
    -- Stripe's PaymentIntent id.
    provider_ref           text NOT NULL,
    status                 text NOT NULL,
    -- What the intent was created for, and what was actually taken. They are
    -- different facts: a PaymentIntent carries the amount asked for whatever
    -- happens to it, and only a succeeded one has captured anything.
    intended_amount_cents  bigint NOT NULL,
    captured_amount_cents  bigint,
    currency               text NOT NULL DEFAULT 'TWD',
    card_brand             text,
    card_last4             text,
    paid_at                timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT payments_provider_known CHECK (provider IN ('stripe')),
    CONSTRAINT payments_status_known
        CHECK (status IN ('requires_payment', 'processing', 'succeeded', 'failed', 'cancelled')),
    CONSTRAINT payments_intended_positive CHECK (intended_amount_cents > 0),
    CONSTRAINT payments_captured_non_negative
        CHECK (captured_amount_cents IS NULL OR captured_amount_cents >= 0),
    CONSTRAINT payments_currency_is_twd CHECK (currency = 'TWD'),
    CONSTRAINT payments_last4_format CHECK (card_last4 IS NULL OR card_last4 ~ '^[0-9]{4}$'),
    -- Captured, paid_at and succeeded are one fact recorded three ways; any
    -- two without the third is a row that cannot be reconciled.
    CONSTRAINT payments_succeeded_is_captured CHECK (
        (status = 'succeeded') = (paid_at IS NOT NULL AND captured_amount_cents IS NOT NULL)
    )
);

CREATE UNIQUE INDEX payments_provider_ref_key ON payments (provider, provider_ref);
CREATE INDEX payments_order_id_idx ON payments (order_id);
-- At most one capture per order. A second succeeded payment means the customer
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
    IF OLD.status IN ('succeeded', 'failed', 'cancelled') THEN
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

CREATE TABLE refunds (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    payment_id   uuid NOT NULL REFERENCES payments (id) ON DELETE RESTRICT,
    return_request_id uuid REFERENCES return_requests (id) ON DELETE RESTRICT,
    -- The caller's own key, assigned before Stripe is called. It doubles as
    -- the Idempotency-Key on the API request, so a crash between the call and
    -- the row cannot produce a second refund on retry.
    request_key  text NOT NULL,
    -- Filled in once Stripe answers. NULL means "asked for, not yet confirmed"
    -- — the state the previous NOT NULL column could not represent, which is
    -- why the row had to be written after the money moved.
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
        CHECK ((status = 'failed') = (failed_at IS NOT NULL))
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
    already  bigint;
BEGIN
    SELECT captured_amount_cents INTO captured
    FROM payments WHERE id = NEW.payment_id FOR UPDATE;

    IF captured IS NULL THEN
        RAISE EXCEPTION 'refunding a payment that captured nothing'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
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

CREATE TRIGGER refunds_within_capture
    BEFORE INSERT OR UPDATE OF amount_cents, status ON refunds
    FOR EACH ROW EXECUTE FUNCTION refunds_guard();

-- Stripe delivers at least once and in no guaranteed order. Recording the
-- event before acting on it is what makes that safe; keeping the payload and
-- the provider's own timestamp is what makes a late arrival recognisable as
-- late rather than as news.
CREATE TABLE payment_webhook_events (
    provider            text NOT NULL,
    event_id            text NOT NULL,
    type                text NOT NULL,
    object_ref          text,
    provider_created_at timestamptz,
    payload             jsonb NOT NULL,
    received_at         timestamptz NOT NULL DEFAULT now(),
    processed_at        timestamptz,
    PRIMARY KEY (provider, event_id),
    CONSTRAINT payment_webhook_events_type_present CHECK (type ~ '[^[:space:]]')
);

CREATE INDEX payment_webhook_events_unprocessed_idx
    ON payment_webhook_events (received_at)
    WHERE processed_at IS NULL;
CREATE INDEX payment_webhook_events_object_idx ON payment_webhook_events (object_ref);

-- ============================================================================
-- Outbox
--
-- Order confirmations, shipping notices and password resets are written in the
-- same transaction as the change that causes them. Sending first risks a mail
-- about an order that never committed; committing first risks silence.
-- ============================================================================

CREATE TABLE outbox_messages (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    topic        text NOT NULL,
    dedupe_key   text NOT NULL,
    payload      jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    attempts     integer NOT NULL DEFAULT 0,
    last_error   text,
    CONSTRAINT outbox_messages_topic_present CHECK (topic ~ '[^[:space:]]'),
    CONSTRAINT outbox_messages_attempts_non_negative CHECK (attempts >= 0)
);

CREATE UNIQUE INDEX outbox_messages_dedupe_key ON outbox_messages (topic, dedupe_key);
CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (available_at)
    WHERE delivered_at IS NULL;

-- ============================================================================
-- Audit
--
-- Who changed what, kept apart from the domain tables so that a correction to
-- the domain cannot quietly correct its own record.
-- ============================================================================

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

-- ============================================================================
-- Merchandising
-- ============================================================================

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
        CHECK (image_key IS NULL OR image_alt ~ '[^[:space:]]'),
    CONSTRAINT hero_slides_secondary_cta_complete
        CHECK ((secondary_cta_label IS NULL) = (secondary_cta_href IS NULL)),
    CONSTRAINT hero_slides_window_ordered
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);

CREATE UNIQUE INDEX hero_slides_position_key ON hero_slides (position);

CREATE TRIGGER hero_slides_set_updated_at
    BEFORE UPDATE ON hero_slides
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE promo_banners (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    message       text NOT NULL,
    -- The narrow-screen wording is different copy, not a truncation.
    message_short text,
    code          text,
    cta_label     text,
    cta_href      text,
    starts_at     timestamptz,
    ends_at       timestamptz,
    is_active     boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT promo_banners_message_present CHECK (message ~ '[^[:space:]]'),
    CONSTRAINT promo_banners_cta_complete CHECK ((cta_label IS NULL) = (cta_href IS NULL)),
    CONSTRAINT promo_banners_window_ordered
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at)
);

CREATE INDEX promo_banners_active_idx ON promo_banners (starts_at) WHERE is_active;

CREATE TRIGGER promo_banners_set_updated_at
    BEFORE UPDATE ON promo_banners
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 限時優惠. The campaign is a collection with a deadline; the reduction itself
-- is the variant's price against its compare-at price. The trigger below is
-- what stops the two disagreeing on the storefront.
CREATE TABLE sale_campaigns (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    slug       text NOT NULL,
    title      text NOT NULL,
    starts_at  timestamptz NOT NULL DEFAULT now(),
    ends_at    timestamptz NOT NULL,
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sale_campaigns_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT sale_campaigns_title_present CHECK (title ~ '[^[:space:]]'),
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

-- A product in 限時優惠 that shows no saving is a promise the page cannot
-- keep, so membership requires at least one variant actually marked down.
CREATE FUNCTION sale_campaign_products_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
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

-- ============================================================================
-- Content and messages
-- ============================================================================

CREATE TABLE faq_entries (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    category   text NOT NULL,
    question   text NOT NULL,
    answer     text NOT NULL,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT faq_entries_category_present CHECK (category ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_question_present CHECK (question ~ '[^[:space:]]'),
    CONSTRAINT faq_entries_answer_present CHECK (answer ~ '[^[:space:]]')
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

CREATE TABLE newsletter_subscribers (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    email           text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    unsubscribed_at timestamptz,
    CONSTRAINT newsletter_subscribers_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT newsletter_subscribers_email_trimmed CHECK (email = btrim(email))
);

CREATE UNIQUE INDEX newsletter_subscribers_email_key ON newsletter_subscribers (lower(email));
