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
-- Roles
--
-- The comments in this file claim that stock_quantity has one writer, that a
-- ledger is append-only, that captured money is history. A trigger can refuse
-- an UPDATE, but nothing stops a connection from running a plain
-- `UPDATE product_variants SET stock_quantity = 999` — the trigger is on the
-- movements table, not on the column. So those claims are enforced the only
-- way they can be: the application connects as a role that cannot do it.
--
-- goen_app is what the running binary uses. It may read everything and write
-- the ordinary tables, but the privileged paths — posting inventory, taking
-- money, appending to a ledger or an audit log — are SECURITY DEFINER
-- functions owned by the schema owner, and goen_app reaches them only by
-- calling the function. Direct DML on those tables is revoked.
--
-- NOLOGIN: these are privilege sets, granted to whatever login role a
-- deployment creates. `GRANT goen_app TO goen;` in a dev database.
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'goen_app') THEN
        CREATE ROLE goen_app NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'goen_readonly') THEN
        CREATE ROLE goen_readonly NOLOGIN;
    END IF;
    -- The login role production connects with. NOSUPERUSER is the point: the
    -- privilege model rests on the connection being unable to regain what
    -- goen_app gives up, so `RESET ROLE` must not restore a superuser. It owns
    -- nothing and is only a member of goen_app. The password is set by
    -- operations, never in a migration. In development the Makefile may still
    -- connect as the owning superuser for convenience; the binary's startup
    -- guard refuses to serve if, after SET ROLE goen_app, the session is a
    -- superuser.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'goen_web') THEN
        CREATE ROLE goen_web LOGIN NOSUPERUSER IN ROLE goen_app;
    END IF;
END
$$;

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
LANGUAGE plpgsql SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    -- The one permitted mutation of an append-only row: the referential
    -- ON DELETE SET NULL that nulls actor_user_id when the acting user is
    -- erased. Everything else must be byte-identical and the column may only
    -- go to NULL, so a user can be deleted without the ledger blocking it and
    -- without history being rewritten — a value becomes unknown, never false.
    -- The jsonb key-removal is a no-op on the append-only tables that have no
    -- actor_user_id, so for them any change at all still falls through to the
    -- exception below.
    IF TG_OP = 'UPDATE'
       AND (to_jsonb(NEW) - 'actor_user_id') = (to_jsonb(OLD) - 'actor_user_id')
       AND to_jsonb(NEW) ->> 'actor_user_id' IS NULL THEN
        RETURN NEW;
    END IF;
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
    -- No surrounding whitespace of any kind. btrim strips only spaces, so
    -- `btrim(email) = email` let a leading tab through and the folded unique
    -- index below could then hold two rows for one mailbox.
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
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    -- SET NULL, not RESTRICT: a customer exercising erasure must not be held
    -- hostage by a store-credit account, and the ledger below keeps its own
    -- account_id so the financial history survives the user going away.
    user_id    uuid UNIQUE REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE store_credit_entries (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    -- Points at the account, not the user, so erasing the user leaves the
    -- ledger balanced and attributable to the account that still exists.
    account_id     uuid NOT NULL REFERENCES store_credit_accounts (id) ON DELETE RESTRICT,
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
    -- Symmetric ceiling (credits are positive, debits negative). Without it
    -- store_credit_guard's running balance could be driven to overflow bigint
    -- (SQLSTATE 22003) instead of raising store_credit_never_negative.
    CONSTRAINT store_credit_entries_amount_in_range
        CHECK (amount_cents BETWEEN -10000000000 AND 10000000000),
    CONSTRAINT store_credit_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT store_credit_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]')
);

-- A reversal undoes exactly one entry, once. Without the unique index the same
-- debit could be reversed twice and the balance would climb.
CREATE UNIQUE INDEX store_credit_entries_reverses_key
    ON store_credit_entries (reverses_id) WHERE reverses_id IS NOT NULL;

CREATE UNIQUE INDEX store_credit_entries_idempotency_key
    ON store_credit_entries (idempotency_key);
CREATE INDEX store_credit_entries_account_idx ON store_credit_entries (account_id, created_at DESC);
CREATE INDEX store_credit_entries_order_idx ON store_credit_entries (order_id);

-- Locks the account before it sums, so two concurrent debits serialise rather
-- than both reading a balance that is about to be spent. A reversal, if this
-- is one, must undo exactly one entry of the same account by exactly its
-- negation — otherwise a -50 debit could be "reversed" by a +80 on another
-- account.
CREATE FUNCTION store_credit_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    balance bigint;
    original store_credit_entries%ROWTYPE;
BEGIN
    PERFORM 1 FROM store_credit_accounts WHERE id = NEW.account_id FOR UPDATE;

    IF NEW.reverses_id IS NOT NULL THEN
        SELECT * INTO original FROM store_credit_entries WHERE id = NEW.reverses_id FOR UPDATE;
        IF original.account_id <> NEW.account_id OR NEW.amount_cents <> -original.amount_cents THEN
            RAISE EXCEPTION 'a reversal must negate one entry of the same account'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_never_negative';
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
        'hold',         -- 結帳保留期間扣減
        'sale',         -- 出貨扣減
        'release',      -- 取消或逾期釋放
        'return',       -- 退貨入庫
        'adjustment'    -- 人工盤點
    )),
    -- The sign is not free: stock comes IN on a receipt, release or return and
    -- goes OUT on a hold or sale. Only a manual adjustment may be either way.
    -- Without this a caller could post a +5 'hold' or a -5 'receipt' and the
    -- ledger would read backwards while the projection still moved.
    CONSTRAINT inventory_movements_delta_direction CHECK (
        CASE reason
            WHEN 'receipt' THEN delta > 0
            WHEN 'release' THEN delta > 0
            WHEN 'return'  THEN delta > 0
            WHEN 'hold'    THEN delta < 0
            WHEN 'sale'    THEN delta < 0
            ELSE true  -- adjustment: either direction
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
    -- A sale or a hold may not take stock below the safety level; a receipt,
    -- return or manual correction may (a correction is how you fix an
    -- oversold count). The floor is folded into the same conditional UPDATE as
    -- the balance, so the read and the write remain one statement on one
    -- locked row.
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
        CHECK ((state = 'held') = (settled_at IS NULL)),
    CONSTRAINT inventory_reservations_expiry_after_creation CHECK (expires_at > created_at)
);

-- At most one LIVE hold per (order, variant). Partial on state='held' so that a
-- released or consumed reservation does not occupy the slot forever: after a
-- hold is released (abandoned checkout), a fresh hold for the same order and
-- variant can be taken. A non-partial unique index made re-holding impossible.
CREATE UNIQUE INDEX inventory_reservations_order_variant_key
    ON inventory_reservations (order_id, variant_id)
    WHERE state = 'held';
-- The unique index above is partial now, so it no longer covers the order_id
-- foreign key for every state; a plain index does.
CREATE INDEX inventory_reservations_order_idx ON inventory_reservations (order_id);
CREATE INDEX inventory_reservations_variant_idx ON inventory_reservations (variant_id);
-- The sweeper's read: holds that have run out of time.
CREATE INDEX inventory_reservations_expiring_idx
    ON inventory_reservations (expires_at)
    WHERE state = 'held';

-- Take a hold: decrement stock through the ledger and record the reservation,
-- in one transaction. The reservation table on its own is just a shape — this
-- is the only thing that makes a hold mean the stock is gone.
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

    -- The idempotency key is the caller's, not derived from (order, variant):
    -- a retry of the SAME checkout attempt passes the same key and the movement's
    -- unique key makes it a no-op, while a genuinely NEW hold after a release
    -- passes a fresh key and is allowed (the partial unique index above frees the
    -- slot). A key derived from order+variant could never distinguish the two.
    -- record_inventory_movement locks the variant and enforces the floor.
    PERFORM record_inventory_movement(
        p_variant_id, -p_quantity, 'hold',
        p_idempotency_key, 'order', p_order_id, NULL);

    INSERT INTO inventory_reservations (order_id, variant_id, quantity, expires_at)
    VALUES (p_order_id, p_variant_id, p_quantity, p_expires_at)
    RETURNING id INTO reservation_id;

    RETURN reservation_id;
END;
$$;

-- Consume a hold at fulfilment. The stock already left the shelf when the
-- hold was taken (a -quantity 'hold' movement), so consuming records NO
-- further stock movement — doing so would return the item to stock at the
-- moment it is sold. The 'hold' movement is the permanent ledger record that
-- the stock left; the reservation flipping to 'consumed' is what marks it a
-- completed sale rather than an outstanding hold. The conditional UPDATE is
-- the lock: a second caller finds no held row.
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

-- Release a hold — cancelled checkout or expiry sweep: return the stock and
-- close the reservation, once. A hold on a PAID order may NOT be released: the
-- sweeper could otherwise expire the hold of an order that has been paid but not
-- yet picked and hand its stock back to the shelf, reselling a sold item. The
-- only exit for a paid hold is consume_reservation. The reservation is locked,
-- then its order, so this serialises against a payment landing concurrently.
CREATE FUNCTION release_reservation(p_reservation_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    r inventory_reservations%ROWTYPE;
BEGIN
    SELECT * INTO r FROM inventory_reservations WHERE id = p_reservation_id FOR UPDATE;
    IF NOT FOUND OR r.state <> 'held' THEN
        RAISE EXCEPTION 'reservation % is not held', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_state';
    END IF;

    PERFORM 1 FROM orders WHERE id = r.order_id FOR UPDATE;
    IF EXISTS (SELECT 1 FROM payments WHERE order_id = r.order_id AND status = 'succeeded') THEN
        RAISE EXCEPTION 'reservation % is on a paid order; consume it, do not release', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_paid_no_release';
    END IF;

    UPDATE inventory_reservations
    SET state = 'released', settled_at = now()
    WHERE id = p_reservation_id AND state = 'held';

    PERFORM record_inventory_movement(
        r.variant_id, r.quantity, 'release',
        'release:' || r.id, 'reservation', r.id, NULL);
END;
$$;

-- Reservations are written only through those three functions; goen_app's
-- direct write is revoked with the rest at the foot of the file.

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
-- Partial, but a lookup by user_id is always `WHERE user_id = $1`, which
-- implies NOT NULL, so this serves the foreign key as well — no separate full
-- index is needed.
CREATE UNIQUE INDEX carts_one_per_user ON carts (user_id) WHERE user_id IS NOT NULL;

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

-- Partial on order_id IS NOT NULL; a lookup by order_id implies NOT NULL, so
-- this also serves the foreign key.
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
    order_number         text NOT NULL DEFAULT next_order_number(),
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
    shipping_version_id  uuid NOT NULL REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
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
    lines integer;
    subtotal bigint;
    order_total bigint;
    credit_applied bigint;
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

    -- goen does not ship what it has not collected (owner decision: online-only,
    -- Stripe-only, no cash-on-delivery). Leaving 'pending' into fulfilment
    -- (picking) requires the order to be FUNDED: either it owes nothing — a free
    -- or fully store-credited order — or a succeeded payment is on record, which
    -- payments_capture_matches_order guarantees captured exactly the owed amount.
    -- Cancelling from pending is always allowed. This is also the answer to "what
    -- is a paid order": a 0-owed order is funded with no payment row, which the
    -- "exists a succeeded payment" definition could not express. When a new
    -- funding source (COD) is ever added, it is added HERE.
    IF OLD.fulfillment_status = 'pending' AND NEW.fulfillment_status = 'picking' THEN
        SELECT count(*), coalesce(sum(unit_price_cents * quantity), 0)
        INTO lines, subtotal FROM order_lines WHERE order_id = NEW.id;
        order_total := subtotal - NEW.discount_cents + NEW.shipping_cents + NEW.tax_cents;
        credit_applied := -coalesce((
            SELECT sum(amount_cents) FROM store_credit_entries
            WHERE order_id = NEW.id AND amount_cents < 0), 0);
        IF (order_total - credit_applied) <> 0
           AND NOT EXISTS (SELECT 1 FROM payments
                           WHERE order_id = NEW.id AND status = 'succeeded') THEN
            RAISE EXCEPTION 'order % cannot leave pending unfunded (owes %)',
                NEW.order_number, order_total - credit_applied
                USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_funded_to_leave_pending';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER orders_legal_transition
    BEFORE UPDATE OF fulfillment_status ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_check_transition();

-- An order begins unpaid and unfulfilled. Inserting one straight into
-- 'shipped' would skip every transition guard and every side effect they
-- carry, so only the two starting states are legal at birth.
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
-- Referenced by the composite foreign keys that keep shipment and return
-- lines on the same order as the line.
CREATE UNIQUE INDEX order_lines_order_key ON order_lines (order_id, id);

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
    -- Two exhaustive states, not "erased iff nothing set". The old form —
    -- `erased_at IS NOT NULL = (all NULL)` — was satisfied by a live row that
    -- happened to have only some fields cleared, e.g. a row with an email but
    -- no name and no erased_at. Delivery needs every field, or the row is
    -- erased and holds none.
    CONSTRAINT order_private_data_all_or_erased CHECK (
        (erased_at IS NULL
            AND email IS NOT NULL AND recipient_name IS NOT NULL
            AND phone IS NOT NULL AND postal_code IS NOT NULL
            AND city IS NOT NULL AND district IS NOT NULL AND street IS NOT NULL)
        OR
        (erased_at IS NOT NULL
            AND email IS NULL AND recipient_name IS NULL AND phone IS NULL
            AND postal_code IS NULL AND city IS NULL AND district IS NULL
            AND street IS NULL)
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

-- Which lines, and how many of each, went in this parcel. A two-item order
-- shipped in two boxes has two shipments and four rows here.
--
-- order_id is carried so the composite foreign keys can enforce that the line
-- and the shipment belong to the SAME order. Two plain foreign keys could not:
-- a shipment of order A was able to carry a line of order B.
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

-- You cannot ship more of a line than was bought, counting every shipment.
-- The line's order is locked first so two shipments cannot both pass.
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

-- Once money has been captured, the itemisation that justified it is history.
-- Editing a price afterwards makes the payment unexplainable; a correction is
-- a refund, not an UPDATE.
CREATE FUNCTION order_lines_freeze() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target uuid := coalesce(NEW.order_id, OLD.order_id);
BEGIN
    -- On UPDATE a line cannot move to another order, or a paid line could be
    -- carried into an unpaid one to escape the freeze.
    IF TG_OP = 'UPDATE' AND NEW.order_id <> OLD.order_id THEN
        RAISE EXCEPTION 'an order line cannot change orders'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_paid';
    END IF;

    -- Lock the order so a concurrent capture cannot succeed between this check
    -- and the commit. Without it, T1 edits the line while T2 inserts the
    -- succeeded payment, and both pass.
    PERFORM 1 FROM orders WHERE id = target FOR UPDATE;

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

-- INSERT included: a paid order used to accept a brand-new line, because the
-- trigger only fired on UPDATE and DELETE.
CREATE TRIGGER order_lines_frozen_once_paid
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
       AND NEW.shipping_method_name = OLD.shipping_method_name THEN
        RETURN NEW;
    END IF;

    PERFORM 1 FROM orders WHERE id = NEW.id FOR UPDATE;
    IF EXISTS (
        SELECT 1 FROM payments
        WHERE order_id = NEW.id AND status = 'succeeded'
    ) THEN
        RAISE EXCEPTION 'order % is paid; its totals and shipping are settled', NEW.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'orders_money_frozen_once_paid';
    END IF;
    RETURN NEW;
END;
$$;

-- The shipping snapshot must name the method the version actually belongs to: a
-- home_delivery version carrying a store_pickup code is a contradiction the FK
-- alone cannot catch (it only proves the version exists). The name is a
-- customer-facing label and may differ from the version's internal name, so only
-- the code — which identifies the method — is constrained.
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
    PRIMARY KEY (return_request_id, order_line_id),
    CONSTRAINT return_request_lines_quantity_positive CHECK (quantity > 0),
    CONSTRAINT return_request_lines_request_fk
        FOREIGN KEY (order_id, return_request_id) REFERENCES return_requests (order_id, id)
        ON DELETE CASCADE,
    CONSTRAINT return_request_lines_line_fk
        FOREIGN KEY (order_id, order_line_id) REFERENCES order_lines (order_id, id)
        ON DELETE RESTRICT
);

CREATE INDEX return_request_lines_order_line_idx ON return_request_lines (order_id, order_line_id);
CREATE INDEX return_request_lines_order_request_idx ON return_request_lines (order_id, return_request_id);

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

-- The transition machine: requested → approved | rejected, approved →
-- completed. Every advanced state is reached only through this UPDATE (birth is
-- guarded to 'requested' below). There is deliberately no branch for leaving
-- 'rejected': it is terminal, so an earlier draft's "recount on revival" code
-- was unreachable and is gone — the file's own rule is to keep no guard that
-- can never fire.
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
    RETURN NEW;
END;
$$;

CREATE TRIGGER return_requests_legal_transition
    BEFORE UPDATE OF status ON return_requests
    FOR EACH ROW EXECUTE FUNCTION return_requests_recount();

-- A return begins 'requested'. Inserting one straight into 'approved' or
-- 'completed' would skip the transition machine above and the quantity recount
-- it performs, exactly as orders_start_pending guards orders. Only the initial
-- state is legal at birth; everything else is reached through an audited UPDATE.
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
    -- `type <> 'company' OR tax_id ~ regex` evaluates to NULL when tax_id is
    -- NULL, and a NULL CHECK passes — so a company invoice with no 統編 got in.
    -- The NOT NULL has to be spelled out before the regex.
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
    -- No self-reference CHECK: it is unreachable. An invoice must have
    -- original_id NULL (allowance_has_original below) and an allowance's
    -- original must be a real invoice of the same order (the trigger), so
    -- original_id = id cannot arise for any row that passes those. A CHECK
    -- that can never fire is one the review taught us not to keep.
);

CREATE UNIQUE INDEX invoice_documents_number_key ON invoice_documents (number);
CREATE INDEX invoice_documents_order_idx ON invoice_documents (order_id);
CREATE INDEX invoice_documents_original_idx ON invoice_documents (original_id);
-- At most one live 統一發票 per order: issuing a second while the first stands
-- would file two tax documents for one sale. Allowances (kind='allowance') are
-- unbounded, and a voided invoice frees the slot for a corrected reissue.
CREATE UNIQUE INDEX invoice_documents_one_active_invoice_per_order
    ON invoice_documents (order_id)
    WHERE kind = 'invoice' AND status <> 'voided';

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

    -- Voiding is one-way. A voided invoice is filed tax history (its 折讓 and
    -- the 統一發票 platform already have it); reviving it to 'issued' would let
    -- that history be rewritten. voided_has_time then also forbids clearing
    -- voided_at, since it must stay set while status is voided.
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

-- An allowance (折讓) must relieve an invoice of the SAME order, that invoice
-- must be a real invoice (not another allowance) and not voided, and the
-- allowances against it must not total more than it was for. The original is
-- locked so two allowances cannot both pass.
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

-- Line detail behind each document — the 財政部 allowance message needs the
-- original line, quantity, unit price, tax type and amounts, and none of that
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
    -- A line's unit price cannot be negative (a -500 line would let an issued
    -- invoice be padded with a credit that no allowance recorded), and both
    -- amounts share the ceiling every money column carries. The full
    -- header-equals-sum(lines) reconciliation waits for the draft→issued issue
    -- flow (tracked with the invoicing batch); these are the bounds that hold
    -- regardless of it.
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
    -- A failed ATTEMPT is not a terminal state for the payment: Stripe returns
    -- the PaymentIntent to requires_payment_method so the customer can try
    -- another card. Only succeeded and cancelled end it. `failed` used to be
    -- in this set and was treated as terminal, which made a recoverable
    -- decline unrecoverable.
    CONSTRAINT payments_status_known
        CHECK (status IN ('requires_payment', 'requires_action', 'processing',
                          'succeeded', 'cancelled')),
    CONSTRAINT payments_intended_positive CHECK (intended_amount_cents > 0),
    -- Same ceiling as every other money column (order_lines, refunds, …). Its
    -- absence let a capture approach 2^63 and overflow the running sums the
    -- refund and store-credit guards compute; bound the input instead.
    CONSTRAINT payments_intended_in_range CHECK (intended_amount_cents <= 10000000000),
    CONSTRAINT payments_captured_non_negative
        CHECK (captured_amount_cents IS NULL OR captured_amount_cents >= 0),
    CONSTRAINT payments_captured_in_range
        CHECK (captured_amount_cents IS NULL OR captured_amount_cents <= 10000000000),
    CONSTRAINT payments_currency_is_twd CHECK (currency = 'TWD'),
    CONSTRAINT payments_last4_format CHECK (card_last4 IS NULL OR card_last4 ~ '^[0-9]{4}$'),
    -- Captured, paid_at and succeeded are one fact recorded three ways, and
    -- this must be an equivalence in BOTH directions.
    --
    -- The previous form compared two booleans, which let a failed payment
    -- carry a captured amount: false = (NULL IS NOT NULL AND 50 IS NOT NULL)
    -- is false = false, and passed. The refund guard reads captured_amount
    -- without reading status, so that row was refundable — real money out
    -- against a payment that never took any in.
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
    -- 'failed' is intentionally absent: payments_status_known has no such value
    -- (a declined attempt returns to requires_payment, it is not terminal), so
    -- listing it here would name a state this table can never hold. refunds do
    -- keep 'failed' because the refunds CHECK includes it.
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

-- A payment may only succeed against an order that is still complete — has
-- lines, has delivery details, totals at or above zero. orders_have_lines
-- checks this at order-insert time only, and a draft order can lose its lines
-- between then and payment. This closes that window at the moment money is
-- taken, holding the order locked while it looks.
CREATE FUNCTION payments_require_complete_order() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    o orders%ROWTYPE;
    lines integer;
    subtotal bigint;
    order_total bigint;
    credit_applied bigint;
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

    -- "Has delivery details" means a LIVE, filled-in row, not merely a row.
    -- order_private_data_all_or_erased permits an all-NULL erased shape, and a
    -- live row could still carry blank strings; paying against either would ship
    -- an order with nowhere to send it. Require the row to be un-erased and every
    -- field non-blank.
    IF lines = 0
       OR NOT EXISTS (
           SELECT 1 FROM order_private_data
           WHERE order_id = o.id AND erased_at IS NULL
             AND email ~ '[^[:space:]]' AND recipient_name ~ '[^[:space:]]'
             AND phone ~ '[^[:space:]]' AND postal_code ~ '[^[:space:]]'
             AND city ~ '[^[:space:]]' AND district ~ '[^[:space:]]'
             AND street ~ '[^[:space:]]')
       OR subtotal - o.discount_cents + o.shipping_cents + o.tax_cents < 0 THEN
        RAISE EXCEPTION 'order % is not complete enough to be paid', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_require_complete_order';
    END IF;

    -- The capture must equal what the order is actually owed: its total, less any
    -- store credit spent on it. Without this an NT$1 capture marks an NT$33,980
    -- order paid, and an overpay is just as wrong — the whole intended/captured
    -- split is pointless if captured need not match the order. Store credit spent
    -- at checkout is a negative store_credit_entries row carrying the order_id;
    -- there is no such flow yet, so today this is simply capture = order total.
    -- When store-credit or cash-on-delivery funding arrives (batch ④), this is
    -- the line that must learn about them.
    order_total := subtotal - o.discount_cents + o.shipping_cents + o.tax_cents;
    credit_applied := -coalesce((
        SELECT sum(amount_cents) FROM store_credit_entries
        WHERE order_id = o.id AND amount_cents < 0), 0);
    IF NEW.captured_amount_cents <> order_total - credit_applied THEN
        RAISE EXCEPTION 'order % is owed % (total % less store credit %) but the capture is %',
            o.order_number, order_total - credit_applied, order_total, credit_applied,
            NEW.captured_amount_cents
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_capture_matches_order';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER payments_require_complete_order
    BEFORE INSERT OR UPDATE OF status ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_require_complete_order();

-- Once money has been captured, the row explaining it is history. Lowering
-- captured_amount_cents afterwards would silently raise the refundable
-- balance; moving the payment to another order would detach it from what it
-- paid for.
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
    -- The caller's own key, assigned and committed BEFORE the provider is
    -- called, so a crash between the call and the response leaves a row that
    -- reconciliation can resolve.
    --
    -- With Stripe it can also be sent as the Idempotency-Key, which makes the
    -- retry safe at the provider too. Do not assume that of every provider:
    -- ECPay's refund call (DoAction) takes seven parameters and none of them
    -- is an idempotency key, so a blind retry there refunds twice. For such a
    -- provider the rule is query-then-decide, never retry.
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
    -- The identity of a refund is fixed once written: re-pointing it at
    -- another payment would let one capture's allowance be spent against a
    -- second, and changing its key would break the provider correlation.
    IF TG_OP = 'UPDATE' AND (NEW.payment_id <> OLD.payment_id
                             OR NEW.request_key <> OLD.request_key) THEN
        RAISE EXCEPTION 'a refund cannot be moved to another payment'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;

    SELECT captured_amount_cents, status, order_id INTO captured, pay_status, pay_order
    FROM payments WHERE id = NEW.payment_id FOR UPDATE;

    -- Both halves matter. Reading captured alone accepted a failed payment
    -- that carried an amount, which the old CHECK allowed.
    IF pay_status <> 'succeeded' OR captured IS NULL THEN
        RAISE EXCEPTION 'refunding a payment that is % and captured %',
            pay_status, coalesce(captured::text, 'nothing')
            USING ERRCODE = 'check_violation', CONSTRAINT = 'refunds_within_capture';
    END IF;

    -- A refund tied to a return must relieve the SAME order the payment paid
    -- for: payment_id and return_request_id are otherwise unrelated foreign
    -- keys, so order A's capture could be refunded against order B's return.
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

-- Fires on every UPDATE, not only on the columns the sum reads: moving a
-- refund to another payment changed neither amount nor status and so was
-- invisible to the previous trigger.
CREATE TRIGGER refunds_within_capture
    BEFORE INSERT OR UPDATE ON refunds
    FOR EACH ROW EXECUTE FUNCTION refunds_guard();

-- A succeeded refund is money that left. Demoting it to failed would drop it
-- out of the sum above and free the allowance to be spent again.
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

-- A settled refund is money that already moved; its amount is history. The
-- no-regression guard above stops the STATUS being demoted, but leaves the
-- amount editable — a succeeded 60 could be rewritten to 100 (still within the
-- capture, so refunds_guard passes) and misstate what was actually returned.
-- Freeze the amount and identity once the refund is terminal, the same way
-- payments_settled_is_history freezes a captured payment. DELETE of a settled
-- refund is barred here too, since the owner/payment path keeps DELETE on the
-- table.
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

-- Membership is checked when a product joins, but a later edit that clears the
-- last discounted variant would leave a featured product with no saving. When
-- a variant loses its compare-at price or goes inactive, refuse it if that
-- product is in any campaign and nothing else is discounted.
CREATE FUNCTION sale_campaign_variant_still_valid() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM sale_campaign_products WHERE product_id = NEW.product_id) THEN
        RETURN NEW;
    END IF;
    IF EXISTS (
        SELECT 1 FROM product_variants
        WHERE product_id = NEW.product_id AND is_active
          AND compare_at_price_cents IS NOT NULL AND id <> NEW.id
    ) OR (NEW.is_active AND NEW.compare_at_price_cents IS NOT NULL) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'variant % is the last discount holding product % in a campaign',
        NEW.id, NEW.product_id
        USING ERRCODE = 'check_violation', CONSTRAINT = 'sale_campaign_variant_still_valid';
END;
$$;

CREATE TRIGGER sale_campaign_variant_still_valid
    BEFORE UPDATE OF compare_at_price_cents, is_active ON product_variants
    FOR EACH ROW EXECUTE FUNCTION sale_campaign_variant_still_valid();

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
    CONSTRAINT newsletter_subscribers_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$')
);

CREATE UNIQUE INDEX newsletter_subscribers_email_key ON newsletter_subscribers (lower(email));

-- ============================================================================
-- Privileges
--
-- Applied last, once every table and function exists. goen_app gets ordinary
-- read/write, then the privileged tables have their direct-write privileges
-- revoked so the only way in is the SECURITY DEFINER functions below.
-- ============================================================================

-- Erasing an account. order_private_data keys on the order, not the user, so a
-- plain DELETE of a user never reaches the delivery PII on their orders; and
-- stock_notifications carries a plaintext email that ON DELETE SET NULL would
-- leave behind. Coupling erasure to the schema — one SECURITY DEFINER entry
-- point — is what stops it being a second UPDATE someone has to remember.
CREATE FUNCTION erase_user(p_user_id uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public, pg_temp AS $$
BEGIN
    -- Blank every delivery field on this user's orders and stamp erased_at. The
    -- order_private_data_all_or_erased CHECK permits exactly this all-NULL
    -- state, so the order survives as a financial record with no PII.
    UPDATE order_private_data pd SET
        email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
        city = NULL, district = NULL, street = NULL, erased_at = now()
    FROM orders o
    WHERE pd.order_id = o.id AND o.user_id = p_user_id AND pd.erased_at IS NULL;

    -- customer_note is the customer's own words and routinely carries PII (a
    -- doorman, a phone number, a name) — it must go with the rest. staff_note is
    -- internal and stays. Nulling a note does not trip orders_freeze_money, which
    -- guards only money and the shipping snapshot, so a paid order erases too.
    UPDATE orders SET customer_note = NULL
    WHERE user_id = p_user_id AND customer_note IS NOT NULL;

    -- The restock-notification email cannot be nulled (it is NOT NULL); drop the
    -- rows outright — an erased account is not waiting for a restock.
    DELETE FROM stock_notifications WHERE user_id = p_user_id;

    -- The account itself. Its foreign keys carry the rest: orders.user_id and
    -- the ledgers' actor_user_id go to NULL (forbid_change permits that one
    -- nulling), auth rows cascade.
    DELETE FROM users WHERE id = p_user_id;
END;
$$;

-- ============================================================================
-- Privileges
--
-- Applied last, once every table and function exists. goen_app gets ordinary
-- read/write, then the privileged tables have their direct-write privileges
-- revoked so the only way in is the SECURITY DEFINER functions below.
-- ============================================================================

GRANT USAGE ON SCHEMA public TO goen_app, goen_readonly;

GRANT SELECT ON ALL TABLES IN SCHEMA public TO goen_app, goen_readonly;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO goen_app;

-- goen_readonly is for reading BUSINESS data (reports, dashboards), not for
-- reading everything. Take back SELECT on the tables that hold credentials and
-- personal data: a report role has no business seeing a session token, a TOTP
-- secret, a reset-token hash, an OAuth identity, a customer's delivery details,
-- or a raw payment webhook payload. goen_app keeps them — the application needs
-- them — but the read-only role does not.
REVOKE SELECT ON
    sessions, password_reset_tokens, staff_totp_credentials, user_identities,
    order_private_data, payment_webhook_events
    FROM goen_readonly;

-- Tables whose integrity depends on going through a function. Revoke the write
-- privileges that would let application code bypass it. SELECT stays.
--
--   product_variants.stock_quantity  — only record_inventory_movement writes it,
--     but a column grant cannot express "every column except one", so the whole
--     row is write-revoked and a function owns every mutation of a variant. A
--     direct INSERT could otherwise mint a variant carrying phantom stock the
--     ledger never posted, so INSERT goes too.
--   inventory_movements — append-only ledger; INSERT is via
--     record_inventory_movement, UPDATE/DELETE never.
--   audit_events / store_credit_entries — append-only ledgers with ALL direct
--     DML revoked. Their posting functions (an audit writer, a store-credit
--     posting function) are NOT built yet — they arrive with the admin/account
--     batches (⑦/⑥). Until then the door is deliberately shut rather than left
--     ajar: the schema does not pretend a write path exists. Tracked as
--     "add store_credit posting + audit writer functions" for those batches.
--   payments / refunds — money; written through the payment service which runs
--     as owner, not as goen_app. INSERT is revoked with UPDATE/DELETE, or
--     goen_app could write a born-succeeded capture with no provider behind it.
--   product_variants.stock_quantity is function-owned; a create_variant posting
--     function (forcing stock_quantity=0 at birth) also arrives with the admin
--     batch. Direct DML stays revoked until then.
--   order_number_counters — the atomic counter; only next_order_number (now a
--     SECURITY DEFINER function) may touch it, or the numbering stops being
--     unique under concurrency.
--   invoice_document_lines — append-only tax lines; INSERT appends, the rest is
--     history.
--   store_credit_accounts — its only mutable column is user_id, and the only
--     legal change is the erasure nulling (which runs as owner). A direct UPDATE
--     could repoint a whole balance to another user with no ledger guard
--     noticing, so revoke it.
REVOKE INSERT, UPDATE, DELETE ON
    inventory_movements, inventory_reservations, audit_events, store_credit_entries
    FROM goen_app;
REVOKE INSERT, UPDATE, DELETE ON product_variants FROM goen_app;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM goen_app;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM goen_app;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM goen_app;
REVOKE UPDATE ON store_credit_accounts FROM goen_app;
-- payment_webhook_events is the at-least-once delivery dedupe ledger AND the raw
-- record of what the provider sent. Deleting a row makes a resent event look
-- new and be processed twice; rewriting the payload rewrites the evidence. The
-- app only needs to stamp when it processed one, so keep INSERT and grant UPDATE
-- on processed_at alone; take the rest away.
REVOKE UPDATE, DELETE ON payment_webhook_events FROM goen_app;
GRANT UPDATE (processed_at) ON payment_webhook_events TO goen_app;
-- order_events and shipping_method_versions are append-only too (forbid_change),
-- and had only DELETE revoked below — leaving the trigger as their sole guard.
-- Revoke UPDATE so the privilege layer backs it. INSERT stays: the app appends
-- an order event, and a new shipping version is an insert.
REVOKE UPDATE ON order_events, shipping_method_versions FROM goen_app;
-- A user must be erased through erase_user(), which also blanks the delivery PII
-- on their orders and drops their restock emails. A direct DELETE would leave
-- both behind (order_private_data keys on the order, stock_notifications.email
-- is NOT NULL), so take DELETE away and leave that door as the only one. UPDATE
-- stays: profile edits are ordinary writes.
REVOKE DELETE ON users FROM goen_app;
REVOKE DELETE, TRUNCATE ON
    orders, order_lines, order_private_data, order_shipments,
    order_shipment_lines, order_events, invoice_documents, invoice_preferences,
    return_requests, return_request_lines, warranty_registrations,
    payments, refunds, shipping_method_versions
    FROM goen_app;

-- Trigger guards read their tables by unqualified name. A plpgsql function with
-- no pinned search_path resolves those against the caller's path — and pg_temp
-- is searched FIRST for relations even when it is not listed, so goen_app,
-- which may create temp tables, could plant an empty pg_temp.categories (or a
-- forged pg_temp.payments) and the guard would read the decoy and pass.
--
-- Listing pg_temp LAST is what fixes it: current_schemas then puts pg_temp after
-- public, so a real table always wins over a same-named temp one. (Omitting
-- pg_temp does NOT help — it is then searched implicitly first; verified.) Every
-- goen-authored function is pinned to (pg_catalog, public, pg_temp) and has its
-- EXECUTE revoked from PUBLIC so a SECURITY DEFINER posting function is not
-- callable by goen_readonly. The pg_trgm extension's own functions are left
-- untouched. TEMP is revoked from goen_app as a second, independent layer.
DO $$
DECLARE
    fn record;
BEGIN
    FOR fn IN
        SELECT p.oid::regprocedure AS sig
        FROM pg_proc p
        JOIN pg_language l ON l.oid = p.prolang
        WHERE p.pronamespace = 'public'::regnamespace
          AND l.lanname = 'plpgsql'
          AND NOT EXISTS (
              SELECT 1 FROM pg_depend d
              WHERE d.objid = p.oid AND d.deptype = 'e'
          )
    LOOP
        EXECUTE format('ALTER FUNCTION %s SET search_path = pg_catalog, public, pg_temp', fn.sig);
        EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', fn.sig);
    END LOOP;

    -- goen_app's TEMP privilege arrives via PUBLIC, so revoking it from PUBLIC
    -- is what takes it away. The owning superuser keeps it (superusers bypass);
    -- the storefront never needs a temp table.
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());

    -- golang-migrate creates public.schema_migrations before this migration
    -- runs, so GRANT ... ON ALL TABLES above hands goen_app write access to the
    -- migration bookkeeping — enough to forge a version or set dirty. Revoke it
    -- if the table is present (it is not when the schema is loaded directly).
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'schema_migrations'
               AND relnamespace = 'public'::regnamespace) THEN
        EXECUTE 'REVOKE ALL ON schema_migrations FROM goen_app, goen_readonly';
    END IF;
END
$$;

-- The posting functions run as their owner, so they can write what goen_app
-- cannot. EXECUTE is what goen_app is granted instead of direct DML.
-- record_inventory_movement is made SECURITY DEFINER here (the other three were
-- created that way); next_order_number joins them so the counter it increments
-- can be write-revoked from goen_app above.
ALTER FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) SECURITY DEFINER;
ALTER FUNCTION next_order_number() SECURITY DEFINER;

GRANT EXECUTE ON FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) TO goen_app;
GRANT EXECUTE ON FUNCTION hold_inventory(uuid, uuid, integer, timestamptz, text) TO goen_app;
GRANT EXECUTE ON FUNCTION consume_reservation(uuid) TO goen_app;
GRANT EXECUTE ON FUNCTION release_reservation(uuid) TO goen_app;
GRANT EXECUTE ON FUNCTION next_order_number() TO goen_app;
GRANT EXECUTE ON FUNCTION erase_user(uuid) TO goen_app;
