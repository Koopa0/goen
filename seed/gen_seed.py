#!/usr/bin/env python3
"""Generate goen's dev catalogue seed.

Emits a single SQL file: brands, a category tree, ~15 Traditional-Chinese 3C
products with options/variants (composite-FK chain), images (with alt text),
specs, and reviews. Everything respects the schema's formats and guards. IDs are
deterministic so the file is stable across runs (no uuid churn in git).
"""
import re

_counter = [0]
def uid(*_ignored):
    # A valid v4-shaped uuid from a monotonic counter: pure hex, version 4 and
    # variant nibbles set. Deterministic across runs (same generation order), so
    # the emitted ids are stable in git.
    _counter[0] += 1
    n = _counter[0]
    return f"{n:08x}-0000-4000-8000-{n:012x}"

def slugify_ascii(s):
    return s

BRANDS = [
    ("pixelight", "Pixelight"),
    ("aurora", "Aurora"),
    ("koto", "琴 Koto"),
    ("meridian", "Meridian"),
    ("nimbus", "Nimbus"),
]

# category tree: (slug, name, icon, parent_slug or None, position)
CATS = [
    ("phones", "手機", "phone", None, 0),
    ("laptops", "筆電", "laptop", None, 1),
    ("tablets", "平板", "tablet", None, 2),
    ("audio", "耳機與音響", "headphones", None, 3),
    ("wearables", "穿戴裝置", "watch", None, 4),
    ("accessories", "周邊配件", "plug", None, 5),
    ("chargers", "充電與線材", "plug", "accessories", 0),
    ("cases", "保護殼與包", "shield", "accessories", 1),
]

# product spec: (slug, name, brand, category, summary, price, compare_at,
#   [(color, capacity_or_None, sku_suffix, price_delta, stock)...], specs[], reviews[(rating,title,body)])
P = []
def add(**k): P.append(k)

add(slug="pixelight-9-pro", name="Pixelight 9 Pro 5G", brand="pixelight", cat="phones",
    summary="旗艦影像旗艦，鈦金屬邊框與 LTPO 螢幕。", price=3390000, compare=3690000,
    colors=["星霧藍","曜石黑"], caps=["256GB","512GB"], sku="PXL-9P",
    specs=[("螢幕","6.7\" LTPO OLED 120Hz"),("處理器","Pixelight X3"),("相機","主鏡 50MP + 潛望 5x"),("電池","5000mAh")],
    reviews=[(5,"影像真的頂","夜拍乾淨，變焦也堪用。"),(4,"手感好但略重","鈦框質感很好，就是重了點。")])
add(slug="pixelight-9", name="Pixelight 9 5G", brand="pixelight", cat="phones",
    summary="旗艦體驗，親民入手。", price=2590000, compare=None,
    colors=["薄荷綠","曜石黑"], caps=["128GB","256GB"], sku="PXL-9",
    specs=[("螢幕","6.3\" OLED 120Hz"),("處理器","Pixelight X3"),("相機","主鏡 50MP")],
    reviews=[(5,"CP 值高","該有的都有。")])
add(slug="aurora-edge-7", name="Aurora Edge 7", brand="aurora", cat="phones",
    summary="曲面螢幕與快充，續航一整天。", price=1990000, compare=2290000,
    colors=["曙光金","午夜灰"], caps=["256GB"], sku="AUR-E7",
    specs=[("螢幕","6.6\" 曲面 AMOLED"),("快充","80W"),("電池","5200mAh")],
    reviews=[(4,"充電超快","半小時就滿。"),(3,"曲面誤觸","偶爾會誤觸邊緣。")])
add(slug="meridian-book-14", name="Meridian Book 14", brand="meridian", cat="laptops",
    summary="14 吋輕薄金屬機身，一日續航。", price=4290000, compare=None,
    colors=["太空銀","石墨黑"], caps=["16GB/512GB","32GB/1TB"], sku="MRD-B14",
    specs=[("螢幕","14\" 2.8K OLED"),("處理器","Meridian M-Core 9"),("重量","1.29kg"),("續航","18 小時")],
    reviews=[(5,"螢幕很讚","OLED 看片超爽。"),(5,"鍵盤手感好","打字回饋剛好。")])
add(slug="meridian-book-16-pro", name="Meridian Book 16 Pro", brand="meridian", cat="laptops",
    summary="創作者的行動工作站。", price=6890000, compare=7290000,
    colors=["石墨黑"], caps=["32GB/1TB","64GB/2TB"], sku="MRD-B16P",
    specs=[("螢幕","16\" 4K mini-LED"),("處理器","Meridian M-Core 9 Max"),("顯示卡","獨顯 12GB")],
    reviews=[(5,"跑圖很順","3D 算圖明顯快。")])
add(slug="aurora-slate-11", name="Aurora Slate 11", brand="aurora", cat="tablets",
    summary="輕巧平板，支援手寫筆。", price=1490000, compare=None,
    colors=["曙光金","午夜灰"], caps=["128GB","256GB"], sku="AUR-S11",
    specs=[("螢幕","11\" 2.2K 120Hz"),("重量","465g"),("手寫筆","支援 Aurora Pen")],
    reviews=[(4,"追劇好夥伴","螢幕比例剛好。")])
add(slug="koto-pad-mini", name="Koto Pad mini", brand="koto", cat="tablets",
    summary="單手可握的小尺寸平板。", price=1290000, compare=1490000,
    colors=["櫻花粉","墨綠"], caps=["128GB"], sku="KOTO-PADM",
    specs=[("螢幕","8.3\" IPS"),("重量","297g")],
    reviews=[(5,"通勤神器","放外套口袋剛好。"),(4,"喇叭普通","外放稍小聲。")])
add(slug="nimbus-buds-pro", name="Nimbus Buds Pro", brand="nimbus", cat="audio",
    summary="主動降噪真無線耳機。", price=590000, compare=690000,
    colors=["雲白","曜石黑","薄荷綠"], caps=None, sku="NMB-BP",
    specs=[("降噪","主動式 ANC"),("續航","單次 6 小時 / 共 30 小時"),("防水","IPX4")],
    reviews=[(5,"降噪有感","捷運上很安靜。"),(4,"配戴舒適","久戴不會痛。"),(5,"回購第二副","送人自用兩相宜。")])
add(slug="koto-over-ear", name="Koto Over-Ear 靜", brand="koto", cat="audio",
    summary="頭戴式降噪耳機，木質調音。", price=990000, compare=None,
    colors=["胡桃","炭黑"], caps=None, sku="KOTO-OE",
    specs=[("單體","40mm 動圈"),("續航","40 小時"),("連線","藍牙 5.3 + 3.5mm")],
    reviews=[(5,"聲音溫暖","人聲很耐聽。")])
add(slug="meridian-watch-s3", name="Meridian Watch S3", brand="meridian", cat="wearables",
    summary="全天候健康量測智慧手錶。", price=890000, compare=990000,
    colors=["銀","石墨黑","玫瑰金"], caps=["41mm","45mm"], sku="MRD-WS3",
    specs=[("螢幕","AMOLED 常亮"),("感測","心率 / 血氧 / ECG"),("續航","48 小時")],
    reviews=[(4,"運動紀錄準","跑步配速蠻準。"),(5,"錶面好看","可換的錶面很多。")])
add(slug="nimbus-band-2", name="Nimbus Band 2", brand="nimbus", cat="wearables",
    summary="輕量手環，睡眠與心率監測。", price=190000, compare=None,
    colors=["黑","珊瑚橘","天空藍"], caps=None, sku="NMB-B2",
    specs=[("螢幕","1.47\" AMOLED"),("續航","14 天"),("防水","5ATM")],
    reviews=[(5,"便宜好用","睡眠追蹤意外準。")])
add(slug="aurora-charger-65", name="Aurora GaN 65W 充電器", brand="aurora", cat="chargers",
    summary="氮化鎵雙孔快充，手機筆電通用。", price=99000, compare=129000,
    colors=None, caps=None, sku="AUR-C65",
    specs=[("輸出","65W GaN"),("孔位","USB-C x2"),("體積","比火柴盒大一點")],
    reviews=[(5,"出門一顆搞定","筆電手機一起充。")])
add(slug="koto-cable-braided", name="Koto 編織 USB-C 線 2m", brand="koto", cat="chargers",
    summary="耐折編織線，支援 100W。", price=49000, compare=None,
    colors=["墨綠","炭黑"], caps=None, sku="KOTO-CBL",
    specs=[("長度","2m"),("功率","100W"),("材質","尼龍編織")],
    reviews=[(4,"耐用","用了半年沒壞。")])
add(slug="meridian-book-sleeve-14", name="Meridian 筆電內袋 14\"", brand="meridian", cat="cases",
    summary="羊毛氈內袋，防刮防潑水。", price=79000, compare=None,
    colors=["石墨灰","燕麥"], caps=None, sku="MRD-SLV14",
    specs=[("尺寸","適用 14 吋"),("材質","羊毛氈")],
    reviews=[(5,"質感好","比想像中挺。")])
add(slug="pixelight-9-pro-case", name="Pixelight 9 Pro 保護殼", brand="pixelight", cat="cases",
    summary="軍規防摔透明殼。", price=59000, compare=89000,
    colors=["透明","霧透"], caps=None, sku="PXL-9P-CASE",
    specs=[("防摔","軍規 2m"),("材質","TPU + PC")],
    reviews=[(4,"保護足夠","摔過一次沒事。")])

out = []
def w(s): out.append(s)

w("-- goen dev catalogue seed — generated by seed/gen_seed.py, do not hand-edit.")
w("-- Dev only. Loads as the owner. Idempotent-ish: run against a fresh schema.")
w("BEGIN;")
w("")
# brands — build the id map once, then emit using it (never call uid twice per row)
brand_id = {s: uid() for s,_ in BRANDS}
w("INSERT INTO brands (id, slug, name) VALUES")
w(",\n".join(f"    ('{brand_id[s]}', '{s}', '{n}')" for s,n in BRANDS) + ";")
w("")

# categories (roots first so parents exist)
w("INSERT INTO categories (id, parent_id, slug, name, icon_key, position) VALUES")
cat_id = {}
rows = []
for i,(s,n,icon,parent,pos) in enumerate(CATS,1):
    cat_id[s] = uid('c'+str(i), i)
for i,(s,n,icon,parent,pos) in enumerate(CATS,1):
    p = f"'{cat_id[parent]}'" if parent else "NULL"
    rows.append(f"    ('{cat_id[s]}', {p}, '{s}', '{n}', '{icon}', {pos})")
# ensure roots inserted before children: CATS already lists roots then children, and
# a single multi-row INSERT evaluates FKs at statement end, so order within is fine.
w(",\n".join(rows) + ";")
w("")

pv_products, pv_options, pv_optvals, pv_variants, pv_vov, pv_specs, pv_images, pv_reviews = [],[],[],[],[],[],[],[]
for pi, prod in enumerate(P, 1):
    pid = uid(f"a{pi}", pi)
    pv_products.append(
        f"    ('{pid}', '{brand_id[prod['brand']]}', '{cat_id[prod['cat']]}', "
        f"'{prod['slug']}', '{prod['name']}', '{prod['summary']}', 'active', now())")
    colors = prod.get("colors")
    caps = prod.get("caps")
    # options
    opt_ids = {}
    oidx = 0
    for oname, ovals in (("顏色", colors), ("容量", caps)):
        if not ovals:
            continue
        oidx += 1
        oid = uid(f"a{pi}o{oidx}", pi*10+oidx)
        opt_ids[oname] = (oid, [])
        pv_options.append(f"    ('{oid}', '{pid}', '{oname}', {oidx-1})")
        for vpos, val in enumerate(ovals):
            ovid = uid(f"a{pi}{oname[0]}{vpos}", pi*100+oidx*10+vpos)
            opt_ids[oname][1].append((ovid, val))
            pv_optvals.append(f"    ('{ovid}', '{pid}', '{oid}', '{val}', {vpos})")
    # variants: cross color x capacity (or just one axis, or single)
    vlist = []
    color_list = colors if colors else [None]
    cap_list = caps if caps else [None]
    vpos = 0
    for ci, color in enumerate(color_list):
        for ki, cap in enumerate(cap_list):
            # price rises with capacity index
            price = prod["price"] + (ki * 300000 if caps else 0)
            cmp = prod["compare"] + (ki * 300000 if (prod["compare"] and caps) else 0) if prod["compare"] else None
            suffix = prod["sku"]
            if color: suffix += "-" + str(ci+1)
            if cap: suffix += "-" + str(ki+1)
            vid = uid(f"a{pi}v{vpos}", pi*1000+vpos)
            # Stock has to include the states the storefront must render and the
            # listing must filter on. Every variant used to carry 3+ units, so
            # "in stock" matched the whole catalogue, no tile could show 缺貨,
            # and — worst — no product had stock SPLIT across its variants, which
            # is precisely the shape the facet rule exists for: a product whose
            # blue is sold out and whose black is not must not answer
            # "blue AND in stock". A seed that cannot express the bug cannot
            # demonstrate the guard against it.
            #
            # Deterministic, not random: the file is committed and must not churn.
            # Every third product sells out its FIRST variant only, so that
            # product is split; one product in nine sells out entirely, so the
            # out-of-stock tile state has a subject.
            # safety_stock is 2 below, and record_inventory_movement refuses a
            # sale or hold that would take stock below it — so the sellable
            # quantity is stock_quantity - safety_stock, and a variant holding
            # exactly 2 is NOT purchasable however available it looks. A seed
            # with no variant in that band lets `stock_quantity > 0` pass for
            # "in stock" and the storefront promises what the database refuses.
            if pi % 9 == 4:
                stock = 0                      # wholly sold out
            elif pi % 3 == 0 and vpos == 0:
                stock = 0                      # split: this colour gone, others not
            elif pi % 5 == 2 and vpos == 1:
                stock = 2                      # nonzero, at the floor, unsellable
            else:
                stock = max(12 - vpos, 3)
            cmp_sql = str(cmp) if cmp else "NULL"
            pv_variants.append(
                f"    ('{vid}', '{pid}', '{suffix}', {price}, {cmp_sql}, {stock}, 2, {vpos})")
            # variant_option_values
            if color:
                oid, vals = opt_ids["顏色"]
                ovid = vals[ci][0]
                pv_vov.append(f"    ('{pid}', '{vid}', '{oid}', '{ovid}')")
            if cap:
                oid, vals = opt_ids["容量"]
                ovid = vals[ki][0]
                pv_vov.append(f"    ('{pid}', '{vid}', '{oid}', '{ovid}')")
            vpos += 1
    # specs
    for spos,(label,val) in enumerate(prod["specs"]):
        sid = uid(f"a{pi}s{spos}", pi*10000+spos)
        pv_specs.append(f"    ('{sid}', '{pid}', '{label}', '{val}', {spos})")
    # images
    imgid = uid(f"a{pi}i0", pi*100000)
    alt = f"{prod['name']} 商品照"
    pv_images.append(f"    ('{imgid}', '{pid}', '{prod['slug']}-01.webp', '{alt}', 1600, 1200, 0)")
    # Reviews. The catalogue seed has no users and no orders, so none of these
    # can be a verified purchase — product_reviews_verified_is_real requires an
    # author with a committed order for the product, and claiming otherwise was
    # the demo data asserting something the schema is meant to refuse.
    for ri,(rating,title,body) in enumerate(prod["reviews"]):
        rid = uid(f"a{pi}r{ri}", pi*1000000+ri)
        pv_reviews.append(f"    ('{rid}', '{pid}', NULL, {rating}, '{title}', '{body}', false)")

def emit(header, rows):
    if not rows: return
    w(header)
    w(",\n".join(rows) + ";")
    w("")

emit("INSERT INTO products (id, brand_id, category_id, slug, name, summary, status, published_at) VALUES", pv_products)
emit("INSERT INTO product_options (id, product_id, name, position) VALUES", pv_options)
emit("INSERT INTO product_option_values (id, product_id, option_id, value, position) VALUES", pv_optvals)
emit("INSERT INTO product_variants (id, product_id, sku, price_cents, compare_at_price_cents, stock_quantity, safety_stock, position) VALUES", pv_variants)
emit("INSERT INTO variant_option_values (product_id, variant_id, option_id, option_value_id) VALUES", pv_vov)
emit("INSERT INTO product_specs (id, product_id, label, value, position) VALUES", pv_specs)
emit("INSERT INTO product_images (id, product_id, storage_key, alt_text, width, height, position) VALUES", pv_images)
emit("INSERT INTO product_reviews (id, product_id, user_id, rating, title, body, is_verified_purchase) VALUES", pv_reviews)

# Shipping. Checkout cannot exist without these: an order carries a snapshot of
# the version it was placed under, because a fee is a promise made at a moment
# and changing 宅配 from NT$80 to NT$100 must not make last month's orders
# unexplainable. Ids are fixed so a dev database can be rebuilt without every
# order's shipping_version_id changing underneath it.
SHIPPING = [
    ("home_delivery", "宅配到府", "黑貓宅急便", 8000, 300000, 0),
    ("store_pickup",  "超商取貨", "7-ELEVEN",   6000, 300000, 1),
]
w("INSERT INTO shipping_methods (id, code, position) VALUES")
ship_id = {c: f"ffff0001-0000-4000-8000-{i:012x}" for i,(c,*_ ) in enumerate(SHIPPING, 1)}
w(",\n".join(f"    ('{ship_id[c]}', '{c}', {pos})" for c,_,_,_,_,pos in SHIPPING) + ";")
w("")
w("-- free_over_cents is the 滿 NT$3,000 免運 the storefront advertises. The copy")
w("-- and this number are the same claim, so they change together.")
w("INSERT INTO shipping_method_versions (id, method_id, name, carrier, fee_cents, free_over_cents) VALUES")
w(",\n".join(
    f"    ('ffff0002-0000-4000-8000-{i:012x}', '{ship_id[c]}', '{n}', '{car}', {fee}, {free})"
    for i,(c,n,car,fee,free,_) in enumerate(SHIPPING, 1)) + ";")
w("")

w("COMMIT;")

import pathlib
here = pathlib.Path(__file__).resolve().parent
(here / "dev_catalog.sql").write_text("\n".join(out) + "\n")
print(f"wrote {here/'dev_catalog.sql'}: {len(P)} products, "
      f"{len(pv_variants)} variants, {len(pv_reviews)} reviews")
