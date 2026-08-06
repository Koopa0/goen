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

// sel builds the query string for an option selection.
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

// TestDraftProductIs404 is the access rule. A product that is not active must
// not render at all: a URL that shows a draft is how an unannounced product
// leaks, and it would be a 200 with a price on it.
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

// TestVariantSelectionIsAURL is the whole point of the picker: choosing an
// option is a navigation, so the price on the page follows the URL. If this
// stops holding, the page needs scripting to be usable and the write-face rule
// is broken.
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

// TestPickerMarksUnavailableAgainstOtherChoices is the one-variant rule applied
// to the picker, against real seed data: aurora-slate-11's 曙光金 exists in
// 128GB only as a sold-out variant, so with 128GB chosen that colour must read
// as unavailable. Judging a value against the whole product instead would mark
// it available, because 曙光金/256GB is in stock, and send a visitor to a
// combination they cannot buy.
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

// TestSoldOutCombinationCannotBeBought covers the button. The combination
// exists, so the page renders it and its price, but there must be no way to add
// it to a cart — the database would refuse the hold anyway, and a button that
// leads to a refusal is worse than one that is plainly disabled.
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

// TestVariantAtTheSafetyFloorCannotBeBought covers the band a stock_quantity
// check alone misses. record_inventory_movement refuses a sale or hold that
// would take stock below safety_stock, so a variant holding exactly the floor
// has stock and cannot be sold. The seed's sold-out variants are at zero, which
// `> 0` also excludes — this fixture is the only thing that separates the two
// predicates.
func TestVariantAtTheSafetyFloorCannotBeBought(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Put every variant of this product at exactly its floor.
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

// TestUnknownCombinationSaysSo covers the hand-edited URL. A combination no
// variant has must not quietly fall back to another variant's price — the page
// would then show a figure that belongs to something the visitor did not
// choose.
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

// TestPartialSelectionOffersNoButton pins that the page does not choose for the
// visitor. With only a colour picked there are two capacities left, and adding
// "whichever came first" to a cart is a purchase they did not make.
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

// TestDetailShowsWhatThePageIsFor is the broad check: the parts a detail page
// exists to show are present for a real product.
func TestDetailShowsWhatThePageIsFor(t *testing.T) {
	code, body := get(t, "pixelight-9-pro", "")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	for _, want := range []string{
		"Pixelight 9 Pro 5G", // name
		"Pixelight",          // brand
		"規格",                 // the spec table
		"LTPO OLED 120Hz",    // a spec value from the seed, minus the inch mark
		//   templ escapes into &#34;
		"顧客評價",      // reviews
		"同類商品",      // the related row
		"ui-crumbs", // crumbs
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the detail page is missing %q", want)
		}
	}
}

// TestOnlyACommittedPurchaseEarnsTheBadge proves the 已購買 badge tracks a
// committed order and nothing else.
//
// Anyone signed in may review; only a committed purchase carries 已購買. That
// split is deliberate — a shop where only buyers may speak hides the people who
// returned something, and a badge anyone can claim is worth nothing.
//
// The claim is not the caller's to make: product_reviews_verified_is_real
// refuses a false one, so a bug here becomes a refusal rather than an unearned
// badge. This asserts the caller gets it right in both directions.
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

// TestOneReviewPerPersonPerProduct proves a second review writes nothing.
//
// The LOCK is product_reviews_author_key, the unique index. Removing the Go
// check leaves this green — AddReview maps the unique violation to the same
// ErrAlreadyReviewed, so the two paths are indistinguishable from here. The Go
// check earns its place by answering without a failed INSERT and by letting the
// PDP hide the form; the guarantee is the index.
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

// TestReviewValidation proves the form refuses what the schema would, before
// it gets there.
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

	// The control: a review at the boundary IS accepted, or a validator that
	// refused everything would pass every case above.
	who := reviewer(t, "v-ok")
	atLimit := strings.Repeat("字", product.MaxReviewBodyRunes)
	if errs, err := s.AddReview(ctx, slug, who.String(), &product.Review{
		Rating: 3, Body: atLimit,
	}); err != nil || len(errs) > 0 {
		t.Errorf("a %d-rune review was refused: %v %v", product.MaxReviewBodyRunes, errs, err)
	}
}

// reviewer makes a customer with a unique email.
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

// activeSlug is a product anyone may review.
func activeSlug(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

// buy gives a customer a committed order for a product, which is what earns the
// verified badge. order_is_committed, not a payment row: a fully store-credited
// order is committed with no payment at all.
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

// TestRestockNoticeIsIdempotent proves asking twice is one request.
//
// The PDP has said 補貨中 since it was built with nowhere to leave an address.
// Now there is one, and pressing it twice must be one request — through the
// partial unique index, not through a read-then-write guard two concurrent
// visitors would both pass.
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
	// Scoped to the ADDRESS as well as the variant: the fixture picks the same
	// variant for every restock test, so a bare per-variant count is whatever
	// the other tests left — a different number on every shuffled run.
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND lower(email) = lower($2)`, vid, addr).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("%d rows after the same address in a different case, want 1", n)
	}

	// And the page can report it.
	waiting, err := s.WaitingForRestock(ctx, vid.String(), addr)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !waiting {
		t.Error("the address is on the list but WaitingForRestock says no")
	}
}

// TestANotifiedRequestDoesNotBlockTheNextOne proves the index bounds PENDING
// requests, not every request ever made.
//
// The unique index is PARTIAL — pending rows only — and that is exactly right:
// somebody told about one restock may want to hear about the next.
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

// TestRestockNoticeRefusesAnUnusableAddress proves a bad address never reaches
// the table.
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

// soldOutVariant is a variant nobody can buy.
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

// TestRecommendationsComeFromTheProjection proves the read is a lookup, not an
// aggregation.
//
// The read must not be an aggregation. Per request, the same answer cost 136 ms
// for the most-bought product and grew with order history forever — see
// docs/decisions/004-recommendation-read-model.md. This asserts the read model,
// not the timing: a projection nobody refreshed shows nothing, which is the
// behaviour that makes the staleness trade visible.
func TestRecommendationsComeFromTheProjection(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)

	// Two products bought together twice, which is the minimum that counts.
	a, b := twoProductsBoughtTogether(t, 2)

	// Before any refresh: nothing. The projection is the only source, so an
	// unrefreshed one is an empty strip rather than a live computation.
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

// TestOneSharedOrderIsNotARecommendation proves a coincidence is not shown as a
// pattern.
//
// A coincidence presented as a pattern is worse than an empty slot, because a
// shopper cannot tell them apart. At this catalogue size a threshold of one
// would make any two products that ever met "frequently bought together".
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

// TestAnUncommittedOrderShapesNothing proves an abandoned checkout is not a
// signal.
//
// An abandoned checkout is not a signal about anything. Committed is
// order_is_committed(), never "a payment row exists" — CLAUDE.md records three
// guards that got that wrong.
func TestAnUncommittedOrderShapesNothing(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	// THREE unpaid orders, not one. With a minimum of two shared orders, a
	// single one produces nothing whether or not it was paid — so a case built
	// on one could not tell the committed filter from the threshold, and stayed
	// green with the filter deleted.
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
// fresh products, and returns their slugs.
//
// A NEGATIVE `committed` places that many orders and leaves them unpaid, which
// is how the uncommitted case is expressed: the lines exist, there are enough
// of them to clear the threshold, and they must still count for nothing.
func twoProductsBoughtTogether(t *testing.T, committed int) (first, second string) {
	t.Helper()
	ctx := t.Context()

	// Fresh products per call, so one case's history cannot leak into another's
	// — the projection is global and a shared product would carry every case's
	// orders into every other case's assertions.
	var v1, v2 uuid.UUID
	for i, slug := range []*string{&first, &second} {
		*slug = "rec-" + uuid.NewString()
		var variantID uuid.UUID
		if err := pool.QueryRow(ctx, `
			WITH b AS (
				INSERT INTO brands (slug, name) VALUES ('rb-'||gen_random_uuid(), '推薦品牌') RETURNING id
			), c AS (
				INSERT INTO categories (slug, name) VALUES ('rc-'||gen_random_uuid(), '推薦分類') RETURNING id
			), p AS (
				INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
				SELECT b.id, c.id, $1, '推薦測試商品', 'draft', now() FROM b, c RETURNING id
			)
			INSERT INTO product_variants (product_id, sku, price_cents)
			SELECT p.id, 'REC-'||upper(replace(gen_random_uuid()::text,'-','')), 100000 FROM p
			RETURNING id`, *slug).Scan(&variantID); err != nil {
			t.Fatalf("create product: %v", err)
		}
		// Published after the variant exists: products_active_has_variant is
		// deferred and refuses an active product with nothing to sell.
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

// TestAnArchivedProductIsNotRecommended proves the strip never links to
// something nobody can buy.
//
// The projection outlives a product being retired — it is rebuilt on a ticker,
// not on a status change — so the read has to exclude what can no longer be
// bought. A recommendation slot pointing at an archived product is a 404
// somebody chose to click.
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

	// Archived WITHOUT rebuilding: the projection still holds the pair, which
	// is exactly the state a ticker leaves between runs.
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
//
// A function rather than a loop body, so its transaction is closed by ITS OWN
// return rather than by a defer that would pile up across iterations. The
// transaction exists because orders_has_lines is DEFERRED and fires at commit:
// a header written on its own is refused, correctly, since an order with
// nothing in it is not an order.
func writeOrder(t *testing.T, v1, v2 uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Rolled back on every path a Fatalf could take. Without it a failing case
	// leaks an open transaction holding the order-number counter, and the NEXT
	// case blocks on it forever rather than failing — which is how a broken
	// fixture became a hung suite rather than a red one.
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

// payFor commits an order through the same posting functions a real payment
// uses, so order_is_committed sees what it would see in production.
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

// TestAStaffAnswerStaysStaffWhenTheAuthorChangesRole proves the badge records
// what was true when it was written.
//
// is_staff is stored at the moment the answer is written, not derived from the
// author's role at read time. Derived, a customer who later joins the shop
// would retroactively turn their old answers into official ones, and a staff
// member who leaves would strip the badge from answers that WERE official.
// Either way the page lies about who said what.
func TestAStaffAnswerStaysStaffWhenTheAuthorChangesRole(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "這台支援 PD 3.1 嗎?"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)

	// The CUSTOMER answers first and the shop second, deliberately: with the
	// staff answer inserted first, insertion order agrees with the intended
	// order by accident and the case stays green with the ORDER BY deleted.
	if err := s.Answer(ctx, qID, customer, "我實測過可以。", false); err != nil {
		t.Fatalf("customer answer: %v", err)
	}
	if err := s.Answer(ctx, qID, customer, "支援,最高 45W。", true); err != nil {
		t.Fatalf("staff answer: %v", err)
	}

	// The author is demoted to a plain customer AFTER writing both.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE id = $1`, uuid.MustParse(customer)); err != nil {
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
	// Staff FIRST, even though it was written second: the shop's answer is what
	// somebody deciding came for, and burying it under customer replies is the
	// same as not having it.
	if !answers[0].IsStaff {
		t.Error("the shop's answer is not first, or it lost its badge when the " +
			"author's role changed")
	}
	if answers[1].IsStaff {
		t.Error("a customer answer gained a staff badge")
	}
}

// TestAHiddenQuestionDisappearsWithItsAnswers proves nothing is left stranded
// without its context.
//
// Hiding is the exception rather than a moderation queue — a question nobody
// sees is a question nobody answers — but when staff do hide one, the answers
// under it must go with it: an answer published under nothing is text with no
// context, and out of context is how a reasonable sentence becomes a wrong one.
func TestAHiddenQuestionDisappearsWithItsAnswers(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "會被隱藏的問題"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	if err := s.Answer(ctx, qID, customer, "會一起消失的回答", true); err != nil {
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

	// And no new answer can be added to it.
	if err := s.Answer(ctx, qID, customer, "太遲了", true); err == nil {
		t.Error("a hidden question accepted a new answer")
	}
}

// TestAQuestionIsBoundedInRunesNotBytes proves a Chinese question gets the same
// room as an English one.
//
// A question in Chinese is three bytes a character. A byte limit would give a
// Chinese-speaking customer a third of the room an English-speaking one gets,
// on a site whose content is Chinese.
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
	// Blank, and whitespace-only.
	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.Ask(ctx, slug, customer, body); !errors.Is(err, product.ErrQuestionInvalid) {
			t.Errorf("%q gave %v, want ErrQuestionInvalid", body, err)
		}
	}
}

// containsQuestion reports whether a body is among the loaded questions.
func containsQuestion(qs []pages.Question, body string) bool {
	for _, q := range qs {
		if q.Body == body {
			return true
		}
	}
	return false
}

// anyActiveProduct is a product a question can be asked about.
func anyActiveProduct(t *testing.T) string {
	t.Helper()
	var slug string
	if err := pool.QueryRow(t.Context(),
		`SELECT slug FROM products WHERE status = 'active' LIMIT 1`).Scan(&slug); err != nil {
		t.Fatalf("find product: %v", err)
	}
	return slug
}

// newCustomer is a fresh signed-in customer.
func newCustomer(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO users (email, role, full_name)
		VALUES ('q-' || gen_random_uuid() || '@goen.invalid', 'admin', '發問者')
		RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return id.String()
}

// latestQuestion is the most recently asked question's id.
func latestQuestion(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM product_questions ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("find question: %v", err)
	}
	return id.String()
}

// TestASingleHiddenAnswerGoesWithoutTakingTheQuestion proves the per-answer
// filter does its own work.
//
// Hiding one answer is a different act from hiding the question: the question
// stays askable and the other answers stay useful. A test that only hid
// questions could not see this — the answers vanish with the question anyway,
// so the per-answer filter did nothing and stayed green when deleted.
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
		if err := s.Answer(ctx, qID, customer, body, false); err != nil {
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

// TestTheProductPageKnowsWhatIsAlreadySaved holds the state the button renders.
//
// WishlistHas shipped with a comment naming the button it was for, and nothing
// called it: the page said 加入願望清單 whether or not the customer had already
// saved the product, so clicking it told them nothing about what had happened.
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

	// Another product of the same customer's is still not saved — without this
	// a function returning true for anything would pass.
	if s.SavedByUser(ctx, userID.String(), "pixelight-9") {
		t.Error("an unsaved product reported as saved")
	}
	// And another customer's wishlist is not this one's.
	var other uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('wishother@example.com') RETURNING id`).Scan(&other); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if s.SavedByUser(ctx, other.String(), slug) {
		t.Error("one customer's wishlist answered for another")
	}
	// A guest has no id at all and must not error into a true.
	if s.SavedByUser(ctx, "", slug) {
		t.Error("a guest reported as having saved something")
	}
}

// waitingAddr is an address of this test's own.
//
// stock_notifications_pending_key is on (variant, lower(email)), so two tests
// sharing an address against one variant are one row — and the second one's
// count is then whatever the first left. Under -shuffle that is a different
// answer every run.
func waitingAddr(t *testing.T) string {
	t.Helper()
	return "waiting-" + strings.ToLower(t.Name()) + "@example.com"
}

// TestHidingAReviewTakesItOutOfTheScore holds the half of moderation that
// matters.
//
// This is the whole point of the moderation column. The displayed rating is
// computed LIVE from these rows, so an abusive or planted one-star review moves
// a product's public score — and hiding it while leaving the average alone would
// achieve nothing at all.
//
// visible_reviews is what every rating reads, so one column change moves the
// PDP, the listing, search, the home page and the account together.
func TestHidingAReviewTakesItOutOfTheScore(t *testing.T) {
	ctx := t.Context()
	s := product.NewStore(pool)
	const slug = "pixelight-9-pro"

	var productID uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM products WHERE slug = $1`, slug).Scan(&productID); err != nil {
		t.Fatalf("find product: %v", err)
	}

	// Two reviews of this test's own: five stars, and a one-star to hide.
	// The five-star is what the average has to move AGAINST: hiding the only
	// review would take the count to zero and prove nothing about the score.
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
	// And the review itself is gone from the list.
	for _, r := range after.Reviews {
		if r.Body == lowBody {
			t.Error("a hidden review is still shown on the page")
		}
	}
}

// TestAHiddenReviewStillBlocksASecondOne holds the one reader that wants the
// hidden rows.
//
// product_reviews_product_user_key is on the BASE table, so a customer whose
// review was hidden must still be told they have written one. Reading the
// visible set here would offer them the form again and the insert would meet the
// index — a 500 on a page that had just invited them to write.
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

// reviewBy writes one review from a new account and returns its id AND its
// body.
//
// Both, because a test looking for the review in a rendered list needs the text
// that was actually written. The first version rebuilt the body from the id and
// the address it was really written from never matched — an assertion that
// could not fail.
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
