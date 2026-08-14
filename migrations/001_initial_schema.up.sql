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
--     `product_copurchases` are derived, and say so. They exist because the
--     alternative is aggregating a ledger — or 14,963 PL/pgSQL calls — on every
--     page view. Each has exactly one writer, named at the definition, and the
--     name is there to be checked: a claim of enforcement is worth only what a
--     reader can go and read.
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
-- Each role answers exactly one question, and its name says which:
--
--   goen       the owner. Runs migrations and owns every object here.
--   store      what a customer-facing request may do. The storefront and the
--              account pages are the same privilege — a visitor and a signed-in
--              customer are told apart in Go by their session, not by a database
--              role, so both run as this one.
--   store_svc  who connects. The only role here with LOGIN; it is the account
--              in the deployment's connection string.
--   reporting  what a report may read. Business data for dashboards, with
--              credentials and personal data taken back below.
--
-- "LOGIN" here is the PostgreSQL role attribute — permission to open a database
-- connection. It has nothing to do with a customer signing in to the website.
--
-- store may read everything and write the ordinary tables, but the privileged
-- paths — posting inventory, taking money, appending to a ledger or an audit
-- log — are SECURITY DEFINER functions owned by the schema owner, and store
-- reaches them only by calling the function. Direct DML on those tables is
-- revoked.
--
-- Batch ⑦'s back office needs a wider set than store (it writes product
-- variants and campaigns, which store is revoked from). Its roles are named
-- here rather than created: `admin` and `admin_svc`, added with the feature.
-- An empty role with no grants and no member would be privilege scaffolding
-- for something that does not exist yet.
--
-- NOLOGIN: store and reporting are privilege sets, granted to whatever login
-- role a deployment creates. `GRANT store TO goen;` in a dev database.
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'store') THEN
        CREATE ROLE store NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'reporting') THEN
        CREATE ROLE reporting NOLOGIN;
    END IF;
    -- The login role production connects with. NOSUPERUSER is the point: the
    -- privilege model rests on the connection being unable to regain what
    -- store gives up, so `RESET ROLE` must not restore a superuser. It owns
    -- nothing and is only a member of store. The password is set by
    -- operations, never in a migration. In development the Makefile may still
    -- connect as the owning superuser for convenience; the binary's startup
    -- guard refuses to serve if, after SET ROLE store, the session is a
    -- superuser.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'store_svc') THEN
        CREATE ROLE store_svc LOGIN NOSUPERUSER IN ROLE store;
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
    -- The English name, or NULL for a category the shop has not translated.
    --
    -- A category name is the most-read chrome on the site: it is in the header of
    -- every page. Treating it as CONTENT — the shop's to say however it likes, the
    -- way a product description is — left the header Chinese for an English
    -- visitor, which is the half-translated failure the whole locale feature
    -- exists to stop.
    --
    -- NULL rather than a copy of name, and NULL falls back to name at read time.
    -- A blank English column would give an English visitor an empty navigation
    -- item; a copy would make "has this been translated?" unanswerable. Absence is
    -- the honest way to say not yet.
    name_en    text,
    icon_key   text,
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT categories_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT categories_name_present CHECK (name ~ '[^[:space:]]'),
    -- Present IF PRESENT: a column that may be absent must not be allowed to be
    -- blank as well, or there are two ways to write "no translation" and only one
    -- of them reads correctly.
    CONSTRAINT categories_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT categories_not_own_parent CHECK (parent_id IS DISTINCT FROM id)
);

-- localized_name picks the name a reader gets, and is the ONE place that decides.
--
-- Written as a function rather than a CASE in each query for the reason
-- committed_orders and store_credit_balances are views: the rule would otherwise be
-- copied into the header query, the listing, the breadcrumb walk, the home tiles
-- and the sitemap, and the one that forgot would be whichever was written next — a
-- site whose header says Phones and whose breadcrumb says 手機 on the same page.
--
-- IMMUTABLE and LANGUAGE sql so the planner inlines it; it costs nothing per row.
--
-- The fallback is deliberate and it is to zh_hant, not to blank: a category the
-- shop has not translated yet renders in Chinese to an English visitor, which is
-- readable, rather than as an empty navigation item, which is broken.
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

-- Its GRANT is NOT here. `admin` and `reporting` do not exist yet at this point in
-- the file, and a GRANT naming a role the file has not created fails the migration
-- outright — which is how this was found, on the first run. It is with the other
-- function grants, far below, where the roles are real.

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
    -- The English copy, each half separately optional.
    --
    -- CLAUDE.md's line has always been that translating a product description is an
    -- EDITORIAL job and not a lookup table, and that stands: goen never invents one.
    -- What was wrong was the conclusion — that the copy therefore stays Chinese for
    -- everybody. A shop that has an English name for a product can now say so, and
    -- one that has not says nothing and the read falls back. The rule is that goen
    -- does not machine-translate, not that the shop may not translate.
    --
    -- Three columns rather than one: a name is worth translating first, a summary
    -- second, and a description is the one somebody has to sit down and write.
    -- Forcing all three at once is what would make the feature go unused.
    name_en       text,
    summary_en    text,
    description_en text,
    warranty_note text,
    -- How many months this product is covered for, or NULL when the shop has
    -- not stated a term.
    --
    -- Per PRODUCT and not a site-wide policy, because it is not one: a phone
    -- and a braided cable do not carry the same cover, and a single number in
    -- a policy page would be wrong for most of the catalogue. NULL is a real
    -- state and it means registration is refused — a warranty whose length
    -- nobody set is a promise nobody made, and computing an expiry from a
    -- default would invent one.
    warranty_months integer,
    status        text NOT NULL DEFAULT 'draft',
    published_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_warranty_months_sane
        CHECK (warranty_months IS NULL OR (warranty_months > 0 AND warranty_months <= 120)),
    CONSTRAINT products_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    CONSTRAINT products_name_present CHECK (name ~ '[^[:space:]]'),
    -- Present if present, so there is exactly one way to say "no translation".
    CONSTRAINT products_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT products_summary_en_present
        CHECK (summary_en IS NULL OR summary_en ~ '[^[:space:]]'),
    CONSTRAINT products_description_en_present
        CHECK (description_en IS NULL OR description_en ~ '[^[:space:]]'),
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

-- Search. Measured at 10,000 products (docs/decisions/003-listing-read-model.md):
-- a Latin query like '%pixel%' becomes a Bitmap Index Scan here at 1.5 ms, while
-- a two-character Chinese query falls back to a sequential scan at 8.8 ms — its
-- trigrams are extracted but far too unselective for the planner to prefer the
-- index. So this earns its keep on Latin model names and costs nothing on the
-- CJK path, which is a scan either way. Chinese search at scale wants a bigram
-- tsvector projection; that is the named follow-up, gated on the catalogue
-- passing ~5,000 active products.
CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);

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
    -- The English alt text, or NULL for one nobody has translated.
    --
    -- Read ALOUD by a screen reader, in the language <html lang> declares. An
    -- English page whose alt text is Chinese is not a cosmetic problem: the
    -- assistive tech announces it in the wrong voice, or gives up. Nullable where
    -- alt_text is not, because the fallback is the Chinese text — which a screen
    -- reader mispronounces, and is still better than nothing at all.
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
-- One image attached to one product once.
--
-- Per PRODUCT, and not UNIQUE (storage_key) alone. Global uniqueness on the key
-- guards a real problem the wrong way: with the key being a filename, the same
-- key on two rows could carry 800x800 and 1200x1200, a dependency on a non-key
-- column — but making that impossible makes something legitimate impossible with
-- it, because content-addressed uploads give the same picture ONE digest, so a
-- generic accessory shot used by two products is refused as a duplicate key.
--
-- The dimension problem is solved where it belongs: an uploaded image's size is
-- media_objects.width/height, one row per set of bytes, and there is nowhere for
-- a second answer to live. What this index holds is the attachment rule, which
-- is per product.
CREATE UNIQUE INDEX product_images_storage_key_key
    ON product_images (product_id, storage_key);

-- 顏色 / 容量: the axes a product varies along.
CREATE TABLE product_options (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- 顏色 → Colour. The picker's heading, so it is read by whoever is looking.
    --
    -- The English name is a LABEL and never an identifier: the variant picker puts
    -- the choice in the URL, and the URL carries the canonical `name`. Selecting by
    -- what is displayed would make a shared link resolve differently for a reader
    -- in another language, which is the whole reason display and identity are two
    -- columns rather than one.
    name_en    text,
    position   integer NOT NULL DEFAULT 0,
    CONSTRAINT product_options_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT product_options_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]')
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
    -- 星霧藍 → Mist Blue. A label, exactly like product_options.name_en: `value` is
    -- what the URL selects on and what variant matching compares.
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
    -- The PARCEL this variant ships as: its longest side, the sum of its three
    -- sides, and what it weighs. In millimetres and grams because a carrier's
    -- own limits are stated that way and a fraction of a centimetre is not a
    -- thing anybody measures.
    --
    -- All NULLABLE, and NULL means UNMEASURED rather than unlimited. A shipping
    -- method may only be REFUSED on a figure that exists: hiding 超商取貨 —
    -- which 75.2% of Taiwanese online shoppers prefer (資策會 MIC, 2025 Q4) —
    -- because nobody typed a box size would cost more than the counter refusal
    -- it prevents, and it would do it silently. /admin/products badges what has
    -- no measurement, the same way it badges what has no English name.
    --
    -- On the VARIANT and not the product: a 128GB and a 256GB phone ship in the
    -- same box, but they are variants of one product and the column has to sit
    -- where the difference could exist.
    parcel_longest_mm      integer,
    parcel_sum_mm          integer,
    parcel_weight_g        integer,
    position               integer NOT NULL DEFAULT 0,
    is_active              boolean NOT NULL DEFAULT true,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_variants_sku_format CHECK (sku ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'),
    -- A measurement that exists is positive and below anything a courier moves.
    -- The ceilings are deliberately loose: this refuses a typo, not a decision.
    CONSTRAINT product_variants_parcel_longest_sane
        CHECK (parcel_longest_mm IS NULL OR (parcel_longest_mm > 0 AND parcel_longest_mm <= 5000)),
    CONSTRAINT product_variants_parcel_sum_sane
        CHECK (parcel_sum_mm IS NULL OR (parcel_sum_mm > 0 AND parcel_sum_mm <= 15000)),
    CONSTRAINT product_variants_parcel_weight_sane
        CHECK (parcel_weight_g IS NULL OR (parcel_weight_g > 0 AND parcel_weight_g <= 200000)),
    -- The sum of three sides cannot be under the longest of them.
    CONSTRAINT product_variants_parcel_sum_covers_longest
        CHECK (parcel_sum_mm IS NULL OR parcel_longest_mm IS NULL
               OR parcel_sum_mm >= parcel_longest_mm),
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

-- An active product must have something to sell.
--
-- products_active_is_published only requires a published_at, so a product with
-- ZERO variants could go active: a page a customer can reach, with no price,
-- that every listing query drops on the spot because the LATERAL JOIN that
-- picks the cheapest variant finds no row. The back office hides the publish
-- button in that state, but a hidden button is a suggestion — this is the door.
--
-- Deferred, for the reason orders_have_lines is: the variants are necessarily
-- inserted after the product row they reference, and the seed publishes a
-- product and its variants in one transaction.
--
-- It refuses rather than un-publishing. Auto-unpublishing would be the database
-- deciding something the back office should: a trigger may enforce an invariant
-- and may not make a merchandising decision.
CREATE FUNCTION products_check_sellable() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    p products%ROWTYPE;
    sellable integer;
BEGIN
    -- Which product this fires for. The branches are separate statements
    -- rather than one coalesce: on DELETE the NEW record does not exist at all,
    -- and referring to it is a runtime error plpgsql cannot catch at compile
    -- time — a guard that only fails the first time somebody deletes a variant.
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

-- The other direction: deactivating or deleting the LAST active variant of a
-- published product would leave exactly the same state, arrived at from the
-- other side.
CREATE CONSTRAINT TRIGGER product_variants_keep_product_sellable
    AFTER UPDATE OF is_active OR DELETE ON product_variants
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION products_check_sellable();

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
    -- The English pair, or NULL for a row the shop has not translated. A spec is a
    -- LABEL on the product page and a row header on /compare, so it is read by
    -- whoever is looking rather than authored prose — the same argument that moved
    -- category names off the "content" list.
    --
    -- Both halves are separately nullable on purpose: 「螢幕 / 6.3 吋 OLED」 needs
    -- the label translated and the value barely at all, and forcing a shop to
    -- retype a number to translate a word is how a translation feature goes unused.
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
    -- Bounded, in CHARACTERS, because the back office types these. length()
    -- counts characters in PostgreSQL, which is the right unit for the same
    -- reason the question and answer bodies use it — a byte limit gives a Chinese
    -- label a third of the room an English one gets.
    --
    -- A spec is a table cell on /compare. A label longer than this is a sentence,
    -- and a comparison table whose first column wraps to three lines is the thing
    -- that page exists to avoid.
    CONSTRAINT product_specs_label_bounded CHECK (length(label) <= 40),
    CONSTRAINT product_specs_value_bounded CHECK (length(value) <= 200)
);

CREATE UNIQUE INDEX product_specs_position_key ON product_specs (product_id, position);
CREATE INDEX product_specs_label_idx ON product_specs (label);

-- There is deliberately NO search projection table here.
--
-- Search ILIKEs products.name, summary and brands.name directly — measured at
-- 1.5 ms for a Latin query at 10,000 products — so a projection of name, brand,
-- SKUs, option values and spec text, "rebuilt from its sources at any time",
-- would be an empty index that reads to anybody opening the schema as the thing
-- serving search, and that nothing rebuilds and nothing reads.
--
-- The projection that IS the documented next step is a BIGRAM TSVECTOR for short
-- CJK queries, gated on ~5,000 active products: a different shape, so a documents
-- table would not even be a head start. The same call the reserved role names
-- get — the design lives in CLAUDE.md, not in an empty table.

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
    -- `btrim(email) = email` would let a leading tab through, and the folded
    -- unique index below would then hold two rows for one mailbox.
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
    -- AES-256-GCM, keyed from the environment. A TOTP secret is a
    -- password-equivalent: anyone holding it can mint valid codes forever. The
    -- column is encrypted so that a database dump alone does not defeat the
    -- second factor — the key lives where the dump does not.
    secret_encrypted bytea NOT NULL,
    -- NULL until the enrolling person has proved they can generate a code.
    -- Without this an admin could "enable" 2FA with a secret they mistyped into
    -- their authenticator and lock themselves out on the next sign-in.
    confirmed_at     timestamptz,
    -- The most recent time step accepted for this credential.
    --
    -- Replay prevention, and the thing every naive TOTP implementation omits: a
    -- code stays valid for its whole 30-second step, and with the skew window
    -- either side that is 90 seconds in which a shoulder-surfed or phished code
    -- can be replayed. Requiring a STRICTLY GREATER step makes each code usable
    -- exactly once, and refuses an older one after a newer has been seen.
    last_step        bigint,
    created_at       timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT staff_totp_secret_present CHECK (octet_length(secret_encrypted) > 0),
    -- An unconfirmed credential has never been used, so it can have no step.
    CONSTRAINT staff_totp_step_needs_confirmation
        CHECK (last_step IS NULL OR confirmed_at IS NOT NULL)
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
    -- When this session last proved a second factor.
    --
    -- The second factor guards the BACK OFFICE, not the sign-in. A password
    -- alone gets a normal session; reaching /admin needs a TOTP verification
    -- recorded here and recent. That is step-up authentication, and it avoids
    -- inventing a half-authenticated state — there is no session that exists
    -- but does not count.
    --
    -- NULL for every customer session, which is correct: a customer has no
    -- second factor and needs none.
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

-- The back office's customer search, and its only user.
--
-- users already has a UNIQUE index on lower(email), but a btree under the default
-- collation cannot serve a prefix LIKE — so this is a second index rather than a
-- duplicate one, and the reason is worth writing down because it looks like a
-- duplicate.
--
-- A PREFIX for the reason the order search uses one: it is what an index serves
-- without pg_trgm on a table of personal data, and it is what somebody reading
-- their own address out loud gives you.
CREATE INDEX users_email_prefix_idx ON users (lower(email) text_pattern_ops);
CREATE INDEX users_name_prefix_idx ON users (full_name text_pattern_ops);

CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

-- An address waiting to be proved.
--
-- ONE table for two things that are the same act: proving the address a customer
-- registered with, and proving a new one they want to move to. A row says "prove
-- that <email> belongs to <user>", and confirming it sets users.email to that
-- address and stamps email_verified_at. At registration the address is already the
-- user's, so the move is a no-op; on a change it is the whole point.
--
-- This table is what sets users.email_verified_at, and the only thing that does.
-- Underneath it is the half that matters more: UpdateProfile writes full_name and
-- phone, so with no door here a customer cannot change their address at all, and
-- somebody who mistyped it at registration receives nothing, for good.
--
-- The change takes effect only on confirmation. Until then the account keeps the
-- old address, so receipts and reset links keep arriving somewhere the customer
-- can read.
CREATE TABLE email_verifications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The address being proved. Not a foreign key to anything: it may not be the
    -- user's current address, and that is the case this table exists for.
    -- text and compared with lower(), the way users.email is: citext is not
    -- installed in this schema and adding an extension for one column is a
    -- dependency for nothing.
    email      text NOT NULL,
    -- sha256 of the token in the link, for the reason every other token in this
    -- schema is stored as a digest: a read of the table must not hand somebody a
    -- live link. Unlike the newsletter's unsubscribe secret, this one is spent on
    -- first use and expires, so nothing needs to reproduce it.
    digest     bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT email_verifications_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT email_verifications_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT email_verifications_digest_sha256 CHECK (octet_length(digest) = 32),
    CONSTRAINT email_verifications_expires_after_created CHECK (expires_at > created_at)
);

-- One outstanding request per customer. Asking again REPLACES the previous, so a
-- mailbox holds one live link rather than three — the same rule the newsletter's
-- confirmations follow, and for the same reason.
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
    -- Who posted this, when a person did. NULL for a posting the application
    -- made on its own — a checkout spending credit has no staff member behind
    -- it — and SET NULL on delete so erasing an employee leaves the ledger
    -- balanced rather than blocking on it.
    --
    -- Granting store credit is giving money away, and inventory_movements and
    -- order_events both record their actor for the same reason: the entry that
    -- cannot say who made it is the one nobody can question.
    actor_user_id  uuid REFERENCES users (id) ON DELETE SET NULL,
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
-- Every foreign key gets an index, and this one earns it twice over: erasing a
-- staff member has to find their postings to null them, and "what did this
-- person give away" is the first question anyone asks of a credit ledger.
CREATE INDEX store_credit_entries_actor_idx ON store_credit_entries (actor_user_id);

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
        -- A reversal undoes an original posting, not another reversal. Reversing
        -- a reversal would net back to the original spend while escaping the
        -- order-state rules below (a reversal carries no order_id, so those rules
        -- do not see it), and the meaning of a chain of reversals is nobody's
        -- intent. Keep reversals one layer deep; a correction is a new posting.
        IF original.reverses_id IS NOT NULL THEN
            RAISE EXCEPTION 'a reversal cannot itself be reversed; post a new entry to correct'
                USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_reversal_single_layer';
        END IF;
    END IF;

    -- A posting attributed to an order takes part in that order's funding, so it
    -- must serialise against the order's fulfilment transition and obey the
    -- order's state. A spend carries the order_id directly; a reversal inherits
    -- it from the entry it reverses. Lock that order FIRST — this is the shared
    -- lock the funding path (which locks the order row on its UPDATE) and the
    -- store-credit path otherwise lacked. Without it a reversal and a
    -- pending->picking transition could each pass their own check and combine
    -- into a picked order whose credit was refunded (predictable mistake #9).
    o_id := CASE WHEN NEW.reverses_id IS NOT NULL THEN original.order_id ELSE NEW.order_id END;
    IF o_id IS NOT NULL THEN
        SELECT fulfillment_status,
               EXISTS (SELECT 1 FROM payments WHERE order_id = o_id AND status = 'succeeded')
        INTO o_status, o_paid
        FROM orders WHERE id = o_id FOR UPDATE;

        IF NEW.reverses_id IS NULL AND NEW.amount_cents < 0 THEN
            -- SPENDING credit on an order is part of paying for it: only while it
            -- is still an open, unpaid checkout.
            IF o_status <> 'pending' OR o_paid THEN
                RAISE EXCEPTION 'store credit cannot be spent on order % (status %, paid %)',
                    o_id, o_status, o_paid
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_posting_matches_order';
            END IF;
        ELSIF NEW.reverses_id IS NULL THEN
            -- A POSITIVE entry attributed to an order is a COMPENSATION: money
            -- being given back on an order that has already been paid for and
            -- gone out. That is the case the branch below prescribes for a shipped
            -- order, and it is a different rule from the branch above, which tests
            -- the ORDER STATE alone. Testing the state without the SIGN makes
            -- "credit spent on an order" and "credit returned on an order" one
            -- rule, and a RETURN of a credit-funded order can then not be paid at
            -- all: the card portion is short of the claim, and the credit portion
            -- has no legal way onto the ledger.
            --
            -- Legal once the order is settled and not before. While it is still an
            -- open unpaid checkout there is nothing to compensate: the right move
            -- is to reverse the spend, which is the branch below.
            IF o_status = 'pending' AND NOT o_paid THEN
                RAISE EXCEPTION 'order % is an open unpaid checkout; reverse the spend rather than compensating it',
                    o_id
                    USING ERRCODE = 'check_violation', CONSTRAINT = 'store_credit_posting_matches_order';
            END IF;
        ELSE
            -- Reversing an order's spend refunds it to the account, which un-funds
            -- the order. Allowed only while the order is still an unpaid checkout
            -- (abandonment) or after it was cancelled. A funded or shipped order
            -- is compensated with a refund or a new positive entry, not a reversal.
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
-- an abandoned checkout cannot keep an item out of the store indefinitely and a
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
-- variant can be taken. A non-partial unique index would make re-holding
-- impossible.
CREATE UNIQUE INDEX inventory_reservations_order_variant_key
    ON inventory_reservations (order_id, variant_id)
    WHERE state = 'held';
-- The unique index above is partial, so it covers the order_id foreign key only
-- for held rows; a plain index covers the rest.
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

-- Consume PART of a hold, for a dispatch that sends some of what was ordered.
--
-- A parcel can carry two of the three units an order holds — the third is on
-- back-order, or would not fit the carrier's box. The whole-row form above
-- cannot say that: it settles the hold entirely, so the units still in the
-- warehouse would read as gone and a later parcel would have no hold to settle.
--
-- SPLITTING rather than a consumed_quantity column, because one row then records
-- one settled fact and the sum over rows per (order, variant) stays exactly what
-- was held. A quantity column beside a state would make `settled_has_state` a
-- lie: the row would be partly settled and partly not, and every reader would
-- need to know which half it was looking at.
--
-- The consumed row carries the ORIGINAL's created_at and expires_at rather than
-- inventing new ones. It records a hold that was taken then and expired then; it
-- has merely stopped being outstanding. Carrying them is also what keeps
-- `expiry_after_creation` true, which a row created now against an expiry in the
-- past would not be.
--
-- The partial unique index is on (order_id, variant_id) WHERE state = 'held', so
-- the inserted row does not collide with the remainder it was split from.
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

    -- More than is held is a dispatch of goods this order never reserved. The
    -- caller's own arithmetic should have caught it; refusing here is what makes
    -- that true rather than assumed, because this is the one place holding the
    -- row under a lock.
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

-- Release a hold — cancelled checkout or expiry sweep: return the stock and
-- close the reservation, once. A hold on a COMMITTED order may NOT be released:
-- the sweeper could otherwise expire the hold of an order that is paid but not
-- yet picked and hand its stock back to the shelf, reselling a sold item. The
-- only exit for such a hold is consume_reservation.
--
-- TWO questions, because one of them cannot see the other's case. Reading
-- order_is_committed as covering both is the shape CLAUDE.md #13 and #30
-- describe: a comment and its predicate, each correct on its own and
-- disagreeing with each other.
--
--   1. order_is_committed — a succeeded payment, or a status past pending.
--   2. order_amount_owed = 0 on an order that is not cancelled.
--
-- (2) is the one committed_orders is blind to. A fully store-credited order —
-- or one a 100% coupon zeroed — has no payment row, because
-- payments_succeeded_is_captured forbids a zero-value succeeded payment, and it
-- legally sits at 'pending' until a human picks it, because
-- orders_funded_to_leave_pending has nothing left to demand. So it is funded,
-- not committed, and without (2) the sweeper takes its stock back thirty minutes
-- after the customer paid for it — measured, not theorised: the units go back on
-- the shelf and Ship then writes a parcel while consuming no reservation at all,
-- leaving stock_quantity permanently one too high.
--
-- 'cancelled' is excluded because a cancelled order's stock MUST come back, and
-- reverse_order_credit runs after the status move — so for the moment the
-- release is asked for, a cancelled order can still read as owing nothing.
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

    -- Variant first, then order — the same order hold_inventory takes them in
    -- (it locks the variant through record_inventory_movement, then the order
    -- through the reservation's foreign key). Reversed, a concurrent re-hold and
    -- release of the same (order, variant) close a cycle and PostgreSQL aborts
    -- one of them with 40P01. No caller retries a deadlock today, so the cost
    -- would be a failed checkout, not just a slow one.
    PERFORM 1 FROM product_variants WHERE id = r.variant_id FOR UPDATE;
    SELECT o.fulfillment_status INTO o_status
    FROM orders o WHERE o.id = r.order_id FOR UPDATE;
    -- COMMITTED, not settled: a cancelled order is settled and its stock must
    -- come back, which is the whole reason the two are separate views.
    IF order_is_committed(r.order_id) THEN
        RAISE EXCEPTION 'reservation % is on a committed order; consume it, do not release', p_reservation_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inventory_reservation_committed_no_release';
    END IF;

    -- FUNDED but not committed — the case the view above cannot answer. See the
    -- header: this is the zero-owed order, paid for and still pending.
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

-- Reservations are written only through those three functions; store's
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

-- The retention sweep's range, and its only user. Without it the daily delete is
-- a sequential scan over every checkout goen has ever seen — which is the table
-- this index exists to stop growing.
CREATE INDEX checkout_attempts_created_at_idx ON checkout_attempts (created_at);

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
    -- The language the visitor was reading when they asked. Kept here for the
    -- same reason orders.locale is: the notice is produced by a back-office
    -- stock adjustment, where the person who asked is not present.
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
-- The partial index above covers only pending rows, so it cannot serve the
-- delete of a variant that has already notified someone.
CREATE INDEX stock_notifications_variant_id_idx ON stock_notifications (variant_id);
CREATE INDEX stock_notifications_user_id_idx ON stock_notifications (user_id);

-- ============================================================================
-- Reviews
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 商品問答
--
-- A review and a question are different things and this is why they are
-- different tables: a review comes from somebody who BOUGHT and is looking
-- back; a question comes from somebody DECIDING and is looking forward. The
-- second is the one a 選品店 exists to answer, and it is worth nothing if the
-- answer arrives after the sale.
--
-- An answer may come from the shop or from another customer. Both are useful
-- and they are not the same claim, which is why is_staff is a stored fact
-- rather than a join anybody could get wrong: a customer who later joins the
-- shop must not retroactively turn their old answers into official ones.
-- ---------------------------------------------------------------------------

CREATE TABLE product_questions (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    -- Nullable for the same reason a review's is: erasure must not take a
    -- published question with it, and must not be blocked by having asked one.
    user_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    body       text NOT NULL,
    -- Hidden by staff. NOT a moderation queue: a question is visible the
    -- moment it is asked, because a question nobody sees is a question nobody
    -- answers, and a shop that reviews every one before publishing answers
    -- none of them in time. Hiding is the exception and it is recorded.
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
    -- Whether the SHOP said it, recorded at the moment it was said.
    --
    -- Not derived from the author's current role: a customer who later joins
    -- the shop would retroactively turn their old answers into official ones,
    -- and a staff member who leaves would strip the badge from answers that
    -- were official when written. Either way the page would be lying about
    -- who said what.
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
    -- Nullable: a customer exercising their right to erasure must not take a
    -- published review with them, and must not be blocked by having left one.
    user_id              uuid REFERENCES users (id) ON DELETE SET NULL,
    rating               smallint NOT NULL,
    title                text,
    body                 text NOT NULL,
    is_verified_purchase boolean NOT NULL DEFAULT false,
    -- Hidden by staff, the same shape product_questions.hidden_at has and for
    -- the same reason: a review is visible the moment it is written, because a
    -- shop that approves every one before publishing has a review page that
    -- reads like an advertisement. Hiding is the exception and it is recorded.
    --
    -- What makes it worth having: the displayed rating is computed LIVE from
    -- these rows, so an abusive or planted one-star review moves a product's
    -- public score, and the only recourse was SQL. A hidden review is excluded
    -- from the aggregate as well as from the list — hiding it and leaving the
    -- score alone would achieve nothing.
    --
    -- Nullable because the overwhelming majority are not hidden, and a boolean
    -- would not say WHEN.
    hidden_at            timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_reviews_rating_range CHECK (rating BETWEEN 1 AND 5),
    CONSTRAINT product_reviews_body_present CHECK (body ~ '[^[:space:]]')
);

-- The reviews the shop shows, which is what every rating is computed from.
--
-- A VIEW and not a predicate repeated at each call site, for the reason
-- committed_orders is one: eleven queries across five packages compute a rating
-- or a review count — the PDP, the listing, search, the home page, the account
-- — and a hidden review excluded from some of them means the same product shows
-- two different scores. One definition, and the twelfth reader cannot forget.
--
-- The base table is still read by exactly two things: the moderation queries,
-- which need the hidden rows to un-hide them, and HasReviewed, which must see a
-- hidden review or the customer writes a second one and meets the unique index.
CREATE VIEW visible_reviews AS
    SELECT id, product_id, user_id, rating, title, body,
           is_verified_purchase, created_at
    FROM product_reviews
    WHERE hidden_at IS NULL;

COMMENT ON VIEW visible_reviews IS
    'Reviews that count: not hidden by staff. Every rating and review count '
    'reads this, so hiding one moves the score as well as the list.';

-- The 已購買 claim is not the caller's to make. is_verified_purchase is a plain
-- boolean with no link to an order line and no moderation column, and the table
-- keeps its ordinary DML — so without this the flag is whatever the writer says
-- it is, while the displayed rating is computed live from these rows and a row
-- claiming a purchase that never happened moves a product's public score. The
-- claim is checked here against a real committed order for the same product.
--
-- Only the moment the flag is SET is guarded. Once true it stays true, because
-- erase_user nulls user_id through the foreign key and re-verifying an erased
-- author is impossible — a flag that could not survive erasure would make the
-- review history depend on whether its author still exists.
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

-- One review per product per person. The average and the count are computed
-- from these rows and never stored on the product.
CREATE UNIQUE INDEX product_reviews_author_key ON product_reviews (product_id, user_id);
CREATE INDEX product_reviews_product_created_idx ON product_reviews (product_id, created_at DESC);
CREATE INDEX product_reviews_user_id_idx ON product_reviews (user_id);

-- ============================================================================
-- Coupons
--
-- A coupon is a PROMISE with a shape, not a number typed at checkout. The kind
-- decides how it is applied and what else must be present, so the schema
-- refuses a half-described one rather than leaving the handler to guess.
--
-- Amounts are integer cents like every other money column. A percentage is
-- basis points — 2500 is 25% — because storing 0.25 as a float is how a
-- discount comes out a cent short on some orders and a cent over on others.
-- ============================================================================

CREATE TABLE coupons (
    id                  uuid PRIMARY KEY DEFAULT uuidv7(),
    -- What the customer types. Compared case-insensitively (the unique index
    -- below is on upper(code)), because a code printed on a card is read by a
    -- human, not copied by a machine.
    code                text NOT NULL,
    -- Shown on the cart when it applies, so the customer sees what they got
    -- rather than an unexplained smaller number.
    description         text NOT NULL,
    kind                text NOT NULL,
    -- Exactly one of these carries the value, decided by kind. The CHECK below
    -- is what stops a percentage coupon with an amount and no percentage.
    amount_cents        bigint,
    percent_bp          integer,
    -- The order must reach this before the coupon applies at all. 0 means no
    -- minimum, which is the common case and so the default.
    min_subtotal_cents  bigint NOT NULL DEFAULT 0,
    -- A percentage coupon on a NT$50,000 laptop is a large number; this is how
    -- a "20% off, up to NT$500" promotion is expressed. NULL means uncapped.
    max_discount_cents  bigint,
    starts_at           timestamptz NOT NULL DEFAULT now(),
    ends_at             timestamptz,
    -- How many times it may be used in total, and by one customer. NULL is
    -- unlimited; the redemption table below is what counts.
    max_redemptions     integer,
    per_customer_limit  integer NOT NULL DEFAULT 1,
    is_active           boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT coupons_code_format CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9-]{1,31}$'),
    CONSTRAINT coupons_description_present CHECK (description ~ '[^[:space:]]'),
    CONSTRAINT coupons_kind_known CHECK (kind IN ('amount', 'percent', 'free_shipping')),
    -- Each kind carries exactly what it needs and nothing it does not. Written
    -- as an equivalence per kind rather than three loose CHECKs, because
    -- "amount IS NOT NULL OR percent IS NOT NULL" would admit both at once and
    -- leave the handler to decide which wins.
    CONSTRAINT coupons_value_matches_kind CHECK (
        (kind = 'amount'        AND amount_cents IS NOT NULL AND percent_bp IS NULL)
     OR (kind = 'percent'       AND percent_bp IS NOT NULL   AND amount_cents IS NULL)
     OR (kind = 'free_shipping' AND amount_cents IS NULL     AND percent_bp IS NULL)
    ),
    CONSTRAINT coupons_amount_positive
        CHECK (amount_cents IS NULL OR (amount_cents > 0 AND amount_cents <= 10000000000)),
    -- Above 0 and at most 100%. A 120% coupon would pay the customer to shop.
    CONSTRAINT coupons_percent_in_range
        CHECK (percent_bp IS NULL OR (percent_bp > 0 AND percent_bp <= 10000)),
    CONSTRAINT coupons_min_subtotal_non_negative
        CHECK (min_subtotal_cents >= 0 AND min_subtotal_cents <= 10000000000),
    CONSTRAINT coupons_max_discount_positive
        CHECK (max_discount_cents IS NULL OR max_discount_cents > 0),
    -- A cap only means something on a percentage. On a fixed amount it would be
    -- a second, quieter amount that silently overrides the first.
    CONSTRAINT coupons_cap_only_on_percent
        CHECK (max_discount_cents IS NULL OR kind = 'percent'),
    CONSTRAINT coupons_window_ordered CHECK (ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT coupons_redemptions_positive
        CHECK (max_redemptions IS NULL OR max_redemptions > 0),
    CONSTRAINT coupons_per_customer_positive CHECK (per_customer_limit > 0)
);

-- Case-insensitive uniqueness. A shop cannot have both SUMMER20 and summer20:
-- to the customer reading a card they are the same code, and issuing both is
-- how one of them silently stops working.
CREATE UNIQUE INDEX coupons_code_key ON coupons (upper(code));
CREATE INDEX coupons_active_idx ON coupons (is_active, starts_at, ends_at);

-- What keeps coupons.updated_at truthful. Without it the column says "last
-- changed" and holds the creation time forever.
--
-- That is departure #3 in CLAUDE.md, from underneath. The whole argument for
-- keeping the timestamp in a trigger rather than at each write site is that "a
-- single set_updated_at trigger cannot forget" — true, and it says nothing about
-- a table that never gets one. The write that matters most is the one the back
-- office makes: /admin/coupons switches a promotion OFF rather than deleting it,
-- and that is exactly the change an unkept column fails to record.
--
-- TestEveryColumnIsReadOrWritten is what asks, and only because its match is
-- scoped to the column's own table: an updated_at written by nothing and read by
-- nothing otherwise passes on the strength of every OTHER table's updated_at.
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
-- Methods are versioned because a fee is a promise made at a moment. Changing
-- 宅配 from NT$80 to NT$100 must not make last month's orders unexplainable.
-- ============================================================================

CREATE TABLE shipping_methods (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    code       text NOT NULL,
    -- WHERE this method delivers to, which decides what the checkout must ask
    -- for. 'address' wants a street; 'pickup_point' wants a convenience store,
    -- and a street address for one is data nobody will ever use.
    --
    -- It is a property of the METHOD rather than a branch in the handler on
    -- code = 'store_pickup'. A rule written in Go is a rule the next method
    -- forgets: 宅配 and 超商 are two of several a Taiwanese shop eventually
    -- offers, and the schema is where the question "what does this one need?"
    -- has one answer.
    destination_kind text NOT NULL DEFAULT 'address',
    -- What this method's carrier will physically accept, per parcel. NULL means
    -- no stated limit, which is the honest default for 宅配 — a courier takes
    -- what fits in a van.
    --
    -- 超商店到店 is the reason these exist. 7-ELEVEN and 全家 refuse a parcel
    -- over 45cm on its longest side, 105cm across three sides, or 10kg; 萊爾富
    -- stops at 5kg. Without them a 3C shop offers 超商取貨 for a 27-inch monitor,
    -- the customer chooses it, the order is placed and paid, and the shop finds
    -- out at the counter — which is the worst place, because the parcel is
    -- already packed and the customer is already waiting.
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
    -- The English name and carrier. Part of the VERSION's copy, like the fee: a
    -- past order names the version it was priced from, so translating a name here
    -- cannot rewrite what an older order says it chose.
    --
    -- The checkout's method chooser is where these are read, which makes them the
    -- last piece of shop-typed chrome on the buying mainline — an English customer
    -- picking a delivery method was reading 宅配到府 and 超商取貨 at the moment of
    -- paying.
    name_en         text,
    carrier_en      text,
    fee_cents       bigint NOT NULL,
    -- The order value at or above which this method ships free. NULL means the
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

-- 離島. A shipping fee that is one number for the whole country charges a
-- Taipei price to send a parcel to 金門, which the shop pays for out of the
-- margin, and offers 超商取貨 to places the chain may not reach at all.
--
-- A ZONE is a set of postal-code prefixes. The prefix is the primary key of the
-- membership table, so a prefix belongs to exactly one zone and "which zone is
-- this address in" has one answer — a postcode in two zones would be a fee that
-- depends on which row the planner returned first.
CREATE TABLE shipping_zones (
    id       uuid PRIMARY KEY DEFAULT uuidv7(),
    code     text NOT NULL,
    name     text NOT NULL,
    -- 離島 → Outlying islands. Read on /shipping and in the surcharge sentence the
    -- checkout shows before charging it.
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
    -- The three-digit prefix of a Taiwanese postal code. Three and not five,
    -- because the surcharge is decided by the 鄉鎮市區 and the last two digits
    -- are the delivery route within it.
    prefix  text PRIMARY KEY,
    zone_id uuid NOT NULL REFERENCES shipping_zones (id) ON DELETE RESTRICT,
    CONSTRAINT shipping_zone_prefixes_format CHECK (prefix ~ '^[0-9]{3}$')
);

CREATE INDEX shipping_zone_prefixes_zone_idx ON shipping_zone_prefixes (zone_id);

-- What one method charges extra for one zone.
--
-- A version with NO row for a zone serves it at no surcharge. That is
-- deliberate: a shop that has never thought about zones ships everywhere at one
-- price, and this table must not change what an untouched install does.
--
-- There is NO "serviceable" flag, and that is a decision rather than an
-- omission: a zone is found from the POSTAL CODE, and the only method that could
-- not serve 離島 is 超商取貨 — which has no postal code at all, because its
-- destination is a store. The flag's one real configuration could never fire.
--
-- Which chain has a 離島 store is the chain's own answer, and goen does not
-- have their store list (the same 電子地圖 integration the picker is missing).
-- A method that refuses a zone can have the column when there is one.
CREATE TABLE shipping_version_zones (
    version_id      uuid NOT NULL REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
    zone_id         uuid NOT NULL REFERENCES shipping_zones (id) ON DELETE RESTRICT,
    surcharge_cents bigint NOT NULL,
    PRIMARY KEY (version_id, zone_id),
    CONSTRAINT shipping_version_zones_surcharge_positive
        CHECK (surcharge_cents > 0)
);

-- The primary key leads on version_id, so a lookup by zone — "which methods
-- charge extra for 離島", which the back office asks — has nothing to use.
CREATE INDEX shipping_version_zones_zone_idx ON shipping_version_zones (zone_id);

COMMENT ON COLUMN shipping_version_zones.surcharge_cents IS
    'Added to the fee AFTER the free-over threshold is applied. 免運 covers the '
    'base rate the shop advertises, never the 離島 surcharge a carrier charges '
    'on top of it.';

-- ============================================================================
-- Orders
--
-- One column per lifecycle. Payment, fulfilment and returns in a single `status`
-- cannot express a shipped order with a refund request open — and, worse, make
-- two concurrent writers of unrelated facts overwrite each other. Payment state
-- lives in `payments`, return state in `return_requests`, and what remains here
-- is fulfilment.
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
    -- There is deliberately NO discount_code column here.
    --
    -- The reflex is to snapshot one — orders already snapshots
    -- shipping_method_code beside its FK, on the argument that "the snapshot
    -- survives the version being superseded". A coupon has nothing to survive:
    -- coupons.code is never updated (the back office switches a coupon OFF rather
    -- than editing or deleting it) and coupon_redemptions holds it by FK with
    -- ON DELETE RESTRICT, so the code is always reachable by one join.
    --
    -- So the column would be a second copy of a recoverable fact, which is the
    -- shape product_images and hero_slides are each caught by. What an order
    -- needs instead is a READER: without one it shows "折扣 −NT$200" and nothing
    -- anywhere says which discount, to the customer or to the shop.
    -- The version that was in force, plus its name as shown. The FK explains
    -- the price; the snapshot survives the version being superseded.
    shipping_version_id  uuid NOT NULL REFERENCES shipping_method_versions (id) ON DELETE RESTRICT,
    shipping_method_code text NOT NULL,
    shipping_method_name text NOT NULL,
    customer_note        text,
    staff_note           text,
    -- The language the order was placed in, so every message ABOUT it speaks it.
    --
    -- On the order rather than in the outbox payload because two of those
    -- messages are produced with no visitor present: the receipt comes from a
    -- Stripe webhook and the dispatch notice from a back-office click. Reading
    -- the locale from whoever happened to trigger it would send a Taiwanese
    -- shopkeeper's language to an English customer.
    --
    -- A snapshot, like the shipping version and the delivery details beside it:
    -- what was true when the order was placed. Somebody who switches the site
    -- to Chinese next year does not retroactively change the receipt they were
    -- sent.
    locale               text NOT NULL DEFAULT 'zh-Hant',
    placed_at            timestamptz NOT NULL DEFAULT now(),
    cancelled_at         timestamptz,
    completed_at         timestamptz,
    -- There is deliberately NO created_at column here.
    --
    -- placed_at is the moment, and every listing, report and account page reads
    -- it. A created_at beside it, both DEFAULT now(), would be a second copy of
    -- one fact that could only ever disagree with the first — the exact shape a
    -- discount_code column is refused for, and a search projection table before
    -- it. TestEveryColumnIsReadOrWritten is what surfaces one, and only because
    -- its match is scoped to the column's own table; unscoped, it passes on every
    -- other table's created_at.
    updated_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT orders_number_format CHECK (order_number ~ '^GO-[0-9]{6}-[0-9]{6}$'),
    CONSTRAINT orders_currency_is_twd CHECK (currency = 'TWD'),
    -- The set i18n.Locales() speaks. A locale goen cannot render is a message
    -- nobody can read, and the fallback would be silent.
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
    -- An order cannot both have been called off and have run to completion.
    CONSTRAINT orders_not_both_ended
        CHECK (cancelled_at IS NULL OR completed_at IS NULL),
    -- Each ending, if recorded, happened after the order existed.
    CONSTRAINT orders_cancelled_after_placed
        CHECK (cancelled_at IS NULL OR cancelled_at >= placed_at),
    CONSTRAINT orders_completed_after_placed
        CHECK (completed_at IS NULL OR completed_at >= placed_at),
    -- One-way, deliberately. The status may move on — a cancelled order can
    -- afterwards be refunded — but the moment it was called off is history and
    -- keeps its timestamp. A two-way form, demanding the status wherever the
    -- timestamp is set, makes "cancelled then refunded" impossible to express at
    -- all.
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
        -- order_amount_owed is the ONE definition: total less store credit, net of
        -- reversals. Written out here and again in the capture guard, with the
        -- payment page carrying no copy at all, is how Stripe comes to be sent a
        -- figure this database refuses.
        owed := order_amount_owed(NEW.id);
        IF owed <> 0
           AND NOT EXISTS (SELECT 1 FROM payments
                           WHERE order_id = NEW.id AND status = 'succeeded') THEN
            RAISE EXCEPTION 'order % cannot leave pending unfunded (owes %)',
                NEW.order_number, owed
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

-- What lets a browser see a guest's order.
--
-- The cookie carries a high-entropy TOKEN and this table holds its DIGEST — the
-- same shape sessions, reset tokens and cart tokens already use: a database dump
-- does not yield a working credential, and there is nothing to enumerate.
--
-- What it replaces is the obvious design, where the cookie holds the ORDER NUMBER
-- and the number is the proof. Numbers come off a per-day counter —
-- GO-260803-000001, then 000002 — so anybody can set that cookie by hand,
-- increment, and read a stranger's email, address and items, then cancel the
-- order, start a payment or open a return. `__Host-`, Secure and HttpOnly govern
-- how a BROWSER treats a cookie; they prove nothing about where the value came
-- from, and curl does not care.
--
-- Several rows per order on purpose. Somebody who places an order on a phone and
-- then proves the email at /orders/find on a laptop should not lose the first
-- browser's access — rotating one digest would do exactly that.
CREATE TABLE order_access_grants (
    digest     bytea PRIMARY KEY,
    order_id   uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT order_access_grants_digest_sha256 CHECK (octet_length(digest) = 32)
);

CREATE INDEX order_access_grants_order_idx ON order_access_grants (order_id);

-- Keyed on created_at because that is what the SWEEP asks: a grant carries no
-- expiry column of its own, so retention is the only thing that bounds it.
--
-- The cookie carrying the token has a 30-day MaxAge, so a grant older than that
-- is unreachable by any browser — a dead credential kept forever, which is
-- precisely what outbox_messages, password_reset_tokens and checkout_attempts
-- each carry a policy to stop being. The one credential in this schema
-- deliberately left non-expiring is the newsletter unsubscribe token, and that
-- decision is written down beside it.
CREATE INDEX order_access_grants_created_at_idx ON order_access_grants (created_at);

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
    -- The 超商門市 a pickup order is collected from. Three columns rather than
    -- one, because they answer different questions: the brand decides which
    -- carrier's manifest the parcel goes on, the code identifies the store to
    -- that carrier, and the name is what a human reads on the label and on the
    -- customer's order page. A single free-text "門市" would be all three
    -- mashed together and machine-readable as none of them.
    --
    -- They live HERE, with the address, because they are the same fact — where
    -- somebody's parcel goes — with the same privacy and the same lifetime.
    -- erase_user clears them together.
    pickup_brand      text,
    pickup_store_code text,
    pickup_store_name text,
    erased_at      timestamptz,
    -- Two exhaustive states, not "erased iff nothing set". The weaker form —
    -- `erased_at IS NOT NULL = (all NULL)` — is satisfied by a LIVE row that
    -- happens to have only some fields cleared: an email, no name, no erased_at.
    -- Delivery needs every field, or the row is erased and holds none.
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
    -- A live row carries EXACTLY ONE destination: a street address or a pickup
    -- point, never both and never neither. Neither is an order nobody can
    -- deliver; both is two answers to one question, and the picker on the
    -- packing bench has to guess which the customer meant.
    --
    -- Written as two all-or-nothing groups and an XOR rather than as
    -- "street IS NOT NULL OR pickup_store_code IS NOT NULL", which a row
    -- carrying a city and no street would satisfy.
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
    -- The four brands a Taiwanese shop ships to. An allowlist rather than free
    -- text, because the brand decides which carrier's manifest the parcel joins
    -- and a typo there is a parcel that never leaves.
    CONSTRAINT order_private_data_pickup_brand_known CHECK (
        pickup_brand IS NULL
        OR pickup_brand IN ('seven_eleven', 'family_mart', 'hi_life', 'ok_mart')
    ),
    -- Digits or uppercase letters, bounded at the length a 門市代碼 is published
    -- with. Digits ALONE reads as obvious — "what is true of all of them is that
    -- a store code is a number" — and it is a GUESS, in a CHECK, which is the
    -- strongest thing this schema can say.
    --
    -- It is also wrong. Measured against 綠界's own GetStoreList on 2026-08-06:
    -- 7-ELEVEN (6,080 stores), 全家 (3,449) and OK (688) number theirs in six
    -- digits, but 萊爾富 uses FOUR characters and 149 of its 1,350 lead with a
    -- letter — S884, H869, G850. Every one of those is a checkout a digits-only
    -- rule refuses, with no other way through, for one customer at a time and
    -- visible to nobody else.
    --
    -- The rule still does the job it is written for: a 店名 typed into the code
    -- field is Han text, which is in neither class.
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

-- Which lines, and how many of each, went in this parcel. A two-item order
-- shipped in two boxes has two shipments and four rows here.
--
-- order_id is carried so the composite foreign keys can enforce that the line
-- and the shipment belong to the SAME order. Two plain foreign keys cannot: with
-- them, a shipment of order A can carry a line of order B.
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

-- The back office's order search, and its only users.
--
-- Without them /admin/orders can filter by STATUS and take the newest N and
-- nothing else, so a staff member on the phone to a customer reaches an order
-- only by typing a URL they already know — and by name or by address, not at all.
--
-- text_pattern_ops because the search is a PREFIX: that is what an index can serve
-- without a trigram extension, and it is what somebody reading their own name or
-- address out loud gives you — a Chinese name starts with the surname, and an
-- address starts with the part people type first. A substring search would need
-- pg_trgm on a table holding PII, for a back office that has the order number in
-- almost every case.
CREATE INDEX order_private_data_email_prefix_idx
    ON order_private_data (lower(email) text_pattern_ops);
CREATE INDEX order_private_data_recipient_prefix_idx
    ON order_private_data (recipient_name text_pattern_ops);

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

-- A coupon redeemed on an order.
--
-- This is what makes the limits real: max_redemptions and per_customer_limit
-- are counted from these rows, never from a column on the coupon that two
-- concurrent checkouts could each read and each increment.
--
-- ON DELETE RESTRICT on both sides. A redeemed coupon is part of what an order
-- was charged, so deleting either would make the order's discount
-- unexplainable.
CREATE TABLE coupon_redemptions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    coupon_id    uuid NOT NULL REFERENCES coupons (id) ON DELETE RESTRICT,
    order_id     uuid NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    -- NULL for a guest checkout. The per-customer limit therefore binds on
    -- accounts only, which is stated rather than hidden: a guest can re-use a
    -- code by checking out again, and the total cap is what bounds that.
    user_id      uuid REFERENCES users (id) ON DELETE SET NULL,
    -- What it actually took off, computed at checkout and frozen here. The
    -- coupon may be edited afterwards; what this order was given may not
    -- change.
    amount_cents bigint NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT coupon_redemptions_amount_non_negative
        CHECK (amount_cents >= 0 AND amount_cents <= 10000000000)
);

-- One coupon per order. Stacking is a policy decision with real arithmetic
-- behind it (which applies first, do minimums see the other's discount), and
-- goen does not make it: the index refuses the state rather than leaving the
-- question to whichever handler wrote last.
CREATE UNIQUE INDEX coupon_redemptions_order_key ON coupon_redemptions (order_id);
CREATE INDEX coupon_redemptions_coupon_idx ON coupon_redemptions (coupon_id);
CREATE INDEX coupon_redemptions_user_idx ON coupon_redemptions (user_id);

-- A redemption is history. Editing what an order was discounted afterwards
-- makes its total unexplainable, the same reason order_lines freeze.
CREATE TRIGGER coupon_redemptions_append_only
    BEFORE UPDATE OR DELETE ON coupon_redemptions
    FOR EACH ROW EXECUTE FUNCTION forbid_change('coupon_redemptions_append_only');

-- The redemption must match what the order was actually discounted.
--
-- Without this the two are independent numbers: a redemption row saying NT$200
-- against an order whose discount_cents is 0 — or 2000 — and nothing to say
-- which is right. They are one fact, so the database keeps them one.
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

-- Once money has been captured, the itemisation that justified it is history.
-- Editing a price afterwards makes the payment unexplainable; a correction is
-- a refund, not an UPDATE.
-- "This order is committed" — ONE definition, because the wrong one has three
-- exits.
--
-- Three guards ask it: this file's order_lines_freeze, orders_freeze_money and
-- release_reservation. Reading `EXISTS (succeeded payment)` as "committed" is
-- false for a legitimate second kind of funded order. A zero-owed order — 100%
-- discount, or fully paid from store credit — leaves pending with no payment row
-- at all, because orders_funded_to_leave_pending skips its payment check entirely
-- when `order_total - credit_applied = 0`, while payments_succeeded_is_captured
-- forbids a zero-value succeeded payment. Such an order can therefore never have
-- one, and all three guards then do nothing for it silently: its lines, its
-- totals and its held stock stay editable after it ships.
--
-- Committed means money has settled against the order OR the order has left
-- pending. The second half is what covers the zero-owed case, and it is sound
-- for every order because orders_check_transition already refuses to let an
-- unfunded order leave pending.
--
-- It lives in one function so the next change cannot fix one exit and miss two.

CREATE FUNCTION order_lines_freeze() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    target uuid := coalesce(NEW.order_id, OLD.order_id);
BEGIN
    -- On UPDATE a line cannot move to another order, or a committed line could
    -- be carried into an open one to escape the freeze.
    IF TG_OP = 'UPDATE' AND NEW.order_id <> OLD.order_id THEN
        RAISE EXCEPTION 'an order line cannot change orders'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_committed';
    END IF;

    -- Lock the order so a concurrent capture — or a concurrent transition out
    -- of pending — cannot land between this check and the commit. Without it,
    -- T1 edits the line while T2 inserts the succeeded payment, and both pass.
    PERFORM 1 FROM orders WHERE id = target FOR UPDATE;

    -- SETTLED, not committed: a cancelled order's lines are history too, and
    -- committed_orders deliberately excludes it so its stock can come back.
    IF order_is_settled(target) THEN
        RAISE EXCEPTION 'order % is settled; its lines cannot change', target
            USING ERRCODE = 'check_violation', CONSTRAINT = 'order_lines_frozen_once_committed';
    END IF;
    RETURN coalesce(NEW, OLD);
END;
$$;

-- INSERT included: bound to UPDATE and DELETE alone, this trigger lets a paid
-- order accept a brand-new line.
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
       -- The language the order was placed in is part of the record: a receipt
       -- was already sent in it, and changing it afterwards would make the row
       -- disagree with what somebody was emailed.
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

CREATE TRIGGER orders_money_frozen_once_committed
    BEFORE UPDATE ON orders
    FOR EACH ROW EXECUTE FUNCTION orders_freeze_money();

-- cancelled_at and completed_at are the moments themselves, and this is what
-- holds them to it. orders_check_transition fires only on UPDATE OF
-- fulfillment_status and orders_freeze_money looks only at money and the shipping
-- snapshot, so an UPDATE touching just these two columns trips neither and the
-- history can be moved to any instant after placed_at.
-- payments_settled_is_history and refunds_settled_is_history hold the same line
-- for their tables.
--
-- Only a change to an already-set timestamp is refused. Setting one for the
-- first time is the transition doing its job, and clearing it is not possible —
-- a cancelled order cannot leave 'cancelled'.
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
    -- A BLANK reason is legal. 消保法 §19 I lets a customer rescind a 通訊交易
    -- inside seven days 無須說明理由, and §19 V voids any agreement otherwise —
    -- so a NOT-NULL-and-non-blank CHECK was a barrier in front of an unwaivable
    -- right, enforced in the one place a form cannot talk its way past. The
    -- column stays NOT NULL: '' is "none given", which is a different fact from
    -- NULL and the only one this table needs.
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
    -- nothing arrived" are different facts, and a shop chasing a customer for a
    -- parcel needs to tell them apart.
    received_quantity  integer,
    restocked_quantity integer,
    -- Why the two differ, in the staff member's own words. It renders on
    -- /admin/returns and nowhere a customer reads, so it is the same kind of
    -- column as orders.staff_note.
    inspection_note    text,
    PRIMARY KEY (return_request_id, order_line_id),
    CONSTRAINT return_request_lines_quantity_positive CHECK (quantity > 0),
    -- No more back than was asked for, and no more on the shelf than came back.
    -- A three-way disposition (sellable / open-box / defective) was the other
    -- design and is deliberately NOT here: "open box" only means anything if it
    -- becomes a variant that goes on sale at a different price, and modelling a
    -- state nothing can act on is the table-with-no-door this repository keeps
    -- finding. Either a unit is sellable again or it is not; the note says why.
    CONSTRAINT return_request_lines_received_bounded
        CHECK (received_quantity IS NULL
               OR (received_quantity >= 0 AND received_quantity <= quantity)),
    CONSTRAINT return_request_lines_restocked_bounded
        CHECK (restocked_quantity IS NULL
               OR (restocked_quantity >= 0 AND restocked_quantity <= received_quantity)),
    -- Inspected means BOTH figures, or the row says a quantity came back and
    -- refuses to say what happened to it.
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

-- You cannot return what was never sent to you.
--
-- The ceiling is the SHIPPED quantity, not the ordered one. Bounding by what was
-- ORDERED lets a customer open a return — and, since refunds pay out on approval,
-- be paid for — goods still sitting in the warehouse. Every dispatch writes
-- order_shipment_lines, so the sum of those is what has actually left.
--
-- An order with no shipment therefore has a ceiling of zero and admits no
-- return at all. That is the intended reading: nothing has been sent, so
-- nothing can come back. Cancelling an unshipped order is the other door, and
-- it is not this one.
--
-- The line's order is locked first so two requests cannot both pass a test the
-- other is about to invalidate.
CREATE FUNCTION return_lines_within_purchase() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    shipped integer;
    already integer;
BEGIN
    -- Two statements, because PostgreSQL refuses FOR UPDATE with GROUP BY.
    -- The lock comes first and on its own: without it two requests each read a
    -- shipped total the other is about to spend.
    PERFORM 1
    FROM order_lines ol
    JOIN orders o ON o.id = ol.order_id
    WHERE ol.id = NEW.order_line_id
    FOR UPDATE OF o;

    -- No such line at all, as opposed to a line that has shipped nothing. The
    -- foreign key already refuses this, so reaching here means the FK was
    -- dropped; failing loudly beats treating it as a zero ceiling.
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

-- The transition machine: requested → approved | rejected, approved →
-- completed. Every advanced state is reached only through this UPDATE (birth is
-- guarded to 'requested' below). There is deliberately no branch for leaving
-- 'rejected': it is terminal, so a "recount on revival" branch could never fire,
-- and this file keeps no guard that cannot.
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

    -- Completed means the parcel was opened and every line accounted for. It is
    -- the same shape as orders_funded_to_leave_pending: a status that claims the
    -- work is finished, guarded by the fact that would make it true.
    --
    -- Without it 'completed' is a label somebody clicks, and the goods behind it
    -- are in a state nobody recorded — which for a RETURN is the whole question,
    -- because the units either went back on the shelf or did not and the ledger
    -- is what says which.
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
-- warranties, which one row per order LINE cannot say.
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
-- TWO tables, because they are two things. What the customer asked for is a
-- preference and can be edited; what was issued to the tax authority is a
-- document and cannot.
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
    -- NULL, and a NULL CHECK passes — so a company invoice with no 統編 gets in.
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
    -- that can never fire is one this file does not keep.
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
    -- another card. Only succeeded and cancelled end it, and there is
    -- deliberately no `failed` — a value the transition guard below would treat
    -- as terminal, which makes a recoverable decline unrecoverable.
    CONSTRAINT payments_status_known
        CHECK (status IN ('requires_payment', 'requires_action', 'processing',
                          'succeeded', 'cancelled')),
    CONSTRAINT payments_intended_positive CHECK (intended_amount_cents > 0),
    -- Same ceiling as every other money column (order_lines, refunds, …).
    -- Without it a capture can approach 2^63 and overflow the running sums the
    -- refund and store-credit guards compute; the input is bounded instead.
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
    -- Written as two exhaustive branches rather than as a comparison of two
    -- booleans, which lets a failed payment carry a captured amount:
    -- false = (NULL IS NOT NULL AND 50 IS NOT NULL) is false = false, and passes.
    -- The refund guard reads captured_amount without reading status, so that row
    -- is refundable — real money out against a payment that never took any in.
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

    -- "Has delivery details" means a LIVE, filled-in row, not merely a row.
    -- order_private_data_all_or_erased permits an all-NULL erased shape, and a
    -- live row could still carry blank strings; paying against either would ship
    -- an order with nowhere to send it.
    --
    -- The DESTINATION is an either/or, and writing it as "has a street" is what
    -- made every 超商取貨 order unpayable the day the second destination
    -- shipped: the customer reached Stripe, paid, and the capture threw. The
    -- contact fields are required either way; the destination half mirrors
    -- order_private_data_one_destination, in non-blank form because a CHECK on
    -- IS NOT NULL cannot see a row of spaces.
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
    --
    -- The checks above ask about lines, delivery details and a non-negative total
    -- — everything about whether the order is COMPLETE — and nothing about whether
    -- it is still live, while capture_payment checks only the payment's own
    -- status. Without this, the ordinary two-tab sequence goes through: start a
    -- payment, cancel the order in the other tab, then pay at Stripe. The capture
    -- succeeds against an order whose status is 'cancelled' and whose stock has
    -- already gone back on the shelf — money taken for goods the shop has re-sold,
    -- and a record that contradicts itself in three places at once.
    --
    -- A credit-funded order is caught incidentally, because cancelling reverses
    -- the credit and that moves order_amount_owed into the capture check below. An
    -- ordinary card order is caught by nothing else at all, which is the reverse of
    -- the usual shape here: the harder path guarded and the simple one not.
    --
    -- The order row is already held FOR UPDATE above, so a cancel racing a capture
    -- serialises on it: one of them goes second and sees what the first did.
    IF o.fulfillment_status = 'cancelled' THEN
        RAISE EXCEPTION 'order % was cancelled and cannot be paid', o.order_number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'payments_refuse_cancelled_order';
    END IF;

    -- The capture must equal what the order is actually owed: its total, less any
    -- store credit spent on it. Without this an NT$1 capture marks an NT$33,980
    -- order paid, and an overpay is just as wrong — the whole intended/captured
    -- split is pointless if captured need not match the order. Store credit spent
    -- at checkout is a negative store_credit_entries row carrying the order_id,
    -- and order_amount_owed is the ONE place that arithmetic lives: this guard,
    -- orders_funded_to_leave_pending and the payment page all read it. A new
    -- funding source is added THERE, never re-derived at a fourth call site.
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
    -- — the state a NOT NULL column cannot represent, which would force the row
    -- to be written only after the money had already moved.
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

    -- Both halves matter. Reading captured alone accepts a failed payment that
    -- carries an amount — the shape payments_succeeded_is_captured refuses at the
    -- table, and this guard does not rest on that being the only way in.
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

-- Fires on every UPDATE, not only on the columns the sum reads: moving a refund
-- to another payment changes neither amount nor status, so a trigger bound to
-- those columns cannot see it.
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
    -- There is deliberately no provider_created_at column. The event's own
    -- timestamp is in the payload if reconciliation ever needs it, and a column
    -- for it is a speculative snapshot of a fact the payload already carries.
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
    -- Lower is sooner. Transactional mail is 0; a bulk send is 100.
    --
    -- Without this, one newsletter to ten thousand subscribers sits in front of
    -- every receipt, dispatch notice and password reset written after it — the
    -- queue drains in available_at order, and a customer waiting an hour for the
    -- link back into their own account is a support ticket the shop caused by
    -- sending an email to somebody else.
    --
    -- A number rather than a boolean because "urgent or not" is the distinction
    -- somebody wants a third value for the moment they add anything, and because
    -- the ORDER BY reads the same either way.
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
-- finds the next urgent message with an index scan rather than a sort.
CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (priority, available_at)
    WHERE delivered_at IS NULL;

-- The retention sweep's range. Without it the daily delete is a sequential scan
-- over every message goen has ever sent, which is the table it exists to bound.
CREATE INDEX outbox_messages_delivered_at_idx
    ON outbox_messages (delivered_at)
    WHERE delivered_at IS NOT NULL;

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
    -- The English hero, each field separately optional.
    --
    -- pages.DefaultHero — the copy an untouched install renders — is translated,
    -- because it is compiled into the binary. Without these columns a SCHEDULED
    -- slide is not, so the moment a shop uses the feature the largest thing on its
    -- home page goes Chinese for every visitor. That is the same mistake a
    -- hard-coded nav list makes from the other direction: the default treated as
    -- chrome and the data as content, when both are read by the same person.
    --
    -- The HREFs are not translated. A link goes to one page.
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

-- 會員等級. A tier is what a customer has SPENT, not a column somebody sets.
--
-- Derived and never stored, for the reason store credit is a ledger and points
-- expiry is applied on read: a stored tier is a number that drifts from the
-- orders behind it the moment one is refunded, and nobody notices until a
-- customer asks why they lost a benefit they were told they had.
--
-- The window is a ROLLING one — spend in the last N days — rather than
-- lifetime. Lifetime tiers only ever go up, which turns a benefit into a
-- permanent liability the shop cannot price; a rolling window is what every
-- membership scheme that survives actually does.
CREATE TABLE membership_tiers (
    id                   uuid PRIMARY KEY DEFAULT uuidv7(),
    code                 text NOT NULL,
    name                 text NOT NULL,
    -- 銀卡會員 → Silver. The account page reads this INSIDE a sentence — "NT$10,000
    -- more reaches 銀卡會員" — which is the worst shape a missing translation takes:
    -- half an English sentence, so it reads as a bug rather than as untranslated
    -- content.
    name_en              text,
    -- The spend at or above which a customer is in this tier, over
    -- MembershipWindow days of committed orders.
    min_spend_cents      bigint NOT NULL,
    -- What a point is worth here, in basis points of the base rate: 10000 is
    -- one point per NT$100, 15000 is one and a half.
    --
    -- A MULTIPLIER on points rather than a discount at checkout, deliberately.
    -- A percentage off would stack with coupons and the free-shipping
    -- threshold, and every stacking order is a different total — the argument
    -- goen would then have to have in three places. Points already have a
    -- ledger, an expiry and a never-negative guard.
    points_multiplier_bp integer NOT NULL DEFAULT 10000,
    position             integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT membership_tiers_code_format CHECK (code ~ '^[a-z0-9]+(_[a-z0-9]+)*$'),
    CONSTRAINT membership_tiers_name_present CHECK (name ~ '[^[:space:]]'),
    CONSTRAINT membership_tiers_name_en_present
        CHECK (name_en IS NULL OR name_en ~ '[^[:space:]]'),
    CONSTRAINT membership_tiers_min_spend_non_negative CHECK (min_spend_cents >= 0),
    -- At least the base rate: a tier that earned FEWER points than no tier at
    -- all would be a punishment for spending more.
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
    -- The English strip. It sits above the header on every storefront page, which
    -- makes it the first thing a visitor reads, and shop-typed chrome with no twin
    -- is chrome in one language for everybody. The HREF has none; the CODE has
    -- none either, because a coupon code is typed into a box and matched exactly.
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

-- 限時優惠. The campaign is a collection with a deadline; the reduction itself
-- is the variant's price against its compare-at price. The trigger below is
-- what stops the two disagreeing on the storefront.
CREATE TABLE sale_campaigns (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    slug       text NOT NULL,
    title      text NOT NULL,
    -- /s/{slug} is a page whose whole heading is this title, and /deals lists them.
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

-- A product in 限時優惠 that shows no saving is a promise the page cannot
-- keep, so membership requires at least one variant actually marked down.
CREATE FUNCTION sale_campaign_products_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    -- Both halves of "a featured product keeps a discount" meet on the product
    -- row. Without this lock the two guards miss each other exactly the way a
    -- single unlocked guard misses itself: this transaction joins a campaign and
    -- reads a discount that a concurrent transaction is in the middle of
    -- clearing, while sale_campaign_variant_still_valid looks for a membership
    -- this transaction has not committed yet. Both pass, and the product ends up
    -- featured with nothing marked down.
    --
    -- Taking it here does not create a cycle: this guard locks the product and
    -- then only READS variants, while the variant-side guard locks the variant
    -- first and the product second. Nothing waits on a lock the other holds.
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

-- Membership is checked when a product joins, but a later edit that clears the
-- last discounted variant would leave a featured product with no saving. When
-- a variant loses its compare-at price or goes inactive, refuse it if that
-- product is in any campaign and nothing else is discounted.
-- AFTER, not BEFORE, and DELETE as well as UPDATE. A BEFORE-row form has two
-- ways through:
--
--   (a) `UPDATE product_variants SET compare_at_price_cents = NULL
--        WHERE product_id = X` clears every discount in one statement. A
--        BEFORE-row trigger sees the pre-statement snapshot of its siblings, so
--        each row in turn reads "another variant is still discounted" and all of
--        them pass — the same trap the categories cycle guard has to avoid.
--   (b) DELETE of the last discounted variant fires nothing at all when the
--        trigger is bound to UPDATE OF only.
--
-- AFTER-row triggers are queued and fired once the statement has finished, so
-- the check reads the true final state and (a) closes. The product row is locked
-- because two concurrent statements would otherwise each clear a different
-- variant, each read the other's discount as still present, and both commit.
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

-- ============================================================================
-- Content and messages
-- ============================================================================

CREATE TABLE faq_entries (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    category   text NOT NULL,
    question   text NOT NULL,
    answer     text NOT NULL,
    -- The English FAQ, each field separately optional. The same rule product copy
    -- follows: goen never invents a translation, and a shop that HAS one can say so.
    -- An entry with no English renders its Chinese, and /faq is the one page the
    -- footer already warns an English visitor about.
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

-- A row here is a SUBSCRIPTION, present or past. There is no unconfirmed
-- subscriber, because a request that has not been answered lives in
-- newsletter_confirmations instead — so a send query that forgets to ask
-- whether an address was ever confirmed still cannot reach one. The alternative
-- (a confirmed_at column that every future query must remember to test) is the
-- shape of hole this schema has already been caught by once.
CREATE TABLE newsletter_subscribers (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    email              text NOT NULL,
    -- When its owner said yes. NOT NULL because the row means exactly that.
    confirmed_at       timestamptz NOT NULL DEFAULT now(),
    -- NULL while they are on the list. The row survives an opt-out rather than
    -- being deleted: "this address asked not to be emailed" is the fact that
    -- has to outlive the subscription, and a deleted row says nothing.
    unsubscribed_at    timestamptz,
    -- The token in the unsubscribe link, stored AS THE TOKEN and not as a digest.
    --
    -- It does not expire: an email sent a year ago still has to be able to take
    -- somebody off the list, and telling them to sign in to a shop they may have
    -- no account at is not an answer.
    --
    -- Storing the token rather than its hash is deliberate. The reflex is to hash
    -- a secret in a table — but the threat that shapes this one is a script on the
    -- open internet walking ids, not a reader of the database: `store` holds
    -- SELECT and UPDATE here, because unsubscribing IS an update, so anybody who
    -- could read a hash could already set unsubscribed_at directly. A hash would
    -- defend nothing this table is not already open to.
    --
    -- And it would cost the whole feature. With only the digest kept, the token
    -- exists once — in the welcome email — and the SEND cannot reproduce it. A
    -- newsletter could then carry no unsubscribe link, which is the one place the
    -- link has to be, and a subscriber who lost that first email would have no way
    -- off the list but writing to support.
    unsubscribe_token  text NOT NULL,
    -- The language they were reading when they confirmed. Kept for the same
    -- reason orders.locale and stock_notifications.locale are: an issue is sent
    -- by a back-office click, where the subscriber is not present.
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

-- The link in an email is looked up by token, and two rows sharing one would
-- make which subscriber it unsubscribes a matter of plan order.
CREATE UNIQUE INDEX newsletter_subscribers_unsubscribe_token_key
    ON newsletter_subscribers (unsubscribe_token);

-- The back office reads the list newest first. There is no partial index over the
-- ACTIVE subscribers beside it: an index nothing uses is a claim about coverage
-- this schema has already been caught making once
-- (contact_messages_unhandled_idx).
CREATE INDEX newsletter_subscribers_confirmed_at_idx
    ON newsletter_subscribers (confirmed_at DESC);

CREATE TRIGGER newsletter_subscribers_set_updated_at
    BEFORE UPDATE ON newsletter_subscribers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- An address somebody typed into the footer form, waiting for its OWNER to say
-- so. Anybody can type anybody's address there, which is the whole reason this
-- table is separate from the one above.
CREATE TABLE newsletter_confirmations (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    email      text NOT NULL,
    -- sha256 of the token in the confirmation link. Unlike the unsubscribe
    -- token this one is spent and expires: it is the thing that decides whether
    -- an address joins the list at all.
    digest     bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT newsletter_confirmations_email_present CHECK (email ~ '[^[:space:]]'),
    CONSTRAINT newsletter_confirmations_email_trimmed CHECK (email !~ '^[[:space:]]|[[:space:]]$'),
    CONSTRAINT newsletter_confirmations_digest_sha256 CHECK (octet_length(digest) = 32),
    CONSTRAINT newsletter_confirmations_expires_after_created CHECK (expires_at > created_at)
);

-- One outstanding request per address. A second submission REPLACES the first
-- rather than adding a second live link, so a mailbox that has been asked three
-- times holds one key and not three.
CREATE UNIQUE INDEX newsletter_confirmations_email_key
    ON newsletter_confirmations (lower(email));
CREATE UNIQUE INDEX newsletter_confirmations_digest_key
    ON newsletter_confirmations (digest);

-- One issue of the newsletter, and the record that it went out.
--
-- Append-only in practice: sent_at is stamped once and an issue that has been
-- sent is history. Editing the subject of something ten thousand people have
-- already read would make the row disagree with every copy of it.
CREATE TABLE newsletter_issues (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    subject    text NOT NULL,
    body       text NOT NULL,
    -- NULL until it is sent. A draft is an issue nobody has received, and the
    -- send is what makes it real — so this column is also the freeze: the
    -- trigger below refuses an edit once it is set.
    sent_at    timestamptz,
    -- How many messages the send enqueued, counted from the rows it actually
    -- claimed rather than from the subscriber count read beforehand. A figure
    -- taken before the write is a figure that can disagree with what was sent.
    recipients integer NOT NULL DEFAULT 0,
    -- Who sent it. NULL once that account is erased; the issue survives, because
    -- it is a record of what the shop published.
    sent_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT newsletter_issues_subject_present CHECK (subject ~ '[^[:space:]]'),
    CONSTRAINT newsletter_issues_body_present CHECK (body ~ '[^[:space:]]'),
    CONSTRAINT newsletter_issues_recipients_non_negative CHECK (recipients >= 0),
    -- An issue nobody received has no send to have counted.
    CONSTRAINT newsletter_issues_unsent_has_no_recipients
        CHECK (sent_at IS NOT NULL OR recipients = 0)
);

CREATE INDEX newsletter_issues_created_at_idx ON newsletter_issues (created_at DESC);
-- Every foreign key is indexed, so an ON DELETE SET NULL of a staff account does
-- not scan the issues.
CREATE INDEX newsletter_issues_sent_by_idx ON newsletter_issues (sent_by);

CREATE TRIGGER newsletter_issues_set_updated_at
    BEFORE UPDATE ON newsletter_issues
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A sent issue is frozen.
--
-- Ten thousand copies of it are in ten thousand mailboxes. Rewriting the subject
-- afterwards makes the shop's record of what it published disagree with what
-- people actually read, and there is no way to correct the copies.
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

-- ============================================================================
-- Privileges
--
-- Applied last, once every table and function exists. store gets ordinary
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
DECLARE
    addr text;
BEGIN
    -- Read the address before the account goes: the newsletter keys on the
    -- ADDRESS and not on the user, so nothing below can find it afterwards.
    SELECT email INTO addr FROM users WHERE id = p_user_id;

    -- The LAST ADMIN cannot erase themselves, and this is the only place the
    -- question can be asked.
    --
    -- /account/erase checks that the person typed their own address and then
    -- calls this function; it never consults guardLastAdmin, because that guard
    -- lives in the staff feature and erasure is an account feature. So the last
    -- admin could erase their own account and lock the shop out of its own back
    -- office permanently — the exact outcome ErrLastAdmin exists to prevent,
    -- through a door that never asked. There is no SQL recovery short of
    -- promoting somebody by hand.
    --
    -- It belongs here rather than in the handler for the reason every other rule
    -- in this schema does: this function is documented as the ONLY door that
    -- removes a person, so a second door added later would have to remember, and
    -- the one that forgot would be whichever was written next.
    IF (SELECT role FROM users WHERE id = p_user_id) = 'admin'
       AND (SELECT count(*) FROM users WHERE role = 'admin') <= 1 THEN
        RAISE EXCEPTION 'the last admin cannot be erased'
            USING CONSTRAINT = 'erase_user_keeps_one_admin';
    END IF;

    -- Blank every delivery field on this user's orders and stamp erased_at. The
    -- order_private_data_all_or_erased CHECK permits exactly this all-NULL
    -- state, so the order survives as a financial record with no PII.
    UPDATE order_private_data pd SET
        email = NULL, recipient_name = NULL, phone = NULL, postal_code = NULL,
        city = NULL, district = NULL, street = NULL,
        pickup_brand = NULL, pickup_store_code = NULL, pickup_store_name = NULL,
        erased_at = now()
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

    -- The invoice PREFERENCE, which survived erasure entirely. carrier_code is a
    -- 手機條碼載具 — a personal identifier — and tax_id identifies a company, and
    -- both sat here keyed by order after the account and the delivery PII were
    -- gone. It cannot be nulled in place: invoice_preferences_mobile_has_carrier
    -- and invoice_preferences_company_has_tax_id require the value for their
    -- invoice type. The row is dropped instead, which loses nothing financial —
    -- it says how to issue an invoice, while invoice_documents IS the issued
    -- tax record and stays untouched.
    DELETE FROM invoice_preferences ip
    USING orders o
    WHERE ip.order_id = o.id AND o.user_id = p_user_id;

    -- The newsletter, which keys on the address rather than the account — so a
    -- plain DELETE of a user never reaches it, and it is the one table in the
    -- schema that would go on emailing somebody who asked to be forgotten.
    --
    -- The pending confirmation goes too, and that is the half easy to miss: a
    -- link already sitting in the mailbox would otherwise let the erased address
    -- rejoin the list after the erasure, which is the erasure undone by a click.
    IF addr IS NOT NULL THEN
        DELETE FROM newsletter_subscribers WHERE lower(email) = lower(addr);
        DELETE FROM newsletter_confirmations WHERE lower(email) = lower(addr);
        -- contact_messages is the SECOND address-keyed table, and it is the one
        -- the reasoning above is easiest to apply to the newsletter and not to:
        -- it has no user_id, so a DELETE of a user never reaches it and no
        -- foreign key says it exists. It holds a name, an address, a subject and
        -- whatever the customer typed — which is routinely a delivery address and
        -- a phone number, because "my order has not arrived" is what people write
        -- in about — and /admin/messages reads it. The erasure probe is derived
        -- from the catalog rather than from a four-entry list for exactly that
        -- reason.
        DELETE FROM contact_messages WHERE lower(email) = lower(addr);
    END IF;

    -- Every browser's proof of access to this person's orders. The grants key on
    -- the ORDER rather than the user, so nothing above reaches them, and the FK
    -- is ON DELETE RESTRICT so they must go explicitly.
    --
    -- A grant is a live bearer credential: a token still sitting in a browser —
    -- or in a proxy log — opens the order page, the cancel form and the return
    -- form. Leaving them behind is the same mistake as leaving a pending
    -- newsletter confirmation in a mailbox, which this function already refuses
    -- to make one paragraph up.
    DELETE FROM order_access_grants g
    USING orders o
    WHERE g.order_id = o.id AND o.user_id = p_user_id;

    -- The account itself. Its foreign keys carry the rest: orders.user_id and
    -- the ledgers' actor_user_id go to NULL (forbid_change permits that one
    -- nulling), auth rows cascade.
    DELETE FROM users WHERE id = p_user_id;
END;
$$;

-- ============================================================================
-- Privileges
--
-- Applied last, once every table and function exists. store gets ordinary
-- read/write, then the privileged tables have their direct-write privileges
-- revoked so the only way in is the SECURITY DEFINER functions below.
-- ============================================================================

GRANT USAGE ON SCHEMA public TO store, reporting;

GRANT SELECT ON ALL TABLES IN SCHEMA public TO store, reporting;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO store;

-- reporting is for reading BUSINESS data (reports, dashboards), not for
-- reading everything. Take back SELECT on the tables that hold credentials and
-- personal data: a report role has no business seeing a session token, a TOTP
-- secret, a reset-token hash, an OAuth identity, a customer's delivery details,
-- or a raw payment webhook payload. store keeps them — the application needs
-- them — but the read-only role does not.
-- A credential table added after this list is written is swept into `reporting`
-- by the blanket GRANT SELECT above and stays there silently, because a list in
-- prose asks nothing — order_access_grants, holding bearer-token digests, is one
-- that arrived months later. TestReportingCannotReadCredentialsOrPII is what
-- asks, per named table, so the next one is covered by the row it adds.
REVOKE SELECT ON
    sessions, password_reset_tokens, staff_totp_credentials, user_identities,
    order_private_data, payment_webhook_events, order_access_grants,
    email_verifications, newsletter_confirmations,
    -- The eight the guard reports, which is the point: a list above maintained by
    -- hand, with nothing running against it, drifts exactly this far.
    --
    -- outbox_messages is the one that matters most and the least obvious.
    -- internal/email/notify.go concedes it: a reset link, an unsubscribe link
    -- and a newsletter confirmation all travel in the PAYLOAD, in plaintext,
    -- because sending from the handler loses the message when the process dies
    -- mid-send. So the queue holds live credentials, and the read-only role
    -- could read every one of them.
    --
    -- The rest are contact details and identity: users, addresses and
    -- contact_messages are somebody's name, address and own words;
    -- newsletter_subscribers and stock_notifications are mailing lists;
    -- invoice_preferences carries a 統編; carts carries a token_hash.
    --
    -- A report that genuinely needs one of these gets a VIEW exposing the
    -- aggregate, not the table. reporting is the role most likely to be pointed
    -- at a BI tool, a notebook, or a contractor.
    users, addresses, carts, contact_messages, invoice_preferences,
    newsletter_subscribers, outbox_messages, stock_notifications
    FROM reporting;

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
--     DML revoked. record_audit_event and post_store_credit are their doors, and
--     the only ones: a ledger with a second write path is a ledger whose guards
--     are optional.
--   payments / refunds — money; written through the payment posting functions,
--     which run as owner and not as store. INSERT is revoked with UPDATE/DELETE,
--     or store could write a born-succeeded capture with no provider behind it.
--   product_variants — stock_quantity is function-owned, and the back office
--     reaches the rest of the row through a column-level grant that omits it
--     (see the admin section far below), so the column takes its DEFAULT 0 at
--     birth and record_inventory_movement stays its one writer.
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
    FROM store;
REVOKE INSERT, UPDATE, DELETE ON product_variants FROM store;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM store;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM store;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM store;
-- UPDATE, because a direct one could repoint a whole balance at another user
-- with no ledger guard noticing. DELETE, because deleting the account is
-- deleting the balance — store_credit_entries references it ON DELETE RESTRICT
-- so the foreign key would refuse an account with history, but an account whose
-- entries have all been reversed to zero would go, and with it the trail that
-- says they were.
--
-- INSERT stays: the account is created on first use, and refusing to make one
-- would refuse a customer their first points.
REVOKE UPDATE, DELETE ON store_credit_accounts FROM store;
-- payment_webhook_events is the at-least-once delivery dedupe ledger AND the raw
-- record of what the provider sent. Deleting a row makes a resent event look
-- new and be processed twice; rewriting the payload rewrites the evidence. The
-- app only needs to stamp when it processed one, so keep INSERT and grant UPDATE
-- on processed_at alone; take the rest away.
-- A coupon is merchandising, and the storefront does not do merchandising: it
-- may READ one to apply it and nothing else. Without the revoke, a bug in a
-- handler could raise its own discount or lift its own expiry.
--
-- coupon_redemptions is a ledger, so it goes through a function for the reason
-- every other ledger does — the row and the order's discount_cents must agree,
-- and two writes cannot be made to agree by asking nicely.
REVOKE INSERT, UPDATE, DELETE ON coupons, coupon_redemptions FROM store;
REVOKE UPDATE, DELETE ON payment_webhook_events FROM store;
GRANT UPDATE (processed_at) ON payment_webhook_events TO store;
-- order_events and shipping_method_versions are append-only too (forbid_change),
-- and had only DELETE revoked below — leaving the trigger as their sole guard.
-- Revoke UPDATE so the privilege layer backs it. INSERT stays: the app appends
-- an order event, and a new shipping version is an insert.
REVOKE UPDATE ON order_events, shipping_method_versions FROM store;
-- A user must be erased through erase_user(), which also blanks the delivery PII
-- on their orders and drops their restock emails. A direct DELETE would leave
-- both behind (order_private_data keys on the order, stock_notifications.email
-- is NOT NULL), so take DELETE away and leave that door as the only one.
REVOKE DELETE ON users FROM store;

-- A newsletter row is a SUPPRESSION record as much as a subscription: once an
-- address has said "stop", that is the fact which must survive, and deleting the
-- row is how a list quietly starts emailing somebody again. Unsubscribing is an
-- UPDATE, which stays. Erasure is the one thing that removes it, and it goes
-- through erase_user for the same reason users does.
REVOKE DELETE ON newsletter_subscribers FROM store;
-- A storefront request has no business publishing a newsletter. The customer
-- side of goen writes to newsletter_subscribers and reads nothing else here.
REVOKE INSERT, UPDATE, DELETE ON newsletter_issues FROM store;
REVOKE DELETE, TRUNCATE ON
    orders, order_lines, order_private_data, order_shipments,
    order_shipment_lines, order_events, invoice_documents, invoice_preferences,
    return_requests, return_request_lines, warranty_registrations,
    payments, refunds, shipping_method_versions
    FROM store;


-- The posting functions run as their owner, so they can write what store
-- cannot. EXECUTE is what store is granted instead of direct DML.
-- record_inventory_movement is made SECURITY DEFINER here (the other three were
-- created that way); next_order_number joins them so the counter it increments
-- can be write-revoked from store above.
ALTER FUNCTION record_inventory_movement(uuid, integer, text, text, text, uuid, uuid) SECURITY DEFINER;
ALTER FUNCTION next_order_number() SECURITY DEFINER;

-- What "committed" means, as a SET.
--
-- ONE definition, and the reason it is a view rather than only a function is
-- measured. A per-row function call over 14,000 orders costs 106 ms because the
-- planner must evaluate two EXISTS subqueries per row and cannot turn them into
-- a join; the same question asked set-wise is a merge join at 7.7 ms. Every
-- report aggregates over order history, so that difference is the difference
-- between a page and a wait.
--
-- (Writing the function as LANGUAGE sql so PostgreSQL can inline it does NOT
-- close that gap — measured at 105 ms, unchanged. Inlining does not help inside
-- an aggregate expression, where there is no WHERE clause for a semi-join to
-- become.)
--
-- An order is committed when money or goods have moved: a succeeded payment, or
-- a fulfilment status past pending. The second half is what covers a fully
-- store-credited order, which has no payment row at all — a guard reading
-- "EXISTS a succeeded payment" as committed skips such an order silently, and
-- CLAUDE.md records three that did.
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
--
-- One view for both questions makes a CANCELLED order committed, because
-- `fulfillment_status <> pending` is true of it. That is right for the freeze
-- guards — nobody may rewrite a cancelled order's totals — and wrong for
-- everything else, in a way that costs real stock: release_reservation refuses a
-- committed order's hold and the sweeper skips one, so the units behind a
-- cancelled order can never come back to the shelf. By ANY path. They are gone
-- until somebody notices.
--
-- It is wrong in three other places at the same time. A cancelled order counts as
-- revenue and as a best seller, it earns its customer the 已購買 badge on a
-- product they never kept, and it feeds 買了又買.
--
-- So: committed means the shop is doing the work, settled means the record is
-- closed. Cancelled is settled and not committed.
CREATE VIEW settled_orders AS
    SELECT id FROM committed_orders
    UNION
    SELECT id FROM orders WHERE fulfillment_status = 'cancelled';

COMMENT ON VIEW settled_orders IS
    'Orders whose money and lines may no longer change: committed, or cancelled.';

-- Granted EXPLICITLY, and this is the whole reason the grant is here rather
-- than left to the sweeping GRANT ... ON ALL TABLES earlier in the file: that
-- statement already ran, so a view created below it starts with no privileges
-- at all.
--
-- What its absence costs: order_is_committed() is SECURITY INVOKER and reads this
-- view, and order_lines' trigger calls it — so EVERY CHECKOUT dies with
-- "permission denied for view committed_orders". Nothing in the test suite
-- reaches that, because the integration tests connect as the owner.
-- make check-layout does, by placing a real order through the site's own form.
GRANT SELECT ON committed_orders, settled_orders TO store, reporting;

-- The row-at-a-time form, for triggers and guards.
--
-- It READS THE VIEW rather than restating the predicate, so there is one
-- definition and not two that can drift. CLAUDE.md is explicit that a fourth
-- call site must not re-derive this test — that applies to the view and the
-- function equally, which is why one is written in terms of the other.
-- What an order still owes: its total, less the store credit spent on it.
--
-- ONE definition, for the reason committed_orders and store_credit_balances are one.
-- Written out separately in orders_check_transition and in
-- payments_capture_matches_order, with the PAYMENT path carrying no copy at all, is
-- how the gross total goes to Stripe while the capture guard demands the net: an
-- order part-funded by store credit is charged in full at Stripe and then refused by
-- this database forever, so the money leaves the customer and the order never goes
-- paid.
--
-- Credit is NET OF REVERSALS. Each spend is a negative entry carrying the order_id,
-- paired with its reversal through reverses_id, so a spend that was given back
-- contributes nothing — summing only `amount_cents < 0` counts the ghost of a
-- reversed spend and lets an unfunded order ship.
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

-- What one return request is worth paying back.
--
-- ONE definition, read by the queue and by the decision page. Computing it as
-- `sum(quantity * unit_price_cents)` in each of them instead is wrong in both
-- directions at once:
--
--   * It ignores `orders.discount_cents` while `payments_capture_matches_order`
--     forces the capture to equal `order_amount_owed`, which is NET of the
--     discount. Two NT$500 lines, a NT$500 coupon and NT$80 of shipping capture
--     NT$580; returning ONE line then claims NT$500 for an item the customer paid
--     NT$250 of, and `refunds_within_capture` is satisfied because the total
--     still fits. Returning BOTH claims NT$1,000 against NT$580 of headroom, so
--     the decision is refused outright — goods back at the shop and no door that
--     can pay for them. Invisible on any order with no coupon, because shipping
--     and tax are additive and the claim can never exceed the capture.
--
--   * It never refunds `orders.shipping_cents`. /returns states the statutory
--     rescission under 消保法 §19 I, where the customer bears 任何費用 — no cost
--     at all — so the delivery fee goen collected has to come back with the
--     goods, and nothing else can return it short of a hand-granted store credit.
--
-- The discount is allocated PROPORTIONALLY to what is going back, and rounded
-- UP, so several partial returns can never sum past the capture and strand the
-- last one. A full return is exact: the returned gross equals the subtotal, so
-- the share is the whole discount and no rounding happens.
--
-- The shipping fee goes back only when this request takes the LAST unreturned
-- unit of every line — the customer is rescinding the whole contract rather
-- than sending one thing back, and the shop delivered the rest. Where several
-- partial returns add up to the whole, the one that completes it carries the
-- fee.
--
-- `tax_cents` is deliberately absent. It is written 0 at order creation because
-- 營業稅法 §32 II requires a displayed price to be tax-INCLUSIVE, so there is no
-- separate tax to give back; a term for it here would be dead arithmetic.
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

-- The freeze guards' question, in the same one-definition-in-one-place form.
CREATE FUNCTION order_is_settled(p_order_id uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM settled_orders WHERE id = p_order_id);
$$;

-- What a customer has spent on orders that went through, in a rolling window.
--
-- committed_orders and not "every order": a cancelled order is not spending,
-- and neither is one sitting unpaid in somebody''s browser. The view already
-- answers that question once, so this reads it rather than restating it.
-- p_exclude_order is the order being paid for RIGHT NOW, when there is one.
--
-- The capture marks the payment succeeded before it awards points, both inside
-- one transaction, so by the time the multiplier is read the order is already
-- committed and counts towards the tier it is about to be paid at. That means
-- an order straddling a threshold earns at the NEW rate — a benefit nobody
-- promised, on the order that created it, at a rate the customer had not been
-- shown. Excluding it makes the rate the one their account page said it was.
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

-- The tier that spend earns: the highest band at or below it, or NULL for a
-- customer below every threshold.
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
-- Not a posting function: the freeze triggers run SECURITY INVOKER, so they call
-- this as store and an explicit call checks EXECUTE (firing a trigger does not).
-- It only reads what store may already read.
GRANT EXECUTE ON FUNCTION order_is_committed(uuid) TO store;
-- The payment page reads what an order owes, so store needs this or every
-- /orders/{number}/pay is a 500 in production and nowhere else — every test
-- connects as the owner, who is subject to no missing grant. Trap #20.
GRANT EXECUTE ON FUNCTION order_amount_owed(uuid) TO store;
GRANT EXECUTE ON FUNCTION order_is_settled(uuid) TO store;
GRANT EXECUTE ON FUNCTION member_spend(uuid, integer, uuid) TO store;
GRANT EXECUTE ON FUNCTION member_tier(uuid, integer, uuid) TO store;

-- ============================================================================
-- The back office
--
-- `admin` is what batch ⑦'s pages may do. It is a WIDER set than store, not a
-- superset of everything: the point of the model is that no connecting role can
-- write money or stock directly, and that holds for the back office too. An
-- admin adjusts stock through record_inventory_movement, exactly as the
-- storefront does, so every movement lands in the ledger with a reason.
--
-- The binary opens a SECOND pool for these pages and does SET ROLE admin on it.
-- One pool doing SET ROLE per request would leave the role set on a connection
-- returned to the pool, and the next storefront request would run as admin.
-- ============================================================================

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'admin') THEN
        CREATE ROLE admin NOLOGIN;
    END IF;
    -- The login role a deployment points the admin pool at. Same reasoning as
    -- store_svc: NOSUPERUSER, owns nothing, and its password is set by
    -- operations rather than by a migration.
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'admin_svc') THEN
        CREATE ROLE admin_svc LOGIN NOSUPERUSER IN ROLE admin;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO admin;

-- committed_orders, granted HERE and not beside the view, because the view is
-- created before this role exists. Ordering in this file is load-bearing twice
-- over: the sweeping GRANT ... ON ALL TABLES ran even earlier, so a view
-- created after it starts with no privileges at all.
--
-- LOAD-BEARING, and it reads as redundant — "admin is a member of store, so
-- removing it leaves every test green". It is not.
--
-- admin is NOT a member of store. `pg_auth_members` holds exactly four
-- application edges — admin_svc→admin, store_svc→store, maintenance_svc→
-- maintenance, goen→goen_app — and no admin→store among them. Nothing inherits
-- anything between the three application roles; each is granted what it holds.
--
-- Reading it as redundant is how somebody deletes a GRANT the back office's every
-- report depends on, on the strength of a membership that does not exist. It is
-- the same shape as a note claiming a projection table has one writer, or that
-- another admin performs a recovery no code performs: a claim of enforcement with
-- nothing behind it.
--
-- The independence is also what makes the auth-surface revokes below WORK: a
-- column granted to store would otherwise reach admin through membership, and
-- taking password_hash away from admin would be impossible without taking it
-- from the storefront that has to write it.
GRANT SELECT ON committed_orders, settled_orders TO admin;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO admin;
GRANT INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO admin;

-- Everything store is barred from, admin is barred from too, with one
-- exception: product_variants, because maintaining the catalogue IS the back
-- office's job and sale_campaign_variant_still_valid is the guard that makes it
-- safe. stock_quantity is NOT among what it may set — see the column revoke
-- below.
REVOKE INSERT, UPDATE, DELETE ON
    inventory_movements, inventory_reservations, audit_events, store_credit_entries
    FROM admin;
REVOKE INSERT, UPDATE, DELETE ON payments, refunds FROM admin;
REVOKE INSERT, UPDATE, DELETE ON order_number_counters FROM admin;
REVOKE UPDATE, DELETE, TRUNCATE ON invoice_document_lines FROM admin;
REVOKE UPDATE, DELETE ON store_credit_accounts FROM admin;
-- The back office issues coupons. It still may not write a redemption: that
-- row is a fact about an order's money, and it is posted by the same function
-- the storefront uses so the two cannot diverge.
REVOKE INSERT, UPDATE, DELETE ON coupon_redemptions FROM admin;
REVOKE UPDATE, DELETE ON payment_webhook_events FROM admin;
REVOKE UPDATE ON order_events, shipping_method_versions FROM admin;
REVOKE DELETE ON users FROM admin;
-- The back office does not prove somebody's address for them. Verification is the
-- customer answering a letter, and a staff member who could write this table could
-- mark any address proved without anybody reading anything.
REVOKE INSERT, UPDATE, DELETE ON email_verifications FROM admin;

-- ============================================================================
-- The back office may not become a customer.
--
-- Without the revokes below, a probe runs both of these as `admin`, and there is
-- no feature behind either verb:
--
--     UPDATE users SET password_hash = 'x' WHERE …;                  -- UPDATE 1
--     INSERT INTO sessions (token_hash, user_id, expires_at) …;      -- INSERT 1
--
-- That is silent, complete impersonation. `admin` holds no INSERT on
-- audit_events and record_audit_event is the only door, so a staff member who
-- minted a session for a customer, read their addresses and order history, and
-- placed or cancelled an order as them would leave NOTHING behind. The back
-- office has no impersonation feature; that is capability with no caller.
--
-- The argument is the one email_verifications is already given three lines up,
-- and it applies with more force here: a staff member who can write
-- password_hash can take the account outright, and one who can INSERT a session
-- does not even need to change the password to do it.
--
-- What the back office genuinely writes on users is a ROLE and a NAME, both from
-- /admin/staff — a colleague is created with no password on purpose, and sets
-- their own through /forgot. So the column lists are exactly that, and both
-- verbs take one: revoking UPDATE alone would leave admin able to INSERT a user
-- row that already carries a password_hash it chose.
REVOKE INSERT, UPDATE ON users FROM admin;
GRANT INSERT (email, full_name, role) ON users TO admin;
GRANT UPDATE (role, full_name) ON users TO admin;

-- sessions: the back office ENDS them — revoking a colleague's access has to
-- take their open sessions with it — and stamps totp_verified_at when a second
-- factor is proved. It never creates one. Creating a session is signing
-- somebody in, and the only thing entitled to do that is the sign-in form,
-- which runs as store.
REVOKE INSERT, UPDATE ON sessions FROM admin;
GRANT UPDATE (totp_verified_at) ON sessions TO admin;
-- The shop cannot put an address on its own mailing list, or take one off it.
-- That is what double opt-in MEANS, and leaving it to a convention is how a
-- back office grows an "add subscriber" form: only the owner of a mailbox can
-- answer for it. The back office reads the list — SELECT stays.
REVOKE INSERT, UPDATE, DELETE ON newsletter_subscribers, newsletter_confirmations
    FROM admin;
-- The back office composes and sends. It does not DELETE: a sent issue is what
-- the shop published, and the mailboxes holding it cannot be edited either.
REVOKE DELETE ON newsletter_issues FROM admin;
REVOKE DELETE, TRUNCATE ON
    orders, order_lines, order_private_data, order_shipments,
    order_shipment_lines, order_events, invoice_documents, invoice_preferences,
    return_requests, return_request_lines, warranty_registrations,
    payments, refunds, shipping_method_versions
    FROM admin;

-- Stock is the one column an admin may not set by hand, on the table it may
-- otherwise edit. A direct UPDATE would move stock with no movement row behind
-- it, and the ledger — which is what an audit reads — would disagree with the
-- shelf. record_inventory_movement is the only door, and it writes both.
--
-- A column-level REVOKE does NOT cut into a table-level grant: PostgreSQL reads
-- table-level UPDATE as permission on every column, and the narrower revoke is
-- silently ignored. The table-level grant has to go first, and the columns an
-- admin may set are then listed explicitly — which also means a column added
-- later is barred until someone decides it belongs here.
-- The back office creates and edits variants, but never sets stock directly.
--
-- BOTH verbs need the column list, and the INSERT one is the easier to forget:
-- revoking UPDATE alone leaves `admin` able to INSERT a variant carrying
-- stock_quantity = 999, which is stock conjured with no inventory_movements row
-- behind it — the shelf and the ledger silently disagreeing from birth. It is
-- the same hole the `store` revokes above close, and adding a role while copying
-- half the pattern is how it reopens.
--
-- Omitting stock_quantity from the INSERT list is what makes the column take
-- its DEFAULT 0. Stock then has exactly one door for every role:
-- record_inventory_movement.
REVOKE INSERT, UPDATE ON product_variants FROM admin;
-- BOTH verbs carry the column list, and stock_quantity is in neither. The
-- parcel measurements join them: they are the back office's to state, and they
-- decide which shipping methods a customer is offered.
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
-- admin ONLY, and store is deliberately not given it: dispatching is the back
-- office's act. store takes holds at checkout and releases them on a
-- cancellation, and neither of those settles part of one.
GRANT EXECUTE ON FUNCTION consume_reservation_partial(uuid, integer) TO admin;
GRANT EXECUTE ON FUNCTION release_reservation(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_is_committed(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_amount_owed(uuid) TO admin;
-- The refund figure, for the queue and the decision page. admin only: deciding a
-- return is the back office's act, and the customer's own return pages never
-- state an amount.
GRANT EXECUTE ON FUNCTION return_refundable_amount(uuid) TO admin;
GRANT EXECUTE ON FUNCTION order_is_settled(uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_spend(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION member_tier(uuid, integer, uuid) TO admin;
GRANT EXECUTE ON FUNCTION erase_user(uuid) TO admin;

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
-- store holds no INSERT on payments, so a handler cannot write one. These are
-- the door, and they are SECURITY DEFINER for the same reason the inventory
-- functions are: the boundary is what makes "money has one writer" true rather
-- than aspirational.
--
-- The split is deliberate. Opening an intent is a bookkeeping entry for
-- something that has not happened. Recording a capture is the moment money
-- exists, and it is the one payments_capture_matches_order and
-- payments_succeeded_is_captured both police — so it takes the amount the
-- PROVIDER reports, and lets those guards refuse it if it disagrees with what
-- the order is owed.
-- ============================================================================


-- Open a payment intent against an order, or return the one already open.
--
-- Idempotent on (order, provider_ref): Stripe's own idempotency means a retried
-- create returns the same intent, and this must not then make a second row.
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

-- Record that the provider captured money.
--
-- The amount comes from the PROVIDER, not from the application's own idea of
-- the total: if the two disagree, payments_capture_matches_order refuses the
-- write and the disagreement surfaces as a failed webhook rather than as an
-- order marked paid for the wrong sum.
--
-- Idempotent: a webhook is at-least-once, so the same capture arrives more than
-- once and the second must change nothing. An already-succeeded payment returns
-- without touching the row — which also means payments_settled_is_history never
-- has to refuse a duplicate.
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

-- Record that the provider gave up on an intent.
-- name matches the schema's own vocabulary: 'cancelled', not 'failed', because
-- payments_status_known does not admit the latter.
CREATE FUNCTION cancel_payment(p_provider_ref text) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
    UPDATE payments SET status = 'cancelled'
    WHERE provider_ref = p_provider_ref AND status <> 'succeeded';
END;
$$;

-- Post a store-credit entry.
--
-- store holds no INSERT on store_credit_entries either, and for the same
-- reason: the ledger is the balance. store_credit_never_negative takes the
-- account row FOR UPDATE before it reads, so two concurrent spends cannot each
-- pass a test the other is about to invalidate.
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
    -- customers never have store credit, and a row per account that will always
    -- be empty is a row to keep correct for nothing.
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

-- Give back credit that was spent on an order nobody is going to ship.
--
-- This is the door onto store_credit_entries.reverses_id, the unique index on it
-- and the reversal branch of store_credit_guard — all of which are written with
-- the ledger and reachable through nothing else. Without it, cancelling an order
-- releases its stock and leaves the customer's credit spent: the goods go back on
-- the shelf, and their money does not go back to them.
--
-- One function rather than a reverses_id parameter on post_store_credit,
-- because the two are different acts under different rules. A posting grants or
-- spends; a reversal UNDOES one specific entry, and store_credit_guard holds it
-- to negating exactly that entry on exactly that account. A nullable parameter
-- on the posting function would let a caller pass a reversal by accident.
--
-- Idempotent per entry: the key is derived from the entry being reversed, so a
-- retried cancellation reverses once. Every spend on the order is reversed,
-- because an order can have been part-funded more than once.
--
-- SECURITY DEFINER for the reason every posting function is: store_credit_entries
-- is revoked from store and admin alike, and this is the only way in.
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
        -- Already reversed: the unique partial index on reverses_id would refuse
        -- the insert anyway, and skipping keeps the returned total honest about
        -- what THIS call gave back.
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

-- localized_name, declared beside `categories` and granted here for the reason
-- CLAUDE.md's trap #20 records from both sides: the privilege sweep at the end of
-- this file revokes EXECUTE from PUBLIC on every function, so an ungranted one is
-- every storefront page returning 500 — and only in production, because every test
-- connects as the owner, who is subject to no missing grant.
--
-- reporting as well as store and admin: a dashboard reading a category name reads
-- it through the same door.
GRANT EXECUTE ON FUNCTION localized_name(text, text, text) TO store, admin, reporting;


-- Refund posting. `refunds` is revoked from both store and admin for the same
-- reason `payments` is: a row written directly could claim money moved that
-- never did, and refunds_within_capture only guards the amount.
--
-- Two functions, not one, because the provider call sits between them. The row
-- is committed BEFORE Stripe is asked, so a crash between the request and the
-- response leaves something reconciliation can find — that is what request_key
-- is for. Writing it afterwards means a refund that succeeded at Stripe and
-- exists nowhere in goen.
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
-- store does NOT settle refunds, and there is deliberately no GRANT here.
--
-- The argument for one is "store settles refunds because the Stripe webhook
-- arrives on the storefront pool, not the back office one" — true about routing,
-- and false about this webhook. internal/payment handles
-- checkout.session.completed and checkout.session.expired; nothing in it touches
-- a refund, and SettleRefund's only Go caller is internal/admin on the admin
-- pool. Such a grant has NO CALLER.
--
-- It would not be a free no-op either. `store` is explicitly revoked INSERT,
-- UPDATE and DELETE on refunds, and this is a SECURITY DEFINER function — so the
-- grant is a live path around that revoke from the customer-facing role, and one
-- that can move a refund to 'failed' or 'cancelled'. Those two drop out of
-- refunds_guard's sum, which frees the same capture's allowance to be claimed
-- again: a way to refund a payment twice, reachable from the role that serves
-- anonymous product pages.
--
-- If the refund webhook is built — charge.refund.updated is the event — the grant
-- belongs IN THAT CHANGE, so the grant and its caller land together and can be
-- checked against each other.
--
-- The write-direction guard cannot see this one: it asks about TABLE privileges,
-- and an EXECUTE grant on a definer function is exactly the door that exists to
-- bypass those. Noted here because it is the shape to look for next.


-- Redeem a coupon against an order.
--
-- SECURITY DEFINER because neither role may write coupon_redemptions directly.
-- The amount is passed in rather than recomputed here: the handler already
-- priced the order to set discount_cents, and computing it twice in two places
-- is how the two come to disagree. coupon_redemption_matches_order is what
-- holds them to each other.
--
-- The coupon row is locked FIRST, before the counts are read. Without it two
-- concurrent checkouts each read "9 of 10 used" and each write the tenth and
-- eleventh — the classic oversell, in a coupon.
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

    -- CANCELLED orders do not count, and that is the whole door.
    --
    -- coupon_redemptions is append-only and both roles are revoked all three
    -- write verbs, so a redemption cannot be deleted by anybody — including the
    -- owner. With the count unconditional, a checkout cancelled two minutes
    -- afterwards consumes a total-limit slot and a per-customer slot FOREVER,
    -- with no path in the product to free either and nothing the back office can
    -- do but switch the coupon off: max_redemptions is write-once.
    --
    -- The row stays, because it is the history of what was charged. It is the
    -- QUESTION that has to be right, which is the committed_orders lesson exactly
    -- — one predicate answering two things, and the shop's own cancel unable to
    -- undo what it has just caused.
    --
    -- A PENDING unpaid order still counts, deliberately. It is a checkout in
    -- flight, and not counting it is how two customers both pass the last slot.
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

    -- The per-customer limit binds on accounts. A guest has no account to
    -- count against, so for them the total cap is the only bound — stated here
    -- rather than silently skipped.
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
-- ---------------------------------------------------------------------------
-- 會員點數
--
-- A ledger, exactly like store_credit_entries, and for the same reason: a
-- balance column is a number two concurrent writers each read and each
-- overwrite, and no test finds it until money is missing. The balance is
-- derived, and the guard that keeps it non-negative locks the account first.
--
-- Points are NOT money and are deliberately not stored as money. They convert
-- to store credit at a fixed, published rate — [PointsPerCredit] in
-- internal/loyalty — which is the one place the exchange exists. A points
-- balance that could be spent directly would be a second currency with its own
-- rounding, its own refund rules and its own guards, all of which store credit
-- already has.
--
-- Expiry is per ENTRY rather than per account. "Points earned in March expire
-- in March next year" is what a customer is told and what a shop can honour;
-- "your whole balance expires" is a rule that punishes the customer who keeps
-- shopping, which is the opposite of the point.
-- ---------------------------------------------------------------------------

CREATE TABLE loyalty_entries (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    -- At the ACCOUNT, not the user, so erasure leaves the ledger balanced —
    -- the same reasoning store_credit_entries records.
    account_id      uuid NOT NULL REFERENCES store_credit_accounts (id) ON DELETE RESTRICT,
    points          bigint NOT NULL,
    reason          text NOT NULL,
    -- The caller's name for this posting. A retried award submits the same key
    -- and gets a unique violation instead of a second credit.
    idempotency_key text NOT NULL,
    order_id        uuid REFERENCES orders (id) ON DELETE RESTRICT,
    -- NULL for a spend, which never expires because it has already happened.
    -- An award without an expiry is a liability that grows forever.
    expires_on      date,
    created_at      timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT loyalty_entries_points_nonzero CHECK (points <> 0),
    CONSTRAINT loyalty_entries_reason_present CHECK (reason ~ '[^[:space:]]'),
    CONSTRAINT loyalty_entries_key_present CHECK (idempotency_key ~ '[^[:space:]]'),
    -- A spend cannot expire and an award must. Without this an award with no
    -- expiry sits in the balance forever and a spend with one silently
    -- disappears from it, which reads to the customer as points being taken
    -- back.
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
--
-- A VIEW rather than a column, for the reason the ledger is a ledger. And
-- expiry is applied HERE rather than by a job that writes expiry rows — a job
-- that has not run yet would leave expired points spendable, and a customer
-- spending points the shop believes are gone is the failure this must not have.
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

-- Points may not go negative.
--
-- The account row is locked first here too, and this lock is DEFENCE IN DEPTH
-- rather than the one doing the work: every posting path goes through
-- redeem_loyalty_points, which takes the same lock before it inserts. Removing
-- this one leaves every test green, and that is recorded rather than dressed
-- up — it is here for the posting function somebody writes next and forgets to
-- lock in, and no test can prove a defence against code that does not exist.
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

-- AFTER, so the balance it reads includes the row being checked. A BEFORE
-- trigger would test the balance without the entry that might overdraw it.
CREATE CONSTRAINT TRIGGER loyalty_never_negative
    AFTER INSERT ON loyalty_entries
    FOR EACH ROW EXECUTE FUNCTION loyalty_never_negative();

GRANT SELECT ON loyalty_entries, loyalty_balances TO store, admin, reporting;
-- Posting is through the function below and nowhere else, the same way every
-- other ledger in this schema works.
REVOKE INSERT, UPDATE, DELETE ON loyalty_entries FROM store, admin;

-- ---------------------------------------------------------------------------
-- Store credit balances
-- ---------------------------------------------------------------------------
--
-- ONE definition of "what this account is worth", for the reason loyalty_balances
-- and visible_reviews are each one: the balance is summed from the ledger, and the
-- account page, the checkout and the back office each writing that sum out is
-- three chances for one of them to add a FILTER the others do not have — and a
-- customer told two different balances by two pages of one shop cannot tell which
-- is true.
--
-- It carries user_id as well as account_id because every caller asks by customer.
-- A caller that needs "one row per user even with no account" wraps it in a
-- scalar subquery with coalesce; the view itself has a row only where an account
-- does, which is the honest shape.
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
-- view created after it is granted to nobody. That is trap #20 in CLAUDE.md: a
-- view added without this line is a production 500 on every page that reads it,
-- and green in every test, because the tests connect as the owner.
GRANT SELECT ON store_credit_balances TO store, admin, reporting;

-- ---------------------------------------------------------------------------
-- Co-purchase projection — 買了又買
--
-- A PROJECTION and not a per-request aggregation, and that is a measured
-- position rather than a preference.
--
-- Computed per request, "what did people who bought X also buy" costs 3 ms for
-- a product nobody buys and 136 ms for the one everybody does — because the
-- work is proportional to that product's ORDER HISTORY, which only grows.
-- Measuring the cheap product says the query is fine; measuring the popular one
-- says the opposite, and only the second is the page anybody loads. The expensive
-- part is order_is_committed(), correctly called per candidate row: 14,963
-- PL/pgSQL invocations for one page view.
--
-- Recommendations are also the read model where staleness costs nothing. An
-- hour-old answer to "what goes with this" is the same answer; an hour-old
-- stock count is an oversell. That asymmetry is what makes a projection right
-- here and wrong for the things goen computes live.
-- ---------------------------------------------------------------------------

CREATE TABLE product_copurchases (
    product_id       uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    other_product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    -- How many committed orders contained both.
    orders           integer NOT NULL,
    computed_at      timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (product_id, other_product_id),
    CONSTRAINT product_copurchases_orders_positive CHECK (orders > 0),
    -- A product is not bought together with itself. Without this the pair
    -- (X, X) would rank first on every page, since X appears in every order
    -- containing X.
    CONSTRAINT product_copurchases_not_self CHECK (product_id <> other_product_id)
);

COMMENT ON TABLE product_copurchases IS
    'Derived: how many committed orders contained both products. Rebuilt in '
    'full by refresh_copurchases(); never written by a request.';

-- The read is "this product's neighbours, best first", which is one index.
CREATE INDEX product_copurchases_rank_idx
    ON product_copurchases (product_id, orders DESC);

-- The other side of the pair needs one too, and not for a query goen writes:
-- deleting a product must check every row referencing it, and an unindexed
-- foreign key turns that into a full scan of this table.
CREATE INDEX product_copurchases_other_idx
    ON product_copurchases (other_product_id);

-- Rebuild the whole projection.
--
-- A full rebuild rather than an incremental update, because the input is every
-- committed order and an incremental version would need to know which orders
-- changed state since the last run — a second piece of bookkeeping that can
-- drift from the thing it describes. At goen's scale the rebuild is one query;
-- when it stops being one, the fix is to bound it by date, not to make it
-- incremental.
--
-- DELETE and re-INSERT inside one transaction, so a reader never sees a half
-- built projection. TRUNCATE would take an ACCESS EXCLUSIVE lock and block
-- every PDP for the duration.
CREATE FUNCTION refresh_copurchases() RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    written integer;
BEGIN
    -- The committed set, read from the view rather than resolved per candidate
    -- row. Same reason the reports join it: a per-row function call cannot
    -- become a join, and a join can.
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

-- The role a BACKGROUND JOB runs as.
--
-- It answers one question — what may a scheduled rebuild do — and the answer is
-- almost nothing: read the catalogue, and call refresh_copurchases. It exists
-- because the alternative was granting that function to `store`, and a
-- projection a request can rebuild is a projection a request can be made to
-- rebuild 584 ms at a time.
--
-- A separate POOL, not SET ROLE on a borrowed connection: setting a role on a
-- request's connection leaves it set when the connection goes back, and the
-- next storefront request runs with it. Same reasoning as admin_svc.
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
-- Only the owner rebuilds it: a projection a request could rewrite is a
-- projection a request can be made to rewrite.
REVOKE INSERT, UPDATE, DELETE ON product_copurchases FROM store, admin;

-- The one thing maintenance may do. refresh_copurchases is SECURITY DEFINER, so
-- it writes as the owner — which is why WHO may call it is the whole control.
GRANT EXECUTE ON FUNCTION refresh_copurchases() TO maintenance;

-- ---------------------------------------------------------------------------
-- Media
--
-- Images live in PostgreSQL, not on a disk and not in object storage.
--
-- That is a deliberate choice with a real cost, so it is written down rather
-- than discovered. goen's architecture is one Go binary and one PostgreSQL, and
-- it is deployed as a container built by ko. A filesystem needs a volume the
-- deployment does not have; object storage needs credentials, a client library,
-- and a module graph goen has already refused once for the same reason (adding
-- ko as a tool took the graph from 104 modules to 528, almost all of it cloud
-- SDKs). Bytes in the database keep "one binary plus PostgreSQL" true, make a
-- backup one thing, and let an image and the row that references it commit
-- together.
--
-- What this is NOT: how you serve images at scale. A real catalogue puts them
-- behind a CDN. The seam where that changes is this table plus one handler, and
-- content addressing is what makes the move safe — a URL never changes meaning,
-- so anything already cached stays correct.
--
-- Content-addressed: the primary key is the sha256 of the BYTES AS STORED,
-- after goen re-encoded them. Two uploads of the same picture are one row, and
-- a URL is immutable by construction — which is what lets the handler answer
-- with a one-year Cache-Control and never think about invalidation.
-- ---------------------------------------------------------------------------

CREATE TABLE media_objects (
    -- The lowercase hex sha256 of `bytes`. Not a uuid: the identity of an image
    -- IS its content, and a random id would let the same picture exist twice
    -- under two URLs that a cache must then hold twice.
    digest       text PRIMARY KEY,
    -- The IANA type goen will serve it as. Not the type the client claimed —
    -- that is attacker-controlled — but the one goen chose when it re-encoded.
    content_type text NOT NULL,
    -- The image itself. bytea, not a large object: it is read whole, it is
    -- bounded well under PostgreSQL's 1GB field limit, and TOAST already stores
    -- it out of line without any of lo's separate lifecycle.
    bytes        bytea NOT NULL,
    width        integer NOT NULL,
    height       integer NOT NULL,
    byte_size    integer NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),

    -- Only what goen can decode AND re-encode. The list is short on purpose:
    -- every format here is one whose bytes goen produced itself.
    CONSTRAINT media_objects_type_supported
        CHECK (content_type IN ('image/jpeg', 'image/png')),
    -- A zero-dimension image is a decode that went wrong. The ceiling is what
    -- stops a decompression bomb being stored after it survived decoding.
    CONSTRAINT media_objects_dimensions_sane
        CHECK (width BETWEEN 1 AND 8000 AND height BETWEEN 1 AND 8000),
    CONSTRAINT media_objects_size_positive CHECK (byte_size > 0),
    -- byte_size must describe the bytes it sits beside, or a listing page shows
    -- a number that is not the truth about what a visitor will download.
    CONSTRAINT media_objects_size_matches CHECK (byte_size = octet_length(bytes)),
    -- 64 lowercase hex characters, and nothing else. This value reaches a URL
    -- path, so its shape is the reason the handler needs no escaping.
    CONSTRAINT media_objects_digest_format CHECK (digest ~ '^[0-9a-f]{64}$')
);

COMMENT ON TABLE media_objects IS
    'Uploaded images, content-addressed by the sha256 of the re-encoded bytes.';

-- Newest first, for the back office's picker.
CREATE INDEX media_objects_created_at_idx ON media_objects (created_at DESC);

-- An image is never edited: a change produces different bytes and therefore a
-- different row. Deleting one IS allowed — that is how an unreferenced upload
-- is reclaimed — so this forbids UPDATE only.
CREATE TRIGGER media_objects_immutable
    BEFORE UPDATE ON media_objects
    FOR EACH ROW EXECUTE FUNCTION forbid_change('media_objects_immutable');

-- The back office uploads and the storefront reads. Neither may rewrite one.
GRANT SELECT ON media_objects TO store;
GRANT SELECT, INSERT, DELETE ON media_objects TO admin;
GRANT SELECT ON media_objects TO reporting;

-- ---------------------------------------------------------------------------
-- The audit trail
--
-- goen already records what happened to an ENTITY: order_events for an order's
-- timeline, inventory_movements for every unit of stock, store_credit_entries
-- for the ledger, payment_webhook_events for what Stripe said. What none of
-- them records is WHO — and "show me everything this staff member did last
-- Tuesday" is a question that spans entities, so no per-entity history can
-- answer it.
--
-- Written through a function rather than by INSERT, because `admin` has INSERT
-- revoked on audit_events and that revoke is worth keeping: with it, the one
-- door is here, where occurred_at is the database's clock and the actor must be
-- a real user. Without it, any query in the back office is a place a forged row
-- could be written.
--
-- The row is written in the CALLER's transaction. That is the whole point: an
-- audit row for work that rolled back is a lie, and work that commits without
-- one is a gap. Neither is possible when they are the same commit.
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
    -- An audit row with no actor is a row that answers nothing. The back office
    -- is behind RequireStaff, so an absent actor is a wiring mistake rather than
    -- a state a request can reach, and it should stop the write it belongs to.
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

-- ---------------------------------------------------------------------------
-- Posting points
--
-- The only door into loyalty_entries, for the reason every other ledger has
-- one: `store` and `admin` hold no INSERT, so a bug in a query cannot write a
-- balance, and the guards live where there is no second path around them.
-- ---------------------------------------------------------------------------

-- Award the points a committed order earned.
--
-- Idempotent through loyalty_entries_idempotency_key: the caller names the
-- posting after the order, so a retry — or a webhook Stripe sent twice — is one
-- award. It returns how many points were written, which is zero for a repeat.
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

    -- The account, created on first use exactly as store credit does it: a
    -- customer who has never held either has no row, and refusing to award
    -- points because of that would punish the first purchase.
    SELECT id INTO v_account FROM store_credit_accounts
    WHERE user_id = (SELECT user_id FROM orders WHERE id = p_order_id);

    IF v_account IS NULL THEN
        INSERT INTO store_credit_accounts (user_id)
        SELECT user_id FROM orders WHERE id = p_order_id AND user_id IS NOT NULL
        ON CONFLICT (user_id) DO UPDATE SET user_id = excluded.user_id
        RETURNING id INTO v_account;
    END IF;

    -- A guest order has no account to credit, and that is not an error: guest
    -- checkout is supported and points are a membership benefit.
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

-- Spend points, and post the store credit they bought, in ONE transaction.
--
-- Both or neither. Two statements would let the points go and the credit not
-- arrive — the customer pays and receives nothing, which is the worst available
-- failure and the only one this must not have.
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

    -- The account is locked HERE, before anything is inserted.
    --
    -- Not only in the trigger. An INSERT takes a FOR KEY SHARE lock on the
    -- referenced account row to enforce the foreign key, and the AFTER trigger
    -- then asks for FOR UPDATE on the same row — an upgrade. Several
    -- transactions each holding KEY SHARE and each waiting for UPDATE is a
    -- deadlock, and PostgreSQL resolves it by killing all but one.
    --
    -- That LOOKS like the guard working: exactly one redemption survives. It is
    -- not. A deadlock is a coin toss that happens to leave one winner, the
    -- losers get 40P01 instead of a rule they can act on, and under a different
    -- interleaving it can kill the transaction that would have succeeded.
    -- Taking the strongest lock first means the second writer WAITS and then
    -- meets the guard.
    PERFORM 1 FROM store_credit_accounts WHERE id = p_account_id FOR UPDATE;

    -- The spend. loyalty_never_negative locks the account and refuses an
    -- overdraw, so the balance is never read here — reading it would be reading
    -- it without the lock.
    INSERT INTO loyalty_entries (account_id, points, reason, idempotency_key)
    VALUES (p_account_id, -p_points, 'redeem', p_key);

    -- The credit, in the same transaction. store_credit_entries has its own
    -- idempotency key, prefixed so a redemption and an award of the same id
    -- cannot collide.
    INSERT INTO store_credit_entries (account_id, amount_cents, reason, idempotency_key)
    VALUES (p_account_id, p_cents, 'points', 'redeem:' || p_key);

    RETURN p_cents;
END;
$$;

GRANT EXECUTE ON FUNCTION award_loyalty_points(uuid, bigint, date) TO store, admin;
GRANT EXECUTE ON FUNCTION redeem_loyalty_points(uuid, bigint, bigint, text) TO store, admin;

-- The privilege sweep. THIS MUST BE THE LAST THING IN THE FILE.
--
-- It pins search_path on every stored function and takes EXECUTE away from
-- PUBLIC. Both are swept rather than written per function, because the failure
-- mode is omission: a function added later simply would not be covered.
--
-- Anywhere but last, every function appended below it is PUBLIC EXECUTE for
-- exactly that reason — with the four payment posting functions below it,
-- `reporting`, a read-only role, can call capture_payment and post money.
-- TestNoStoredFunctionIsPublicExecute is what asks. Keeping the sweep last is
-- what makes the next append safe by construction rather than by memory.
--
-- Order is not a problem for the GRANTs above: REVOKE ... FROM PUBLIC does not
-- touch a privilege granted to a named role.
-- ============================================================================
-- The AUTHENTICATION and MERCHANDISING surfaces.
--
-- Placed HERE, after every table in the schema exists, and that placement is the
-- point rather than an accident of editing: up with the other store revokes, this
-- block names media_objects, which is created 1,000 lines below them, and the
-- migration fails outright — trap #21 for the fourth time, from yet another side.
-- A REVOKE, like a GRANT, cannot name what does not exist yet, and the only
-- placement that is safe by construction is after everything.
-- ============================================================================

-- ============================================================================
-- The AUTHENTICATION surface, which store held in full and needs almost none of.
--
-- Without these revokes, `store` escalates to admin in three statements — a
-- third-party probe ran exactly this:
--
--     UPDATE users SET role = 'admin' WHERE id = <attacker>;   -- UPDATE 1
--     DELETE FROM staff_totp_credentials WHERE user_id = <target>;
--     -- sign in
--
-- Nothing stands in the way of it. The money and stock tables are locked down
-- properly; the auth tables are the ones a hand-written list of
-- money/stock/ledger tables never enumerates, which is what every privilege test
-- in this repository would otherwise be.
-- TestNoRoleHoldsAWriteItsQueriesNeverMake derives the question instead, and it
-- is what these revokes are held to.
--
-- users: the storefront registers an account, changes a password, stamps a
-- login, edits a profile and confirms an address. It never sets a ROLE. A
-- column list is the only way to say that — `role` is one column out of many and
-- PostgreSQL has no "every column except" — which is exactly the treatment
-- product_variants.stock_quantity already gets, and TestNoRoleCanWriteStockDirectly
-- already proves the shape works.
--
-- INSERT takes the same list, and that half is the easier to forget: without it
-- `store` cannot UPDATE a role and can simply INSERT a row that already has one.
-- role then takes its DEFAULT 'customer', which is what a registration means.
REVOKE INSERT, UPDATE ON users FROM store;
-- email_verified_at is INSERTABLE as well as updatable, for the identity path:
-- an account created from a provider that has already proved the address is
-- born verified, and making the customer prove it again to a shop they have
-- just proved it to is ceremony. `role` stays out of both lists, which is what
-- keeps a storefront request from creating an admin.
GRANT INSERT (email, password_hash, full_name, phone, email_verified_at) ON users TO store;
GRANT UPDATE (password_hash, last_login_at, full_name, phone, email,
              email_verified_at) ON users TO store;

-- staff_totp_credentials is a password-equivalent guarding /admin, and the
-- storefront role has no business touching it in any direction. With all four
-- verbs, deleting a colleague's second factor — the whole recovery ceremony
-- /admin/staff performs under two guards — is one statement from a
-- customer-facing connection.
--
-- Nothing on the storefront pool reads or writes it either: the twofactor store
-- runs on the ADMIN pool, which is where every route it serves lives. SELECT goes
-- too, because a sealed secret plus its user id is half of an offline attack and
-- no storefront query asks for one.
REVOKE ALL ON staff_totp_credentials FROM store;

-- sessions: the storefront creates one at sign-in and deletes them at sign-out,
-- on password change and on the sweep. It never UPDATES one — the only update
-- in the schema is totp_verified_at, which is the back office proving a factor.
--
-- INSERT is narrowed to the columns sign-in actually supplies, and that is not
-- tidiness. A table-level INSERT includes totp_verified_at, so `store` could
-- create a session BORN step-up verified — and the back office's whole gate is
-- "does this session carry a recent proof". A staff member's sign-in, or any bug
-- on the sign-in path, could mint a session that had already cleared the second
-- factor without a code ever being entered. The column guard is what asks this;
-- no amount of reading a table-level REVOKE list can see it.
REVOKE INSERT, UPDATE ON sessions FROM store;
GRANT INSERT (token_hash, user_id, user_agent, ip, expires_at) ON sessions TO store;

-- Merchandising is not something a storefront request does. It reads all of
-- this and writes none of it, and without the revoke a single bug reachable from
-- a product page could delete the catalogue, retitle a promotion, or publish a
-- shipping version at fee_cents = 0 and undercut every delivery charge the shop
-- makes. shipping_method_versions keeps its SELECT and loses INSERT: it is
-- append-only and /admin/shipping is the only thing that appends.
REVOKE INSERT, UPDATE, DELETE ON
    products, product_images, product_specs, product_options,
    product_option_values, variant_option_values, categories, brands,
    promo_banners, hero_slides, faq_entries, membership_tiers, sale_campaigns,
    sale_campaign_products, shipping_methods, shipping_zones,
    shipping_zone_prefixes, shipping_version_zones, media_objects
    FROM store;
REVOKE INSERT ON shipping_method_versions FROM store;

-- order_access_grants is what lets a browser see a guest's order: the sha256 of
-- a bearer token. store INSERTs one at checkout and at /orders/find, reads it to
-- answer "may this browser see this order", and DELETES the ones nobody can
-- present any more. Rewriting a digest is repointing somebody's access, and
-- nothing does that, so UPDATE goes.
--
-- DELETE stays, against the argument that "deleting one is locking a customer out
-- of their own order" — which sounds right and breaks the retention sweep. The
-- sweeper runs as `store`, so revoking DELETE refuses every daily pass,
-- `GrantRetain` never applies once, and the credential this table exists to
-- expire is kept forever: exactly the state the sweep exists to prevent, under a
-- comment claiming it is prevented.
--
-- media_objects gets the opposite answer three sections down, and the difference
-- is the point rather than an inconsistency. Deleting stored bytes is
-- irreversible, so that sweeper moved to the admin pool instead. The worst a bug
-- here can do is force a guest through /orders/find with their order number and
-- email — an inconvenience with a documented way back — so the role that
-- actually runs the sweep is allowed to run it.
REVOKE UPDATE ON order_access_grants FROM store;
-- created_at ALONE, because "nothing repoints a digest" is still true and is the
-- reason the table-level UPDATE is revoked above. What does need writing is the
-- retention CLOCK: the placed-order cookie is re-issued with a fresh MaxAge on
-- every order and carries the older tokens forward, so their grants have to
-- restart from that same event or the sweeper deletes a credential a live cookie
-- still presents — the lockout GrantRetain's own comment names as the state that
-- must never happen. A column list rather than the verb, so the guard that
-- matters cannot be widened by the fix to a different one.
GRANT UPDATE (created_at) ON order_access_grants TO store;

-- What each role holds and no query it runs exercises. Every line below is
-- produced by TestNoRoleHoldsAWriteItsQueriesNeverMake rather than by reading,
-- which is the point: a privilege nothing exercises accumulates silently as the
-- schema grows, and asking only the READ direction never finds one.
--
-- store: fulfilment is the back office's, and the 發票 tables are the back
-- office's too — a storefront request that could file a tax document is a
-- customer issuing their own invoice.
REVOKE INSERT, UPDATE, DELETE ON
    order_shipments, order_shipment_lines, invoice_documents,
    invoice_document_lines
    FROM store;

-- user_identities is the STOREFRONT's: signing in with Google writes it, and
-- that is a customer-facing act. INSERT and DELETE only — linking and unlinking
-- are the two things that happen to a link, and an UPDATE would repoint one
-- identity at a different account, which is silent account takeover with no row
-- to show for it. The two unique indexes are what make a link one-to-one; UPDATE
-- is what would let somebody move it.
REVOKE INSERT, UPDATE, DELETE ON user_identities FROM store;
GRANT INSERT, DELETE ON user_identities TO store;

-- admin: the storefront's own working tables — a cart, a wishlist, an address
-- book, a reset token, a browser's order-access grant — are none of the back
-- office's business, and holding write on them is how a back-office bug becomes
-- a customer-data incident. The back office READS what it needs of these;
-- SELECT is untouched.
--
-- password_reset_tokens is the sharpest of them: a staff member who could insert
-- one could mint a reset for any account, which is the same complete takeover
-- the users.password_hash revoke above closes, by a second door.
REVOKE INSERT, UPDATE, DELETE ON
    addresses, carts, cart_items, checkout_attempts, wishlist_items,
    password_reset_tokens, order_access_grants, order_lines,
    return_request_lines, warranty_registrations, invoice_preferences,
    invoice_documents, invoice_document_lines, user_identities
    FROM admin;
REVOKE INSERT ON payment_webhook_events FROM admin;

-- The 發票 tables, granted to admin because filing a document with the 加值中心
-- is the back office's act. `store` keeps neither: a storefront request that
-- could file a tax document is a customer issuing their own invoice, and every
-- rule about what may be issued lives in the back office.
--
-- INSERT and UPDATE, never DELETE. An issued 統一發票 is filed history — the
-- 財政部 platform has it, and voiding is how it stops being live —
-- which invoice_documents_guard already enforces from the other side by
-- refusing every UPDATE except the void.
GRANT INSERT, UPDATE ON invoice_documents TO admin;
-- Lines are written with their document and never touched again; the guard on
-- the parent is what makes a filing immutable, and a line that could be edited
-- afterwards would let an issued invoice say it sold something else.
GRANT INSERT ON invoice_document_lines TO admin;

-- ---------------------------------------------------------------------------
-- The DECISION columns, on the eight tables both roles legitimately write.
--
-- The argument against writing a block like this is that "a grant one column too
-- NARROW fails nowhere in this test suite — every suite connects as the OWNER,
-- who is subject to no missing grant. It would surface first in production, on a
-- write path a customer is standing in."
--
-- TestEveryRoleCanRunItsOwnQueries is what makes that false. SET ROLE binds ACLs
-- even for a superuser, and EXPLAIN (GENERIC_PLAN) plans a statement — resolving
-- every table, COLUMN and function privilege — without executing it or binding a
-- parameter. So a grant one column too narrow is a red test over all 425
-- (role, query) pairs, in about a second. **The check whose absence is the
-- argument against narrowing these grants is the check that makes narrowing them
-- safe.**
--
-- What each half is:
--
--   store must not write the SHOP'S decision about what a customer wrote —
--   hidden_at on the three moderated tables, a return's resolution, decided_at
--   and status, an order's staff_note and completed_at, a message's handled_at,
--   a restock's notified_at, a delivery record's erased_at.
--
--   admin must not write the CUSTOMER'S own words, or the money an order was
--   priced at — a review's rating and body, a question's body, a return's stated
--   reason, everything on a contact message except whether it has been handled,
--   and every figure on `orders` that the checkout computed.
--
-- Both directions have a live path. `store` holding product_reviews.hidden_at
-- means anything reachable from a product page can UN-hide the abusive review a
-- shop hid — and hiding is what takes a review out of the SCORE, so that is a
-- product's public rating moved by a storefront request. `admin` holding
-- contact_messages.message means the back office can rewrite what a customer
-- wrote to it, in the one table /admin/messages exists to read.
--
-- Every list is DERIVED: all of the table's columns, minus the ones
-- TestNoRoleHoldsAColumnWriteItsQueriesNeverMake reports that role's own
-- queries never write. Not one of them is hand-picked, which is the only reason
-- writing fourteen of these at once is a defensible thing to do.
--
-- Three properties of the lists that are decisions rather than transcription:
--
--  1. A column with a manufactured default — id, created_at, updated_at — STAYS
--     in the grant, for the reason columnExemptions gives: it carries no
--     authority a guard reads, and omitting it traps the next INSERT that names
--     it rather than bounding anything.
--  2. INSERT and UPDATE take the SAME list. The guard merges the two verbs into
--     one set per table, so a per-verb split is not derivable from it — and this
--     block is worth only as much as its derivation. `users` above does split
--     them, because that list is hand-authored from knowledge of four specific
--     statements; this one is not, and pretending otherwise would be exactly the
--     guess the argument above warns against.
--  3. `admin` on order_private_data and on stock_notifications is deliberately
--     ABSENT. The write-column parser cannot resolve those two sets, so the
--     guard skips them and reports nothing — and a list nobody derived is
--     exactly the hand-authored grant this block exists to avoid. They stay with
--     the table-level guard, and this is where the next person reads why.
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

-- The customer says WHAT they are sending back; the shop says what arrived. The
-- same split return_requests already draws between reason and resolution, one
-- level down — store writes the claim and never the inspection, so a storefront
-- request cannot declare its own parcel received and restocked.
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

-- The mirror: the shop records the inspection and never the claim. INSERT is
-- revoked outright rather than narrowed, because a return line is the
-- CUSTOMER's statement of what they are sending — a back office that could add
-- one could return goods on somebody's behalf and refund them for it.
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
-- Trigger guards read their tables by unqualified name. A plpgsql function with
-- no pinned search_path resolves those against the caller's path — and pg_temp
-- is searched FIRST for relations even when it is not listed, so store,
-- which may create temp tables, could plant an empty pg_temp.categories (or a
-- forged pg_temp.payments) and the guard would read the decoy and pass.
--
-- Listing pg_temp LAST is what fixes it: current_schemas then puts pg_temp after
-- public, so a real table always wins over a same-named temp one. (Omitting
-- pg_temp does NOT help — it is then searched implicitly first; verified.) Every
-- goen-authored function is pinned to (pg_catalog, public, pg_temp) and has its
-- EXECUTE revoked from PUBLIC so a SECURITY DEFINER posting function is not
-- callable by reporting. The pg_trgm extension's own functions are left
-- untouched. TEMP is revoked from store as a second, independent layer.
DO $$
DECLARE
    fn record;
BEGIN
    -- Every language, not just plpgsql. A LANGUAGE sql function resolves its
    -- unqualified relations exactly the same way, so filtering on plpgsql leaves
    -- a blind spot that opens the moment somebody writes a one-line SQL helper —
    -- localized_name is one, and it is covered here by asking about every
    -- function rather than about a language.
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

    -- store's TEMP privilege arrives via PUBLIC, so revoking it from PUBLIC
    -- is what takes it away. The owning superuser keeps it (superusers bypass);
    -- the storefront never needs a temp table.
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());

    -- No role writes through a VIEW, and this is trap #21 from a third side.
    --
    -- A simple view is AUTO-UPDATABLE, and a write through one is permission-
    -- checked against the base table as the VIEW'S OWNER — which is `goen`, who
    -- is subject to none of the REVOKEs above. So `DELETE FROM committed_orders`
    -- as `admin` is ACCEPTED, with no "permission denied", while `admin` holds no
    -- DELETE on `orders`. Without this loop the revokes read like a boundary and
    -- are not one.
    --
    -- It arises from ORDERING rather than intent: the sweeping grant to store runs
    -- before the views exist, which is exactly why store shows SELECT-only on
    -- them, and the grant to admin runs after and sweeps them up. Same trap as
    -- `committed_orders` being created after GRANT ... ON ALL TABLES, and as a
    -- GRANT naming a role the file has not created yet — the third face of one
    -- mistake, which is why the fix belongs HERE, at the end, where every object
    -- exists.
    --
    -- Derived from pg_views rather than named, so a view added afterwards is
    -- covered by existing. TestEveryDefinerWrittenTableIsRevoked cannot see this
    -- because it joins pg_tables; the write-direction guard asks pg_class and
    -- therefore does.
    --
    -- Every view here is a DEFINITION the application reads — committed_orders,
    -- settled_orders, visible_reviews, loyalty_balances, store_credit_balances —
    -- and each exists precisely so one rule is not restated in eleven queries.
    -- Writing through one would be writing to the base table with the rule
    -- bypassed, which is the opposite of why they exist.
    FOR fn IN SELECT format('%I.%I', schemaname, viewname) AS sig
              FROM pg_views WHERE schemaname = 'public'
    LOOP
        EXECUTE format(
            'REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON %s FROM store, admin, reporting',
            fn.sig);
    END LOOP;

    -- golang-migrate creates public.schema_migrations before this migration
    -- runs, so GRANT ... ON ALL TABLES above hands store write access to the
    -- migration bookkeeping — enough to forge a version or set dirty. Revoke it
    -- if the table is present (it is not when the schema is loaded directly).
    IF EXISTS (SELECT 1 FROM pg_class WHERE relname = 'schema_migrations'
               AND relnamespace = 'public'::regnamespace) THEN
        EXECUTE 'REVOKE ALL ON schema_migrations FROM store, reporting';
    END IF;
END
$$;
