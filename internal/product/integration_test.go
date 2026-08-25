//go:build integration

package product_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"time"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ui/pages"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	p, stop, err := dbtest.Start(context.Background())
	if err != nil {
		slog.Error("start database", "error", err)
		os.Exit(1)
	}
	pool = p

	seed, err := os.ReadFile("../../seed/dev_catalog.sql")
	if err != nil {
		slog.Error("read seed", "error", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(context.Background(), string(seed)); err != nil {
		slog.Error("load seed", "error", err)
		os.Exit(1)
	}

	code := m.Run()
	stop()
	os.Exit(code)
}

func sel(pairs ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		q.Set(pairs[i], pairs[i+1])
	}
	return q.Encode()
}

func get(t *testing.T, slug, query string) (status int, body string) {
	t.Helper()
	h := product.NewHandler(product.NewStore(pool), slog.New(slog.DiscardHandler), "https://goen.example")
	target := "/p/" + slug
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.Detail(res, req)
	return res.Code, res.Body.String()
}

func TestDraftProductIs404(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE products SET status = 'draft' WHERE slug = 'pixelight-9-pro';`); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := product.NewStore(tx).Load(ctx, "pixelight-9-pro", nil); err == nil {
		t.Fatal("a draft product loaded; it must be indistinguishable from a missing one")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("draft product failed with %v, want not found", err)
	}
}

func TestVariantSelectionIsAURL(t *testing.T) {
	for _, tc := range []struct {
		name      string
		query     string
		wantPrice string
	}{
		{"256GB is the cheaper tier", sel("顏色", "星霧藍", "容量", "256GB"), "NT$33,900"},
		{"512GB costs more", sel("顏色", "星霧藍", "容量", "512GB"), "NT$36,900"},
		{"the other colour is the same price", sel("顏色", "曜石黑", "容量", "512GB"), "NT$36,900"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := get(t, "pixelight-9-pro", tc.query)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if !strings.Contains(body, tc.wantPrice) {
				t.Errorf("the page does not quote %s for %s", tc.wantPrice, tc.query)
			}
		})
	}
}

func TestPickerMarksUnavailableAgainstOtherChoices(t *testing.T) {
	view, err := product.NewStore(pool).Load(t.Context(), "aurora-slate-11",
		product.Selection{"容量": "128GB"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var checked bool
	for _, o := range view.Options {
		if o.Name != "顏色" {
			continue
		}
		for _, v := range o.Values {
			switch v.Value {
			case "曙光金":
				checked = true
				if v.Available {
					t.Error("曙光金 reads as available with 128GB chosen, but 曙光金/128GB is sold out")
				}
			case "午夜灰":
				if !v.Available {
					t.Error("午夜灰 reads as unavailable with 128GB chosen, but that combination can be bought")
				}
			}
		}
	}
	if !checked {
		t.Fatal("the colour picker did not offer 曙光金; the fixture assumption is stale")
	}
}

func TestSoldOutCombinationCannotBeBought(t *testing.T) {
	code, body := get(t, "aurora-slate-11", sel("顏色", "曙光金", "容量", "128GB"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "補貨中") {
		t.Error("a sold-out combination does not say so")
	}
	if !strings.Contains(body, `type="submit" disabled`) {
		t.Error("the add-to-cart button is live on a sold-out combination")
	}
}

func TestVariantAtTheSafetyFloorCannotBeBought(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, fixErr := tx.Exec(ctx, `
		UPDATE product_variants pv SET stock_quantity = pv.safety_stock
		FROM products p WHERE p.id = pv.product_id AND p.slug = 'pixelight-9-pro';`,
	); fixErr != nil {
		t.Fatalf("fixture: %v", fixErr)
	}

	view, err := product.NewStore(tx).Load(ctx, "pixelight-9-pro",
		product.Selection{"顏色": "星霧藍", "容量": "512GB"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if view.Sellable {
		t.Error("a variant holding exactly safety_stock reports as sellable; " +
			"record_inventory_movement would refuse the hold")
	}
	if view.CanBuy() {
		t.Error("the page would offer an add-to-cart button for stock that cannot be sold")
	}
}

func TestUnknownCombinationSaysSo(t *testing.T) {
	code, body := get(t, "pixelight-9-pro", sel("顏色", "螢光粉", "容量", "1TB"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "找不到這個組合") {
		t.Error("an impossible combination did not say so")
	}
	if strings.Contains(body, "NT$33,900") || strings.Contains(body, "NT$36,900") {
		t.Error("an impossible combination still quoted a price from some other variant")
	}
}

func TestPartialSelectionOffersNoButton(t *testing.T) {
	code, body := get(t, "pixelight-9-pro", sel("顏色", "星霧藍"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "請選擇規格") {
		t.Error("a partial selection does not ask for the rest")
	}
	if !strings.Contains(body, `type="submit" disabled`) {
		t.Error("the add-to-cart button is live on a partial selection")
	}
}

func TestDetailShowsWhatThePageIsFor(t *testing.T) {
	code, body := get(t, "pixelight-9-pro", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	for _, want := range []string{
		"Pixelight 9 Pro 5G",
		"Pixelight",
		"規格",
		// Truncated: templ escapes the inch mark into &#34;.
		"LTPO OLED 120Hz",
		"顧客評價",
		"同類商品",
		"ui-crumbs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the detail page is missing %q", want)
		}
	}
}

func TestOnlyACommittedPurchaseEarnsTheBadge(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)

	buyer := reviewer(t, "buyer")
	browser := reviewer(t, "browser")
	slug := activeSlug(t)
	buy(t, buyer, slug)

	if _, err := s.AddReview(ctx, slug, browser.String(), &product.Review{
		Rating: 5, Body: "看起來不錯,還沒買。",
	}); err != nil {
		t.Fatalf("a signed-in non-buyer could not review: %v", err)
	}
	if _, err := s.AddReview(ctx, slug, buyer.String(), &product.Review{
		Rating: 4, Body: "實際用過兩週,續航符合官方說法。",
	}); err != nil {
		t.Fatalf("a buyer could not review: %v", err)
	}

	var buyerVerified, browserVerified bool
	if err := pool.QueryRow(ctx, `
		SELECT is_verified_purchase FROM product_reviews r
		JOIN products p ON p.id = r.product_id
		WHERE p.slug = $1 AND r.user_id = $2`, slug, buyer).Scan(&buyerVerified); err != nil {
		t.Fatalf("read buyer review: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT is_verified_purchase FROM product_reviews r
		JOIN products p ON p.id = r.product_id
		WHERE p.slug = $1 AND r.user_id = $2`, slug, browser).Scan(&browserVerified); err != nil {
		t.Fatalf("read browser review: %v", err)
	}
	if !buyerVerified {
		t.Error("a committed purchase did not earn the badge")
	}
	if browserVerified {
		t.Error("somebody who never bought it is marked 已購買")
	}
}

func TestOneReviewPerPersonPerProduct(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	who := reviewer(t, "once")
	slug := activeSlug(t)

	if _, err := s.AddReview(ctx, slug, who.String(), &product.Review{
		Rating: 5, Body: "第一次評價的內容。",
	}); err != nil {
		t.Fatalf("first review: %v", err)
	}
	if _, err := s.AddReview(ctx, slug, who.String(), &product.Review{
		Rating: 1, Body: "第二次評價的內容。",
	}); !errors.Is(err, product.ErrAlreadyReviewed) {
		t.Fatalf("second review gave %v, want ErrAlreadyReviewed", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM product_reviews r JOIN products p ON p.id = r.product_id
		WHERE p.slug = $1 AND r.user_id = $2`, slug, who).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d reviews from one person on one product, want 1", n)
	}
}

func TestReviewValidation(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := activeSlug(t)

	long := strings.Repeat("字", product.MaxReviewBodyRunes+1)
	tests := []struct {
		name  string
		r     product.Review
		field string
	}{
		{"no rating", product.Review{Rating: 0, Body: "內容夠長了吧。"}, "rating"},
		{"rating above five", product.Review{Rating: 6, Body: "內容夠長了吧。"}, "rating"},
		{"rating below one", product.Review{Rating: -1, Body: "內容夠長了吧。"}, "rating"},
		{"empty body", product.Review{Rating: 5, Body: ""}, "body"},
		{"body too short", product.Review{Rating: 5, Body: "短"}, "body"},
		{"body too long", product.Review{Rating: 5, Body: long}, "body"},
		{"control character", product.Review{Rating: 5, Body: "內容\x00夾帶"}, "body"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			who := reviewer(t, "v-"+tt.name)
			errs, err := s.AddReview(ctx, slug, who.String(), &tt.r)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := errs[tt.field]; !ok {
				t.Errorf("got %v, want a %q error", errs, tt.field)
			}
		})
	}

	// The control: a review at the boundary IS accepted.
	who := reviewer(t, "v-ok")
	atLimit := strings.Repeat("字", product.MaxReviewBodyRunes)
	if errs, err := s.AddReview(ctx, slug, who.String(), &product.Review{
		Rating: 3, Body: atLimit,
	}); err != nil || len(errs) > 0 {
		t.Errorf("a %d-rune review was refused: %v %v", product.MaxReviewBodyRunes, errs, err)
	}
}

func reviewer(t *testing.T, tag string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ($1 || '-' || gen_random_uuid() || '@example.com', 'customer', '評價者')
		RETURNING id`, tag).Scan(&id); err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	return id
}

func activeSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

// buy gives a customer a committed order, which is what earns the badge.
func buy(t *testing.T, userID uuid.UUID, slug string) {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id`, userID).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	var total int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, pv.price_cents, 1
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.slug = $2 AND pv.is_active LIMIT 1
		RETURNING unit_price_cents`, orderID, slug).Scan(&total); err != nil {
		t.Fatalf("create line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'b@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	ref := "cs_review_" + orderID.String()
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, $3)`, orderID, ref, total); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, $2, NULL, NULL)`, ref, total); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestRestockNoticeIsIdempotent(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	vid := soldOutVariant(t)
	addr := waitingAddr(t)

	for range 3 {
		if err := s.RequestRestockNotice(ctx, vid.String(), addr, ""); err != nil {
			t.Fatalf("request: %v", err)
		}
	}

	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows after asking three times, want 1", n)
	}

	// Case does not make it a different person.
	if err := s.RequestRestockNotice(ctx, vid.String(), strings.ToUpper(waitingAddr(t)), ""); err != nil {
		t.Fatalf("uppercase: %v", err)
	}
	// Scoped to the address too: every restock test shares one variant.
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows after the same address in a different case, want 1", n)
	}
}

func TestANotifiedRequestDoesNotBlockTheNextOne(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	vid := soldOutVariant(t)
	const addr = "again@example.com"

	if err := s.RequestRestockNotice(ctx, vid.String(), addr, ""); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE stock_notifications SET notified_at = now()
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr); err != nil {
		t.Fatalf("mark notified: %v", err)
	}

	if err := s.RequestRestockNotice(ctx, vid.String(), addr, ""); err != nil {
		t.Fatalf("second: %v", err)
	}
	var pending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2) AND notified_at IS NULL`,
		vid, addr).Scan(&pending); err != nil {
		t.Fatalf("count: %v", err)
	}
	if pending != 1 {
		t.Errorf("%d pending requests after a previous one was notified, want 1 — "+
			"the index must bound PENDING rows, not every row ever", pending)
	}
}

func TestRestockNoticeRefusesAnUnusableAddress(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	vid := soldOutVariant(t)

	for _, addr := range []string{"", "  ", "nope", "a@", "@b.com", "a b@c.com"} {
		if err := s.RequestRestockNotice(ctx, vid.String(), addr, ""); !errors.Is(err, product.ErrNotifyInvalid) {
			t.Errorf("%q gave %v, want ErrNotifyInvalid", addr, err)
		}
	}
	// The control: a real address IS accepted.
	if err := s.RequestRestockNotice(ctx, vid.String(), "real@example.com", ""); err != nil {
		t.Errorf("a valid address was refused: %v", err)
	}
}

func soldOutVariant(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		SELECT pv.id FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		ORDER BY pv.stock_quantity - pv.safety_stock
		LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return id
}

func TestRecommendationsComeFromTheProjection(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)

	// Two products bought together twice, which is the minimum that counts.
	a, b := twoProductsBoughtTogether(t, 2)

	view, err := s.Load(ctx, a, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.AlsoBought) != 0 {
		t.Errorf("%d recommendations before a refresh; the read is computing "+
			"rather than projecting", len(view.AlsoBought))
	}

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	after, loadErr := s.Load(ctx, a, product.Selection{})
	if loadErr != nil {
		t.Fatalf("load: %v", loadErr)
	}
	if len(after.AlsoBought) != 1 {
		t.Fatalf("%d recommendations after a refresh, want 1", len(after.AlsoBought))
	}
	if after.AlsoBought[0].Slug != b {
		t.Errorf("recommended %q, want %q", after.AlsoBought[0].Slug, b)
	}
}

func TestOneSharedOrderIsNotARecommendation(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	a, _ := twoProductsBoughtTogether(t, 1)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Load(ctx, a, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.AlsoBought) != 0 {
		t.Errorf("%d recommendations from a single shared order, want 0",
			len(view.AlsoBought))
	}
}

func TestAnUncommittedOrderShapesNothing(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	// THREE unpaid orders: one would produce nothing whatever the committed
	// filter did, so a single order cannot tell the filter from the threshold.
	a, _ := twoProductsBoughtTogether(t, -3)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Load(ctx, a, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.AlsoBought) != 0 {
		t.Errorf("%d recommendations from orders nobody paid for, want 0",
			len(view.AlsoBought))
	}
}

// twoProductsBoughtTogether writes `committed` paid orders each containing two
// fresh products. A NEGATIVE `committed` leaves that many orders unpaid.
func twoProductsBoughtTogether(t *testing.T, committed int) (first, second string) {
	t.Helper()
	ctx := t.Context()

	// Fresh products per call: the projection is global.
	var v1, v2 uuid.UUID
	for i, slug := range []*string{&first, &second} {
		*slug = "rec-" + uuid.NewString()
		var variantID uuid.UUID
		if err := pool.QueryRow(ctx, `
			WITH b AS (
				INSERT INTO brands (slug, name) VALUES ('rb-'||gen_random_uuid(), '推薦品牌') RETURNING id
			), c AS (
				INSERT INTO categories (slug, name, position) SELECT 'rc-'||gen_random_uuid(), '推薦分類', coalesce(max(position) + 1, 0) FROM categories WHERE parent_id IS NULL RETURNING id
			), p AS (
				INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				SELECT b.id, c.id, $1, '推薦測試商品', 'draft', now() FROM b, c RETURNING id
			)
			INSERT INTO product_variants (product_id, sku, price_cents)
			SELECT p.id, 'REC-'||upper(replace(gen_random_uuid()::text,'-','')), 100000 FROM p
			RETURNING id`, *slug).Scan(&variantID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		// products_active_has_variant is deferred, so publish after the variant.
		if _, err := pool.Exec(ctx,
			`UPDATE products SET status = 'active' WHERE slug = $1`, *slug); err != nil {
			t.Fatalf("publish: %v", err)
		}
		if i == 0 {
			v1 = variantID
		} else {
			v2 = variantID
		}
	}

	rounds := committed
	if rounds < 0 {
		rounds = -rounds
	}
	rounds = max(rounds, 1)
	for range rounds {
		orderID := writeOrder(t, v1, v2)
		if committed > 0 {
			payFor(t, orderID)
		}
	}
	return first, second
}

func TestAnArchivedProductIsNotRecommended(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	a, b := twoProductsBoughtTogether(t, 2)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Load(ctx, a, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.AlsoBought) != 1 {
		t.Fatalf("%d recommendations before archiving, want 1", len(view.AlsoBought))
	}

	// No rebuild: the projection still holds the pair, as between ticks.
	if _, err := pool.Exec(ctx,
		`UPDATE products SET status = 'archived' WHERE slug = $1`, b); err != nil {
		t.Fatalf("archive: %v", err)
	}

	after, loadErr := s.Load(ctx, a, product.Selection{})
	if loadErr != nil {
		t.Fatalf("load: %v", loadErr)
	}
	if len(after.AlsoBought) != 0 {
		t.Errorf("%d recommendations after the partner was archived; the strip "+
			"links to a product nobody can buy", len(after.AlsoBought))
	}
}

// writeOrder places one order containing both variants and returns its id.
// orders_has_lines is deferred, so the header and its lines share a transaction.
func writeOrder(t *testing.T, v1, v2 uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1 RETURNING id`).Scan(&orderID); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'r@example.com', '收件', '0912345678', '110', '台北市', '信義區', '路 1 號')`,
		orderID); err != nil {
		t.Fatalf("delivery details: %v", err)
	}
	for pos, variantID := range []uuid.UUID{v1, v2} {
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_lines (order_id, variant_id, sku, product_name,
			                         unit_price_cents, quantity, position)
			VALUES ($1, $2, 'REC-SKU-'||$3::integer::text, '推薦測試商品', 100000, 1, $3::integer)`,
			orderID, variantID, pos); err != nil {
			t.Fatalf("create line: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit order: %v", err)
	}
	return orderID
}

// payFor commits an order through the posting functions a real payment uses.
func payFor(t *testing.T, orderID uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	ref := "rec_" + orderID.String()
	if _, err := pool.Exec(ctx,
		`SELECT open_payment($1, $2, 200000::bigint)`, orderID, ref); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`SELECT capture_payment($1, 200000::bigint, NULL, NULL)`, ref); err != nil {
		t.Fatalf("capture: %v", err)
	}
}

func TestAStaffAnswerStaysStaffWhenTheAuthorChangesRole(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	back := admin.NewStore(pool, admin.NewRefunder(""), nil)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)
	staff := newShopAuthor(t)

	if err := s.Ask(ctx, slug, customer, "這台支援 PD 3.1 嗎?"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)

	// The customer answers first, or insertion order matches the intended
	// order by accident.
	if err := s.Answer(ctx, qID, customer, "我實測過可以。"); err != nil {
		t.Fatalf("customer answer: %v", err)
	}
	staffCtx := account.WithUser(ctx, account.User{ID: staff, Role: "admin"})
	if err := back.AnswerQuestion(staffCtx, qID, staff, "支援,最高 45W。"); err != nil {
		t.Fatalf("staff answer: %v", err)
	}

	// The author is demoted to a plain customer AFTER writing both.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE id = $1`, uuid.MustParse(staff)); err != nil {
		t.Fatalf("demote: %v", err)
	}

	view, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.Questions) == 0 {
		t.Fatal("the question did not load")
	}
	answers := view.Questions[0].Answers
	if len(answers) != 2 {
		t.Fatalf("%d answers, want 2", len(answers))
	}
	if !answers[0].IsStaff {
		t.Error("the shop's answer is not first, or it lost its badge when the " +
			"author's role changed")
	}
	if answers[1].IsStaff {
		t.Error("a customer answer gained a staff badge")
	}
}

func TestAStorefrontReplyIsNeverBadgedAsTheShop(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "這台有支援 PD 嗎?"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	if err := s.Answer(ctx, qID, customer, "我自己實測是可以的。"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	var isStaff bool
	if err := pool.QueryRow(ctx, `
		SELECT is_staff FROM product_answers
		WHERE question_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`,
		uuid.MustParse(qID)).Scan(&isStaff); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if isStaff {
		t.Error("a storefront reply was stored as the shop's own answer")
	}
}

func TestAHiddenQuestionDisappearsWithItsAnswers(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "會被隱藏的問題"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	if err := s.Answer(ctx, qID, customer, "會一起消失的回答"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	before, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !containsQuestion(before.Questions, "會被隱藏的問題") {
		t.Fatal("the question was not visible before hiding")
	}

	if _, hideErr := pool.Exec(ctx,
		`UPDATE product_questions SET hidden_at = now() WHERE id = $1`,
		uuid.MustParse(qID)); hideErr != nil {
		t.Fatalf("hide: %v", hideErr)
	}

	after, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if containsQuestion(after.Questions, "會被隱藏的問題") {
		t.Error("a hidden question is still on the page")
	}
	for _, q := range after.Questions {
		for _, a := range q.Answers {
			if a.Body == "會一起消失的回答" {
				t.Error("an answer to a hidden question is still on the page")
			}
		}
	}

	if err := s.Answer(ctx, qID, customer, "太遲了"); err == nil {
		t.Error("a hidden question accepted a new answer")
	}
}

func TestAQuestionIsBoundedInRunesNotBytes(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	// 300 Han characters: 900 bytes, and exactly the limit.
	atLimit := strings.Repeat("問", product.MaxQuestionRunes)
	if err := s.Ask(ctx, slug, customer, atLimit); err != nil {
		t.Errorf("a question of exactly %d characters was refused: %v",
			product.MaxQuestionRunes, err)
	}
	if err := s.Ask(ctx, slug, customer, atLimit+"問"); !errors.Is(err, product.ErrQuestionInvalid) {
		t.Errorf("a question one character over gave %v, want ErrQuestionInvalid", err)
	}
	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.Ask(ctx, slug, customer, body); !errors.Is(err, product.ErrQuestionInvalid) {
			t.Errorf("%q gave %v, want ErrQuestionInvalid", body, err)
		}
	}
}

func containsQuestion(qs []pages.Question, body string) bool {
	for _, q := range qs {
		if q.Body == body {
			return true
		}
	}
	return false
}

func anyActiveProduct(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

func newCustomer(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('q-' || gen_random_uuid() || '@goen.invalid', 'customer', '發問者')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id.String()
}

func newShopAuthor(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('shop-answer-' || gen_random_uuid() || '@goen.invalid', 'admin', '店家')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create shop author: %v", err)
	}
	return id.String()
}

func latestQuestion(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM product_questions ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("find question: %v", err)
	}
	return id.String()
}

func TestASingleHiddenAnswerGoesWithoutTakingTheQuestion(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "這題會留著"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	for _, body := range []string{"留下來的回答", "會被隱藏的回答"} {
		if err := s.Answer(ctx, qID, customer, body); err != nil {
			t.Fatalf("answer %q: %v", body, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		UPDATE product_answers SET hidden_at = now()
		WHERE question_id = $1 AND body = '會被隱藏的回答'`,
		uuid.MustParse(qID)); err != nil {
		t.Fatalf("hide answer: %v", err)
	}

	view, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var found *pages.Question
	for i := range view.Questions {
		if view.Questions[i].Body == "這題會留著" {
			found = &view.Questions[i]
			break
		}
	}
	if found == nil {
		t.Fatal("hiding one answer took the question with it")
	}
	if len(found.Answers) != 1 {
		t.Fatalf("%d answers, want 1 — the hidden one is still showing", len(found.Answers))
	}
	if found.Answers[0].Body != "留下來的回答" {
		t.Errorf("the wrong answer survived: %q", found.Answers[0].Body)
	}
}

func TestTheProductPageKnowsWhatIsAlreadySaved(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('wishstate@example.com') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	const slug = "pixelight-9-pro"

	if s.SavedByUser(ctx, userID.String(), slug) {
		t.Fatal("an empty wishlist reported the product as saved")
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO wishlist_items (user_id, product_id)
		SELECT $1, id FROM products WHERE slug = $2`, userID, slug); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !s.SavedByUser(ctx, userID.String(), slug) {
		t.Error("a saved product reported as not saved")
	}

	// Without this a function returning true for anything would pass.
	if s.SavedByUser(ctx, userID.String(), "pixelight-9") {
		t.Error("an unsaved product reported as saved")
	}
	var other uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('wishother@example.com') RETURNING id`).Scan(&other); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if s.SavedByUser(ctx, other.String(), slug) {
		t.Error("one customer's wishlist answered for another")
	}
	if s.SavedByUser(ctx, "", slug) {
		t.Error("a guest reported as having saved something")
	}
}

func waitingAddr(t *testing.T) string {
	t.Helper()
	return "waiting-" + strings.ToLower(t.Name()) + "@example.com"
}

func TestHidingAReviewTakesItOutOfTheScore(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	const slug = "pixelight-9-pro"

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM products WHERE slug = $1`, slug).Scan(&productID); err != nil {
		t.Fatalf("find product: %v", err)
	}

	// Two of this test's own: hiding the only review would take the count to
	// zero and prove nothing.
	reviewBy(t, productID, "reviewhigh@example.com", 5)
	low, lowBody := reviewBy(t, productID, "reviewlow@example.com", 1)

	before, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if before.RatingCount < 2 {
		t.Fatalf("the fixture left %d reviews — this test would prove nothing",
			before.RatingCount)
	}

	if _, hideErr := pool.Exec(ctx,
		`UPDATE product_reviews SET hidden_at = now() WHERE id = $1`, low); hideErr != nil {
		t.Fatalf("hide: %v", hideErr)
	}

	after, err := s.Load(ctx, slug, product.Selection{})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.RatingCount != before.RatingCount-1 {
		t.Errorf("the count is %d after hiding one, want %d",
			after.RatingCount, before.RatingCount-1)
	}
	if after.Rating <= before.Rating {
		t.Errorf("the score went from %.2f to %.2f — hiding a one-star review did "+
			"not move it", before.Rating, after.Rating)
	}
	for _, r := range after.Reviews {
		if r.Body == lowBody {
			t.Error("a hidden review is still shown on the page")
		}
	}
}

func TestAHiddenReviewStillBlocksASecondOne(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	const slug = "pixelight-9"

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM products WHERE slug = $1`, slug).Scan(&productID); err != nil {
		t.Fatalf("find product: %v", err)
	}
	reviewID, _ := reviewBy(t, productID, "hiddenblocks@example.com", 3)

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT user_id FROM product_reviews WHERE id = $1`, reviewID).Scan(&userID); err != nil {
		t.Fatalf("read author: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE product_reviews SET hidden_at = now() WHERE id = $1`, reviewID); err != nil {
		t.Fatalf("hide: %v", err)
	}

	allowed, _, err := s.CanReview(ctx, slug, userID.String())
	if err != nil {
		t.Fatalf("CanReview: %v", err)
	}
	if allowed {
		t.Error("a customer whose review is hidden was offered the form again — the " +
			"insert would meet the unique index")
	}
}

// reviewBy writes one review from a new account and returns its id and body.
func reviewBy(t *testing.T, productID uuid.UUID, address string, rating int) (id uuid.UUID, body string) {
	t.Helper()
	ctx := t.Context()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id`, address).Scan(&userID); err != nil {
		t.Fatalf("create %s: %v", address, err)
	}
	body = "評價內容 " + address
	if err := pool.QueryRow(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, body)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		productID, userID, rating, body).Scan(&id); err != nil {
		t.Fatalf("create review: %v", err)
	}
	return id, body
}

// TestASimultaneousSecondReviewIsRefusedByName reaches the branch the ordinary
// path never touches.
//
// AddReview asks CanReview first, and in every ordinary case that is what
// answers: TestOneReviewPerPersonPerProduct drives the pre-check twice and
// never reaches the INSERT's own refusal. So the error mapping below it — the
// one that turns product_reviews_author_key into ErrAlreadyReviewed — is
// reached ONLY when two submissions race, which is the path least exercised
// and least able to announce that it had stopped matching. That is the coupon
// lesson exactly: a count proves the database held the line; only the ERROR
// says what the customer is about to be shown.
//
// The race is made deterministic rather than hoped for. T1 inserts the row and
// holds its transaction OPEN: under read committed the row is invisible, so
// CanReview passes, and the second INSERT then blocks on the unique index until
// T1 commits. Two goroutines behind a start channel would finish microseconds
// apart and never overlap.
func TestASimultaneousSecondReviewIsRefusedByName(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	who := reviewer(t, "race")
	slug := activeSlug(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
		SELECT p.id, $1, 5, '搶先寫進去的評價內容。', false
		FROM products p WHERE p.slug = $2`, who, slug); err != nil {
		t.Fatalf("hold the first review open: %v", err)
	}

	// The second submission sees nothing yet, so it gets past CanReview and
	// then waits on the index.
	refused := make(chan error, 1)
	go func() {
		_, addErr := s.AddReview(context.WithoutCancel(ctx), slug, who.String(), &product.Review{
			Rating: 1, Body: "同時送出的第二則評價內容。",
		})
		refused <- addErr
	}()

	// Give it time to reach the INSERT and block; then release it.
	time.Sleep(300 * time.Millisecond)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the first review: %v", err)
	}

	select {
	case addErr := <-refused:
		if !errors.Is(addErr, product.ErrAlreadyReviewed) {
			t.Errorf("a simultaneous second review gave %v, want ErrAlreadyReviewed — "+
				"the customer is shown a 500 instead of being told they have already reviewed it", addErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second review never returned")
	}
}

// TestLoadIgnoresThePagesOwnParameters holds the WIRING, not the filter.
//
// OnlyOptionsOf has its own unit test, and deleting the call to it from Load
// left that test green — the method was proven and its use was not. This drives
// the whole path: the query the restock form's own redirect produces, against a
// product that is in stock.
func TestLoadIgnoresThePagesOwnParameters(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := activeSlug(t)

	clean, err := s.Load(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load %s: %v", slug, err)
	}
	if !clean.SelectionOK {
		t.Fatalf("%s resolves no variant even with no parameters, so this proves nothing", slug)
	}

	// Exactly what POST /p/{slug}/notify redirects to, and what /compare links
	// back with.
	for _, sel := range []product.Selection{
		{"notify": "1"},
		{"ask": "1"},
		{"p": slug},
	} {
		got, loadErr := s.Load(ctx, slug, sel)
		if loadErr != nil {
			t.Fatalf("load %s with %v: %v", slug, sel, loadErr)
		}
		if !got.SelectionOK {
			t.Errorf("%v makes an in-stock product resolve no variant: the page says "+
				"the combination does not exist, renders no price box, and emits "+
				"OutOfStock with a price of 0.00 in its JSON-LD", sel)
		}
		if got.PriceCents != clean.PriceCents {
			t.Errorf("%v changed the price from %d to %d", sel, clean.PriceCents, got.PriceCents)
		}
	}
}
