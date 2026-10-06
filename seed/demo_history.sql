-- Ninety days of orders for the demo, ending the day before it runs, written
-- the way the shop writes them: one transaction per order and per later step
-- (payment, picking, dispatch, delivery, completion, return, restock), each
-- under the role production uses for it and through the function or trigger
-- the application goes through, so whatever the shop refuses is refused here
-- too. Orders, payments and what follows from them only: no reviews or
-- questions (ruled on #1173), and no coupons or past campaigns (ruled on #1173
-- during the review of #1219).
--
-- Stripe and ECPay issued none of it: payments are cs_demo_ sessions, refunds
-- re_demo_, and invoices sit on a made-up DM track, because
-- settle_invoice_issue accepts only a number shaped like a real one
-- (^[A-Z]{2}[0-9]{8}$). A void, an allowance or a refund started from /admin
-- on a seeded order therefore fails at the provider's sandbox, through goen's
-- usual failure path. Seeded returns carry no 折讓.
--
-- Run once, as a superuser, while goen is stopped (its sweeper would cancel
-- the unpaid orders before this script does), on a database built by
-- seed/dev_catalog.sql that holds an admin account, naming that database:
--
--     psql "$GOEN_DATABASE_URL" -X -v ON_ERROR_STOP=1 -v demo_database=<its name> -f seed/demo_history.sql
--
-- It refuses a database it was not named for, one whose catalogue did not
-- come from the seed, and one holding a payment that is neither a Stripe test
-- session (cs_test_) nor one of its own (cs_demo_): it must never write next to
-- real money. Every step commits as it goes, so a run that fails leaves a
-- partial history, which a later run refuses like a complete one: restore the
-- snapshot to run it again.

\set ON_ERROR_STOP on
\if :{?demo_database}
\else
\set demo_database ''
\endif

SET client_min_messages = warning;
-- The history is a single CALL that runs for minutes.
SET statement_timeout = 0;
-- psql does not substitute its variables inside a DO block's body.
SET demo_history.database = :'demo_database';

DO $$
DECLARE
    v_named constant text := current_setting('demo_history.database');
    v_foreign text;
BEGIN
    IF v_named = '' THEN
        RAISE EXCEPTION 'pass -v demo_database=<this database''s name> to write a demo history into it';
    END IF;
    IF v_named <> current_database() THEN
        RAISE EXCEPTION 'demo_database is %, not this database (%)', v_named, current_database();
    END IF;
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        RAISE EXCEPTION 'run this as a superuser: it acts as store, admin and maintenance and moves times in replica mode';
    END IF;
    SELECT p.provider_ref INTO v_foreign
    FROM payments p
    WHERE p.provider_ref NOT LIKE 'cs\_test\_%' AND p.provider_ref NOT LIKE 'cs\_demo\_%'
    ORDER BY p.created_at, p.id
    LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION 'payment % is not a demo or test payment: this database may hold real money', v_foreign;
    END IF;
    IF EXISTS (SELECT 1 FROM payments WHERE provider_ref LIKE 'cs\_demo\_%') THEN
        RAISE EXCEPTION 'this database already has a demo history (complete or partial); restore the snapshot to run again';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM inventory_movements WHERE reason = 'receipt' AND idempotency_key LIKE 'seed:%') THEN
        RAISE EXCEPTION 'this database holds no opening stock from seed/dev_catalog.sql, the only catalogue the history is written for';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM users WHERE role = 'admin') THEN
        RAISE EXCEPTION 'the back-office steps need an admin account to act as';
    END IF;
END
$$;

CREATE TEMP TABLE demo_window AS
SELECT shop_today() AS anchor,
       (shop_today() - 90)::timestamp AT TIME ZONE 'Asia/Taipei' AS opens,
       shop_today()::timestamp AT TIME ZONE 'Asia/Taipei' AS cutoff,
       (SELECT id FROM users WHERE role = 'admin' ORDER BY created_at, id LIMIT 1) AS staff;

CREATE TEMP TABLE demo_product (
    product_id  uuid PRIMARY KEY,
    price_cents bigint NOT NULL,
    shuffle     float8 NOT NULL,
    weight      float8
);

-- Only what was on sale when the history starts is restocked: a variant the
-- catalogue leaves sold out stays sold out.
CREATE TEMP TABLE demo_variant AS
SELECT pv.id AS variant_id
FROM product_variants pv
JOIN products p ON p.id = pv.product_id
WHERE p.status = 'active' AND pv.is_active AND pv.stock_quantity > pv.safety_stock;

CREATE TEMP TABLE demo_customer (
    k           integer,
    user_id     uuid,
    full_name   text NOT NULL,
    email       text NOT NULL,
    phone       text NOT NULL,
    postal_code text NOT NULL,
    city        text NOT NULL,
    district    text NOT NULL,
    street      text NOT NULL
);

CREATE TEMP TABLE demo_order (
    n            integer PRIMARY KEY,
    placed_at    timestamptz NOT NULL,
    placed_on    date NOT NULL,
    member       boolean NOT NULL,
    unpaid       boolean NOT NULL,
    line_count   integer NOT NULL,
    order_id     uuid,
    order_number text,
    user_id      uuid,
    return_id    uuid
);

CREATE TEMP TABLE demo_event (
    seq        integer GENERATED ALWAYS AS IDENTITY,
    happens_at timestamptz NOT NULL,
    kind       text NOT NULL,
    n          integer
);

\ir demo_backdating.sql

-- The catalogue's opening stock is the seed's receipt, stamped when the seed
-- ran. Read by date, every simulated sale would come before it and the stock
-- desk's ledger would go negative, so it arrives the day before the history.
-- The seed's shipping rates take effect from the history's own midnight:
-- seed/demo_shift.sql dates the database by them, and the seed may have run on
-- an earlier day.
BEGIN;
SET LOCAL session_replication_role = replica;
UPDATE inventory_movements m
SET created_at = w.opens - interval '1 day'
FROM pg_temp.demo_window w
WHERE m.reason = 'receipt' AND m.idempotency_key LIKE 'seed:%' AND m.created_at >= w.opens;
UPDATE shipping_method_versions
SET effective_at = (SELECT cutoff FROM pg_temp.demo_window)
WHERE id IN ('ffff0002-0000-4000-8000-000000000001', 'ffff0002-0000-4000-8000-000000000002');
COMMIT;

CREATE FUNCTION pg_temp.demo_person(p_k integer) RETURNS pg_temp.demo_customer
LANGUAGE plpgsql AS $$
DECLARE
    v pg_temp.demo_customer;
    s integer := 1 + floor(random() * 20)::integer;
    g integer := 1 + floor(random() * 24)::integer;
    a integer := 1 + floor(random() * 14)::integer;
BEGIN
    v.k := p_k;
    v.full_name := (ARRAY['陳', '林', '黃', '張', '李', '王', '吳', '劉', '蔡', '楊',
                          '許', '鄭', '謝', '郭', '洪', '曾', '邱', '廖', '賴', '周'])[s]
                || (ARRAY['怡君', '冠宇', '雅婷', '志明', '家豪', '佩珊', '俊傑', '欣怡',
                          '宇軒', '承翰', '詩涵', '建宏', '淑芬', '柏翰', '心怡', '彥廷',
                          '筱涵', '子豪', '惠雯', '宗翰', '雅雯', '冠廷', '郁婷', '家瑋'])[g];
    v.email := (ARRAY['yijun', 'guanyu', 'yating', 'zhiming', 'jiahao', 'peishan', 'junjie', 'xinyi',
                      'yuxuan', 'chenghan', 'shihan', 'jianhong', 'shufen', 'pohan', 'hsinyi', 'yenting',
                      'hsiaohan', 'tzuhao', 'huiwen', 'tsunghan', 'yawen', 'kuanting', 'yuting', 'chiawei'])[g]
            || '.'
            || (ARRAY['chen', 'lin', 'huang', 'chang', 'lee', 'wang', 'wu', 'liu', 'tsai', 'yang',
                      'hsu', 'cheng', 'hsieh', 'kuo', 'hung', 'tseng', 'chiu', 'liao', 'lai', 'chou'])[s]
            || coalesce(p_k::text, lpad(floor(random() * 1000)::integer::text, 3, '0'))
            || '@goen.invalid';
    v.phone := '09' || lpad(floor(random() * 100000000)::bigint::text, 8, '0');
    v.postal_code := (ARRAY['106', '110', '104', '220', '231', '330', '300',
                            '407', '403', '701', '802', '806', '260', '950'])[a];
    v.city := (ARRAY['台北市', '台北市', '台北市', '新北市', '新北市', '桃園市', '新竹市',
                     '台中市', '台中市', '台南市', '高雄市', '高雄市', '宜蘭縣', '臺東縣'])[a];
    v.district := (ARRAY['大安區', '信義區', '中山區', '板橋區', '新店區', '桃園區', '東區',
                         '西屯區', '西區', '東區', '苓雅區', '前鎮區', '宜蘭市', '臺東市'])[a];
    v.street := (ARRAY['復興南路一段', '松仁路', '南京東路二段', '文化路一段', '北新路三段',
                       '中正路', '光復路二段', '台灣大道三段', '公益路', '長榮路二段',
                       '四維三路', '中華五路', '中山路三段', '中華路一段'])[a]
             || ' ' || (1 + floor(random() * 280))::integer || ' 號';
    RETURN v;
END
$$;

-- An event at p_from minutes past midnight on p_day plus up to p_span more,
-- dropped when it would fall on or after the day the script runs.
CREATE FUNCTION pg_temp.demo_plan(p_n integer, p_day date, p_from float8, p_span float8, p_kind text)
RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    v_at timestamptz := (p_day::timestamp + make_interval(secs => (p_from + random() * p_span) * 60))
                        AT TIME ZONE 'Asia/Taipei';
BEGIN
    IF v_at < (SELECT cutoff FROM pg_temp.demo_window) THEN
        INSERT INTO pg_temp.demo_event (happens_at, kind, n) VALUES (v_at, p_kind, p_n);
    END IF;
END
$$;

-- The plan, from a fixed seed. The order count of a day is Poisson around a
-- rate that grows over the quarter, is highest at the weekend and rises while
-- a campaign runs; its times follow a small shop's day, mostly evenings.
DO $$
DECLARE
    w pg_temp.demo_window;
    r record;
    v_day date;
    v_rate float8;
    v_limit float8;
    v_p float8;
    v_count integer;
    v_minutes float8[];
    v_m float8;
    v_draw float8;
    v_at timestamptz;
    v_n integer := 0;
    v_member boolean;
    v_unpaid boolean;
    v_returned boolean;
    v_pick date;
    v_deliver date;
    v_ask date;
BEGIN
    PERFORM setseed(0.1173);
    SELECT * INTO w FROM pg_temp.demo_window;

    FOR r IN
        SELECT p.id, min(pv.price_cents) AS price_cents
        FROM products p
        JOIN product_variants pv ON pv.product_id = p.id
        JOIN pg_temp.demo_variant d ON d.variant_id = pv.id
        GROUP BY p.id
        ORDER BY p.id
    LOOP
        INSERT INTO pg_temp.demo_product (product_id, price_cents, shuffle)
        VALUES (r.id, r.price_cents, random());
    END LOOP;
    -- A few products sell most of the units, and the cheap ones sell more often.
    UPDATE pg_temp.demo_product d
    SET weight = power(s.rank::float8, -0.85) / (1 + d.price_cents / 600000.0)
    FROM (SELECT product_id, row_number() OVER (ORDER BY shuffle) AS rank
          FROM pg_temp.demo_product) s
    WHERE s.product_id = d.product_id;

    FOR i IN 0..89 LOOP
        v_day := w.anchor - 90 + i;
        v_rate := (3.9 + 1.6 * i / 89.0)
                * (ARRAY[0.85, 0.9, 0.95, 1.0, 1.1, 1.3, 1.25]::float8[])[extract(isodow FROM v_day)::integer]
                * CASE WHEN EXISTS (
                      SELECT 1 FROM sale_campaigns c
                      WHERE c.is_active
                        AND c.starts_at < (v_day + 1)::timestamp AT TIME ZONE 'Asia/Taipei'
                        AND c.ends_at > v_day::timestamp AT TIME ZONE 'Asia/Taipei'
                  ) THEN 1.4 ELSE 1 END;
        v_limit := exp(-v_rate);
        v_p := 1;
        v_count := -1;
        LOOP
            v_count := v_count + 1;
            v_p := v_p * random();
            EXIT WHEN v_p <= v_limit;
        END LOOP;

        v_minutes := '{}';
        FOR j IN 1..v_count LOOP
            v_draw := random();
            v_minutes := v_minutes || CASE
                WHEN v_draw < 0.04 THEN random() * 90
                WHEN v_draw < 0.14 THEN 480 + random() * 240
                WHEN v_draw < 0.32 THEN 720 + random() * 120
                WHEN v_draw < 0.50 THEN 840 + random() * 300
                -- Until 23:45, so the payment minutes later still lands on
                -- the same day, before the history ends.
                ELSE 1140 + random() * 285
            END;
        END LOOP;

        FOR v_m IN SELECT m FROM unnest(v_minutes) AS m ORDER BY m LOOP
            v_n := v_n + 1;
            v_at := (v_day::timestamp + make_interval(secs => v_m * 60)) AT TIME ZONE 'Asia/Taipei';
            v_member := random() < 0.7;
            -- An unpaid order is cancelled when its hold lapses, which must
            -- happen before the history ends.
            v_unpaid := random() < 0.08 AND v_at + interval '90 minutes' < w.cutoff;
            v_returned := v_member AND NOT v_unpaid AND random() < 0.05;
            v_draw := random();
            INSERT INTO pg_temp.demo_order (n, placed_at, placed_on, member, unpaid, line_count)
            VALUES (v_n, v_at, v_day, v_member, v_unpaid,
                    CASE WHEN v_draw < 0.7 THEN 1 WHEN v_draw < 0.92 THEN 2 ELSE 3 END);
            INSERT INTO pg_temp.demo_event (happens_at, kind, n) VALUES (v_at, 'place', v_n);

            IF v_unpaid THEN
                INSERT INTO pg_temp.demo_event (happens_at, kind, n)
                VALUES (v_at + make_interval(secs => (61 + random() * 14) * 60), 'lapse', v_n);
                CONTINUE;
            END IF;
            INSERT INTO pg_temp.demo_event (happens_at, kind, n)
            VALUES (v_at + make_interval(secs => (1 + random() * 5) * 60), 'pay', v_n);

            -- Picked and sent on the next weekday, or the same one for an
            -- order in before nine.
            v_pick := CASE WHEN extract(isodow FROM v_day) < 6 AND v_m < 540 THEN v_day ELSE v_day + 1 END;
            WHILE extract(isodow FROM v_pick) >= 6 LOOP
                v_pick := v_pick + 1;
            END LOOP;
            v_deliver := v_pick + CASE WHEN random() < 0.8 THEN 1 ELSE 2 END;
            PERFORM pg_temp.demo_plan(v_n, v_pick, 570, 120, 'picking');
            PERFORM pg_temp.demo_plan(v_n, v_pick, 840, 210, 'shipped');
            PERFORM pg_temp.demo_plan(v_n, v_deliver, 600, 540, 'delivered');
            IF v_returned THEN
                v_ask := v_deliver + 1 + floor(random() * 5)::integer;
                PERFORM pg_temp.demo_plan(v_n, v_ask, 1200, 150, 'return');
                PERFORM pg_temp.demo_plan(v_n, v_ask + 1, 660, 180, 'refund');
                PERFORM pg_temp.demo_plan(v_n, v_ask + 3, 900, 120, 'receive');
            END IF;
            PERFORM pg_temp.demo_plan(v_n, v_deliver + 10, 600, 300, 'completed');
        END LOOP;
    END LOOP;

    FOR i IN 0..89 LOOP
        v_day := w.anchor - 90 + i;
        IF extract(isodow FROM v_day) IN (2, 5) THEN
            PERFORM pg_temp.demo_plan(NULL, v_day, 630, 60, 'restock');
        END IF;
        IF i IN (6, 24, 41, 58, 75) THEN
            PERFORM pg_temp.demo_plan(NULL, v_day, 900, 60, 'grant');
        END IF;
    END LOOP;
END
$$;

-- The stock desk's receipt, for whatever is down to its last few units.
CREATE PROCEDURE pg_temp.demo_restock(p_at timestamptz)
LANGUAGE plpgsql AS $$
DECLARE
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
    v_variant record;
    v_quantity integer;
BEGIN
    FOR v_variant IN
        SELECT pv.id, pv.sku, pv.stock_quantity, pv.safety_stock
        FROM product_variants pv
        JOIN pg_temp.demo_variant d ON d.variant_id = pv.id
        WHERE pv.is_active AND pv.stock_quantity - pv.safety_stock < 4
        ORDER BY pv.id
    LOOP
        v_quantity := 14 + floor(random() * 10)::integer
                    - (v_variant.stock_quantity - v_variant.safety_stock);
        SET ROLE admin;
        PERFORM record_inventory_movement(v_variant.id, v_quantity, 'receipt',
            'demo-history:' || v_variant.sku || ':' || shop_day(p_at), 'admin', NULL, v_staff);
        PERFORM record_audit_event(v_staff, 'stock.receive', 'product_variants', v_variant.id,
            jsonb_build_object('sku', v_variant.sku, 'stock', v_variant.stock_quantity),
            jsonb_build_object('received', v_quantity));
        RESET ROLE;
    END LOOP;
END
$$;

-- Store credit the shop gives a few members, which some then spend.
CREATE PROCEDURE pg_temp.demo_grant()
LANGUAGE plpgsql AS $$
DECLARE
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
    v_customers integer := (SELECT count(*) FROM pg_temp.demo_customer);
    v_user uuid;
    v_amount bigint;
BEGIN
    FOR i IN 1..least(3, v_customers) LOOP
        SELECT user_id INTO v_user FROM pg_temp.demo_customer
        ORDER BY k OFFSET floor(random() * v_customers)::integer LIMIT 1;
        v_amount := (1 + floor(random() * 3))::bigint * 10000;
        SET ROLE admin;
        PERFORM grant_store_credit(v_user, v_amount, '會員購物金', v_staff, gen_random_uuid());
        PERFORM record_audit_event(v_staff, 'credit.grant', 'store_credit_entries', v_user, NULL,
            jsonb_build_object('amount_cents', v_amount, 'reason', '會員購物金'));
        RESET ROLE;
    END LOOP;
END
$$;

-- The invoice worker filing what a final sale owes: the system's claim, then
-- lease, send and settle with the provider's number.
CREATE PROCEDURE pg_temp.demo_invoice(p_n integer, p_number text, p_trigger text)
LANGUAGE plpgsql AS $$
DECLARE
    v_worker uuid := gen_random_uuid();
    v_operation uuid;
    v_document uuid;
    v_descriptions text[];
    v_quantities integer[];
    v_unit_prices bigint[];
    v_amounts bigint[];
BEGIN
    SET ROLE admin;
    v_operation := claim_invoice_issue(p_number, NULL, p_trigger);
    IF lease_invoice_operation(v_operation, v_worker, interval '5 minutes') <> v_operation
       OR NOT mark_invoice_operation_sent(v_operation, v_worker) THEN
        RAISE EXCEPTION 'invoice operation % of % could not be leased and sent', v_operation, p_number;
    END IF;
    SELECT array_agg(t.item ->> 'description' ORDER BY t.pos),
           array_agg((t.item ->> 'quantity')::integer ORDER BY t.pos),
           array_agg((t.item ->> 'unit_price_cents')::bigint ORDER BY t.pos),
           array_agg((t.item ->> 'amount_cents')::bigint ORDER BY t.pos)
    INTO v_descriptions, v_quantities, v_unit_prices, v_amounts
    FROM invoice_operations op
    CROSS JOIN LATERAL jsonb_array_elements(op.request_payload -> 'lines') WITH ORDINALITY AS t(item, pos)
    WHERE op.id = v_operation;
    v_document := settle_invoice_issue(v_operation, v_worker, 'DM' || lpad(p_n::text, 8, '0'),
        lpad(floor(random() * 10000)::integer::text, 4, '0'), now(),
        v_descriptions, v_quantities, v_unit_prices, v_amounts);
    IF v_document = '00000000-0000-0000-0000-000000000000' THEN
        RAISE EXCEPTION 'invoice operation % of % was not settled', v_operation, p_number;
    END IF;
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- Checkout: the lines a customer could have bought at that moment, held for
-- the payment window, with store credit spent first where the member has some.
CREATE PROCEDURE pg_temp.demo_place(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_buyer pg_temp.demo_customer;
    v_pool integer;
    v_regular integer;
    v_variants uuid[] := '{}';
    v_quantities integer[] := '{}';
    v_product uuid;
    v_variant uuid;
    v_quantity integer;
    v_choices integer;
    v_draw float8;
    v_subtotal bigint;
    v_ship record;
    v_shipping bigint;
    v_total bigint;
    v_credit bigint := 0;
    v_order uuid;
    v_number text;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;

    FOR i IN 1..v_ord.line_count LOOP
        v_quantity := CASE WHEN random() < 0.86 THEN 1 ELSE 2 END;
        v_variant := NULL;
        FOR attempt IN 1..6 LOOP
            v_draw := random();
            -- A campaign triples the chance of its products while it runs.
            SELECT s.product_id INTO v_product
            FROM (
                SELECT x.product_id,
                       sum(x.weight) OVER (ORDER BY x.product_id) AS upto,
                       sum(x.weight) OVER () AS total
                FROM (
                    SELECT d.product_id,
                           d.weight * CASE WHEN EXISTS (
                               SELECT 1
                               FROM sale_campaign_products cp
                               JOIN sale_campaigns c ON c.id = cp.campaign_id
                               WHERE cp.product_id = d.product_id AND c.is_active
                                 AND c.starts_at <= v_ord.placed_at AND c.ends_at > v_ord.placed_at
                           ) THEN 3 ELSE 1 END AS weight
                    FROM pg_temp.demo_product d
                ) x
            ) s
            WHERE s.upto > v_draw * s.total
            ORDER BY s.product_id
            LIMIT 1;

            SELECT count(*) INTO v_choices
            FROM product_variants pv
            WHERE pv.product_id = v_product AND pv.is_active
              AND pv.stock_quantity - pv.safety_stock >= v_quantity
              AND pv.id <> ALL (v_variants);
            IF v_choices > 0 THEN
                SELECT pv.id INTO v_variant
                FROM product_variants pv
                WHERE pv.product_id = v_product AND pv.is_active
                  AND pv.stock_quantity - pv.safety_stock >= v_quantity
                  AND pv.id <> ALL (v_variants)
                ORDER BY pv.id
                OFFSET floor(random() * v_choices)::integer
                LIMIT 1;
                EXIT;
            END IF;
        END LOOP;
        IF v_variant IS NOT NULL THEN
            v_variants := v_variants || v_variant;
            v_quantities := v_quantities || v_quantity;
        END IF;
    END LOOP;
    IF cardinality(v_variants) = 0 THEN
        RETURN;
    END IF;

    IF v_ord.member THEN
        SELECT count(*) INTO v_pool FROM pg_temp.demo_customer;
        IF v_pool < 6 OR (v_pool < 60 AND random() < 0.3) THEN
            v_buyer := pg_temp.demo_person(v_pool + 1);
            SET ROLE store;
            INSERT INTO users (email, full_name, phone, email_verified_at)
            VALUES (v_buyer.email, v_buyer.full_name, v_buyer.phone, now())
            RETURNING id INTO v_buyer.user_id;
            RESET ROLE;
            INSERT INTO pg_temp.demo_customer SELECT (v_buyer).*;
        ELSE
            -- Drawn first: random() in the WHERE clause would draw once per row.
            v_regular := 1 + floor(v_pool * power(random(), 1.6::float8))::integer;
            SELECT * INTO STRICT v_buyer FROM pg_temp.demo_customer WHERE k = v_regular;
        END IF;
    ELSE
        v_buyer := pg_temp.demo_person(NULL);
    END IF;

    SELECT sum(pv.price_cents * q.quantity) INTO v_subtotal
    FROM unnest(v_variants, v_quantities) AS q(variant_id, quantity)
    JOIN product_variants pv ON pv.id = q.variant_id;
    SELECT sv.id, sm.code, sv.name, sv.fee_cents, sv.free_over_cents INTO v_ship
    FROM shipping_method_versions sv
    JOIN shipping_methods sm ON sm.id = sv.method_id
    WHERE sm.code = 'home_delivery' AND sm.is_active AND sv.effective_at <= now()
    ORDER BY sv.effective_at DESC
    LIMIT 1;
    v_shipping := CASE WHEN v_ship.free_over_cents IS NOT NULL AND v_subtotal >= v_ship.free_over_cents
                       THEN 0 ELSE v_ship.fee_cents END;
    v_total := v_subtotal + v_shipping;
    -- next_order_number() numbers today's orders; this is the same count kept
    -- for the simulated day, after any order that day already has. store may
    -- not write the counter.
    INSERT INTO order_number_counters (business_date, last_no)
    VALUES (v_ord.placed_on, 1)
    ON CONFLICT (business_date) DO UPDATE
    SET last_no = order_number_counters.last_no + 1
    RETURNING 'GO-' || to_char(business_date, 'YYMMDD') || '-' || to_char(last_no, 'FM000000') INTO v_number;

    SET ROLE store;
    INSERT INTO orders (order_number, user_id, shipping_version_id, shipping_method_code,
                        shipping_method_name, shipping_cents, discount_cents, tax_cents, locale)
    VALUES (v_number, v_buyer.user_id, v_ship.id, v_ship.code, v_ship.name, v_shipping, 0, 0, 'zh-Hant')
    RETURNING id INTO v_order;
    FOR i IN 1..cardinality(v_variants) LOOP
        INSERT INTO order_lines (order_id, product_id, variant_id, sku, product_name, variant_label,
                                 warranty_note, warranty_months, unit_price_cents, quantity, position)
        SELECT v_order, p.id, pv.id, pv.sku, p.name,
               (SELECT string_agg(ov.value, ' · ' ORDER BY po.position, po.id)
                FROM variant_option_values vov
                JOIN product_options po ON po.id = vov.option_id
                JOIN product_option_values ov ON ov.id = vov.option_value_id
                WHERE vov.variant_id = pv.id),
               p.warranty_note, p.warranty_months, pv.price_cents, v_quantities[i], i - 1
        FROM product_variants pv
        JOIN products p ON p.id = pv.product_id
        WHERE pv.id = v_variants[i];
    END LOOP;
    FOR i IN 1..cardinality(v_variants) LOOP
        PERFORM hold_inventory(v_order, v_variants[i], v_quantities[i], interval '60 minutes',
                               'hold:' || v_order || ':' || v_variants[i]);
    END LOOP;
    INSERT INTO order_events (order_id, kind) VALUES (v_order, 'placed');
    IF v_buyer.user_id IS NOT NULL AND NOT v_ord.unpaid THEN
        v_credit := least(coalesce(lock_store_credit_for_checkout(v_buyer.user_id), 0), v_total);
        IF v_credit > 0 AND random() < 0.7 THEN
            PERFORM spend_store_credit(v_order, -v_credit);
        ELSE
            v_credit := 0;
        END IF;
    END IF;
    INSERT INTO invoice_preferences (order_id, invoice_type, customer_name, customer_email)
    VALUES (v_order, 'member_carrier', v_buyer.full_name, v_buyer.email);
    INSERT INTO order_private_data (order_id, email, recipient_name, phone,
                                    postal_code, city, district, street)
    VALUES (v_order, v_buyer.email, v_buyer.full_name, v_buyer.phone,
            v_buyer.postal_code, v_buyer.city, v_buyer.district, v_buyer.street);
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;

    UPDATE pg_temp.demo_order
    SET order_id = v_order, order_number = v_number, user_id = v_buyer.user_id
    WHERE n = p_n;

    IF v_credit = v_total THEN
        CALL pg_temp.demo_invoice(p_n, v_number, 'commit:' || v_number);
    END IF;
END
$$;

-- The Stripe webhook: capture of what the order still owes, its paid event and
-- points, then the invoice it makes due.
CREATE PROCEDURE pg_temp.demo_pay(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_owed bigint;
    v_ref text;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.order_id IS NULL THEN
        RETURN;
    END IF;
    v_owed := order_amount_after_credit(v_ord.order_id);
    IF v_owed = 0 THEN
        RETURN;
    END IF;
    v_ref := 'cs_demo_' || md5(v_ord.order_number);
    SET ROLE store;
    PERFORM open_payment(v_ord.order_id, v_ref, v_owed);
    PERFORM capture_payment(v_ref, v_owed, 'visa', '4242');
    INSERT INTO order_events (order_id, kind, note, occurred_at)
    SELECT v_ord.order_id, 'paid', 'Visa •••• 4242', max(pay.paid_at)
    FROM payments pay
    WHERE pay.order_id = v_ord.order_id AND pay.status = 'succeeded';
    PERFORM award_loyalty_points(v_ord.order_id);
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
    CALL pg_temp.demo_invoice(p_n, v_ord.order_number, 'evt_demo_' || substr(v_ref, 9, 24));
END
$$;

-- The sweeper: the hold lapsed unpaid, so the stock goes back and the order is
-- cancelled by the system.
CREATE PROCEDURE pg_temp.demo_lapse(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.order_id IS NULL THEN
        RETURN;
    END IF;
    SET ROLE store;
    PERFORM release_reservation(r.id)
    FROM inventory_reservations r
    WHERE r.order_id = v_ord.order_id AND r.state = 'held'
    ORDER BY r.id;
    UPDATE orders SET fulfillment_status = 'cancelled', cancelled_at = now()
    WHERE id = v_ord.order_id AND fulfillment_status = 'pending';
    INSERT INTO order_events (order_id, kind, by_system) VALUES (v_ord.order_id, 'cancelled', true);
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- The order page's status change. Picking closes a credit-funded order's
-- funding, as admin/orders does.
CREATE PROCEDURE pg_temp.demo_advance(p_n integer, p_status text)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.order_id IS NULL THEN
        RETURN;
    END IF;
    SET ROLE admin;
    UPDATE orders
    SET fulfillment_status = p_status,
        completed_at = CASE WHEN p_status = 'completed' THEN now() ELSE completed_at END
    WHERE id = v_ord.order_id;
    IF p_status = 'picking'
       AND NOT EXISTS (SELECT 1 FROM order_events WHERE order_id = v_ord.order_id AND kind = 'paid') THEN
        INSERT INTO order_events (order_id, kind, occurred_at) VALUES (v_ord.order_id, 'paid', now());
        PERFORM award_loyalty_points(v_ord.order_id);
    END IF;
    IF p_status IN ('delivered', 'completed') THEN
        UPDATE order_shipments SET delivered_at = greatest(now(), shipped_at)
        WHERE order_id = v_ord.order_id AND delivered_at IS NULL;
    END IF;
    INSERT INTO order_events (order_id, kind, actor_user_id) VALUES (v_ord.order_id, p_status, v_staff);
    PERFORM record_audit_event(v_staff, 'order.advance', 'orders', v_ord.order_id, NULL,
        jsonb_build_object('number', v_ord.order_number, 'status', p_status));
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- Dispatch: one T-Cat parcel carrying every line, its holds consumed.
CREATE PROCEDURE pg_temp.demo_ship(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
    v_tracking text := '9071' || lpad(p_n::text, 8, '0');
    v_shipment uuid;
    v_line record;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.order_id IS NULL THEN
        RETURN;
    END IF;
    SET ROLE admin;
    INSERT INTO order_shipments (order_id, carrier, tracking_number)
    VALUES (v_ord.order_id, 'black_cat', v_tracking)
    RETURNING id INTO v_shipment;
    FOR v_line IN
        SELECT ol.id, ol.quantity, ir.id AS reservation_id
        FROM order_lines ol
        JOIN inventory_reservations ir
          ON ir.order_id = ol.order_id AND ir.variant_id = ol.variant_id AND ir.state = 'held'
        WHERE ol.order_id = v_ord.order_id
        ORDER BY ol.position, ol.id
    LOOP
        INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
        VALUES (v_ord.order_id, v_shipment, v_line.id, v_line.quantity);
        PERFORM consume_reservation_partial(v_line.reservation_id, v_line.quantity);
    END LOOP;
    UPDATE orders SET fulfillment_status = 'shipped' WHERE id = v_ord.order_id;
    INSERT INTO order_events (order_id, kind, note, actor_user_id)
    VALUES (v_ord.order_id, 'shipped', '黑貓宅急便 ' || v_tracking, v_staff);
    PERFORM record_audit_event(v_staff, 'order.ship', 'orders', v_ord.order_id, NULL,
        jsonb_build_object('carrier', 'black_cat', 'tracking', v_tracking));
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- A member sends the first line back within the statutory seven days. The
-- reason is left empty, as the form allows, rather than invented.
CREATE PROCEDURE pg_temp.demo_return(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_return uuid;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.order_id IS NULL OR v_ord.user_id IS NULL THEN
        RETURN;
    END IF;
    SET ROLE store;
    INSERT INTO return_requests (order_id, requested_by_user_id, reason)
    VALUES (v_ord.order_id, v_ord.user_id, '')
    RETURNING id INTO v_return;
    INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
    SELECT ol.order_id, v_return, ol.id, ol.quantity
    FROM order_lines ol
    WHERE ol.order_id = v_ord.order_id
    ORDER BY ol.position, ol.id
    LIMIT 1;
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
    UPDATE pg_temp.demo_order SET return_id = v_return WHERE n = p_n;
END
$$;

-- Approval and payout, in admin/returns' and admin/refunds' order: the
-- decision, the card refund or the credit, the refunded event, the points
-- clawback.
CREATE PROCEDURE pg_temp.demo_refund(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
    v_request text := 'demo-history-' || p_n;
    v_card bigint;
    v_credit bigint;
    v_refundable bigint;
    v_refund uuid;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.return_id IS NULL THEN
        RETURN;
    END IF;
    SET ROLE admin;
    UPDATE return_requests SET status = 'approved', decided_at = now()
    WHERE id = v_ord.return_id AND status = 'requested';
    PERFORM record_audit_event(v_staff, 'return.decide', 'return_requests', v_ord.return_id, NULL,
        jsonb_build_object('decision', 'approved', 'resolution', '',
                           'policy_window', 'within', 'entitlement', 'statutory'));
    SELECT rr.card_refund_cents, rr.credit_refund_cents, rr.goods_refund_cents + rr.shipping_refund_cents
    INTO v_card, v_credit, v_refundable
    FROM return_requests rr
    WHERE rr.id = v_ord.return_id;
    IF v_card > 0 THEN
        v_refund := claim_return_refund_execution(v_ord.return_id, v_staff, v_request);
        PERFORM record_refund_succeeded(v_refund, 're_demo_' || substr(md5(v_ord.return_id::text), 1, 24),
                                        v_staff, v_request);
    END IF;
    IF v_credit > 0 THEN
        PERFORM compensate_return_with_credit(v_ord.return_id, v_credit, v_staff);
    END IF;
    INSERT INTO order_events (order_id, kind, note, actor_user_id, return_request_id)
    SELECT rr.order_id, 'refunded',
           (SELECT rf.provider_ref FROM refunds rf
            WHERE rf.return_request_id = rr.id AND rf.status = 'succeeded'
            ORDER BY rf.attempt_no DESC LIMIT 1),
           v_staff, rr.id
    FROM return_requests rr
    WHERE rr.id = v_ord.return_id;
    IF v_refundable > 0 THEN
        PERFORM reverse_return_points(v_ord.return_id);
    END IF;
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- The parcel comes back whole: inspected, restocked, and the return closed.
CREATE PROCEDURE pg_temp.demo_receive(p_n integer)
LANGUAGE plpgsql AS $$
DECLARE
    v_ord pg_temp.demo_order;
    v_staff uuid := (SELECT staff FROM pg_temp.demo_window);
    v_line record;
    v_lines integer := 0;
BEGIN
    SELECT * INTO v_ord FROM pg_temp.demo_order WHERE n = p_n;
    IF v_ord.return_id IS NULL
       OR (SELECT status FROM return_requests WHERE id = v_ord.return_id) <> 'approved' THEN
        RETURN;
    END IF;
    SET ROLE admin;
    FOR v_line IN
        SELECT rl.order_line_id, rl.quantity, ol.variant_id
        FROM return_request_lines rl
        JOIN order_lines ol ON ol.id = rl.order_line_id
        WHERE rl.return_request_id = v_ord.return_id
        ORDER BY ol.variant_id, rl.order_line_id
    LOOP
        UPDATE return_request_lines
        SET received_quantity = v_line.quantity, restocked_quantity = v_line.quantity
        WHERE return_request_id = v_ord.return_id AND order_line_id = v_line.order_line_id;
        PERFORM record_inventory_movement(v_line.variant_id, v_line.quantity, 'return',
            'return:' || v_ord.return_id || ':' || v_line.order_line_id,
            'return_request', v_ord.return_id, v_staff);
        v_lines := v_lines + 1;
    END LOOP;
    PERFORM record_audit_event(v_staff, 'return.inspect', 'return_requests', v_ord.return_id, NULL,
        jsonb_build_object('lines', v_lines, 'restocked', v_lines));
    UPDATE return_requests SET status = 'completed' WHERE id = v_ord.return_id AND status = 'approved';
    PERFORM record_audit_event(v_staff, 'return.complete', 'return_requests', v_ord.return_id, NULL,
        jsonb_build_object('resolution', ''));
    SET CONSTRAINTS ALL IMMEDIATE;
    RESET ROLE;
END
$$;

-- Moves what this transaction wrote back to p_at. Replica mode is what lets an
-- append-only or frozen row take a new time, and it also switches off foreign
-- keys and every other trigger, so only the columns seed/demo_backdating.sql
-- names change. uuidv7() is strictly ascending within a backend, so a row
-- whose id is past p_marker was created by this transaction.
CREATE PROCEDURE pg_temp.demo_backdate(p_at timestamptz, p_marker uuid)
LANGUAGE plpgsql AS $$
DECLARE
    v_began constant timestamptz := transaction_timestamp();
    v_xid constant text := (pg_current_xact_id()::text::bigint % 4294967296)::text;
    v_stmt text;
BEGIN
    SET LOCAL session_replication_role = replica;
    FOR v_stmt IN SELECT b.stmt FROM pg_temp.demo_backdating b LOOP
        EXECUTE v_stmt USING v_began, clock_timestamp(), v_began - p_at, p_marker, v_xid;
    END LOOP;
END
$$;

CREATE PROCEDURE pg_temp.demo_run()
LANGUAGE plpgsql AS $$
DECLARE
    v_event record;
    v_marker uuid;
BEGIN
    FOR v_event IN SELECT happens_at, kind, n FROM pg_temp.demo_event ORDER BY happens_at, seq LOOP
        v_marker := uuidv7();
        CASE v_event.kind
            WHEN 'restock' THEN CALL pg_temp.demo_restock(v_event.happens_at);
            WHEN 'grant' THEN CALL pg_temp.demo_grant();
            WHEN 'place' THEN CALL pg_temp.demo_place(v_event.n);
            WHEN 'pay' THEN CALL pg_temp.demo_pay(v_event.n);
            WHEN 'lapse' THEN CALL pg_temp.demo_lapse(v_event.n);
            WHEN 'shipped' THEN CALL pg_temp.demo_ship(v_event.n);
            WHEN 'return' THEN CALL pg_temp.demo_return(v_event.n);
            WHEN 'refund' THEN CALL pg_temp.demo_refund(v_event.n);
            WHEN 'receive' THEN CALL pg_temp.demo_receive(v_event.n);
            ELSE CALL pg_temp.demo_advance(v_event.n, v_event.kind);
        END CASE;
        RESET ROLE;
        CALL pg_temp.demo_backdate(v_event.happens_at, v_marker);
        COMMIT;
    END LOOP;
END
$$;

CALL pg_temp.demo_run();

SET ROLE maintenance;
DO $$ BEGIN PERFORM refresh_copurchases(); END $$;
RESET ROLE;

SELECT count(*) AS orders,
       count(*) FILTER (WHERE o.fulfillment_status = 'cancelled') AS cancelled,
       count(*) FILTER (WHERE d.return_id IS NOT NULL) AS returned,
       min(o.placed_at) AS first_placed,
       max(o.placed_at) AS last_placed
FROM pg_temp.demo_order d
JOIN orders o ON o.id = d.order_id;
