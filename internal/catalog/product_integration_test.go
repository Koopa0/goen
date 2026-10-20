//go:build integration

package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/koopa0/goen/assets"
	"github.com/koopa0/goen/internal/admin/questions"
	"github.com/koopa0/goen/internal/catalog"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/user"
)

func sel(pairs ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		q.Set(pairs[i], pairs[i+1])
	}
	return q.Encode()
}

func getProduct(t *testing.T, slug, query string) (status int, body string) {
	t.Helper()
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	target := "/p/" + slug
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.Product(res, req)
	return res.Code, res.Body.String()
}

// TestChoosingAVariantHasNoSecondServerPath holds the property that makes the
// in-place swap safe to keep.
//
// The option links ask for the same URL they navigate to, and the page keeps
// only the region that changed. That works because the server answers an htmx
// request with the document it always answered with — no branch on HX-Request,
// no fragment endpoint, nothing that can drift out of step with the page a
// visitor gets with scripting off. The day someone adds that branch, the
// scripting-off path and the swapped path stop being the same thing, and this
// is what notices.
func TestChoosingAVariantHasNoSecondServerPath(t *testing.T) {
	const slug, query = "nimbus-buds-pro", "%E9%A1%8F%E8%89%B2=%E9%9B%B2%E7%99%BD"

	plain := render(t, slug, query, nil)
	if plain.Code != http.StatusOK {
		t.Fatalf("plain status = %d, want 200", plain.Code)
	}
	swapped := render(t, slug, query, map[string]string{
		"HX-Request": "true", "HX-Target": "buybox",
	})

	if swapped.Code != plain.Code {
		t.Errorf("htmx status = %d, plain = %d", swapped.Code, plain.Code)
	}
	if swapped.Body.String() != plain.Body.String() {
		t.Error("the htmx response body differs from the plain one: there is a second " +
			"server path, and the scripting-off page and the swapped region can now drift")
	}
	// The headers too, and not only the body: an HX-Retarget, an HX-Reswap or a
	// Vary that only htmx sees is a second path as surely as different markup
	// is, and it is the kind that reads as harmless while it drifts.
	if got, want := fmt.Sprint(swapped.Header()), fmt.Sprint(plain.Header()); got != want {
		t.Errorf("the htmx response headers differ from the plain one:\n  htmx  %s\n  plain %s", got, want)
	}
	if !strings.Contains(plain.Body.String(), `id="buybox"`) {
		t.Error("the response carries no #buybox for the swap to select")
	}
}

// render serves the detail page, optionally as htmx would ask for it, and hands
// back the whole response rather than two of its parts.
func render(t *testing.T, slug, query string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	target := "/p/" + slug
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.SetPathValue("slug", slug)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res := httptest.NewRecorder()
	h.Product(res, req)
	return res
}

// TestAnUntaggedGalleryIsTheSameForEveryChoice is why a product whose
// photographs show no option value keeps its gallery out of the swatch's swap.
//
// meridian-book-16-pro's photograph is untagged, so every combination of its
// options is served the same gallery. Selecting it into the swap would tear down
// an <img> and build an identical one: a cache read, a decode, and an empty
// frame in between for a picture nobody changed.
func TestAnUntaggedGalleryIsTheSameForEveryChoice(t *testing.T) {
	for _, slug := range []string{"meridian-book-16-pro"} {
		t.Run(slug, func(t *testing.T) {
			_, first := getProduct(t, slug, "")
			if strings.Contains(first, `hx-select-oob="#gallery`) {
				t.Fatalf("%s's gallery follows the choice; this lock needs a product whose photographs are all untagged", slug)
			}
			base := gallery(t, first)
			for _, href := range optionHrefs(t, first) {
				query := ""
				if i := strings.IndexByte(href, '?'); i >= 0 {
					query = href[i+1:]
				}
				if _, body := getProduct(t, slug, query); gallery(t, body) != base {
					t.Fatalf("choosing %q changed the gallery; the swap must carry it too", query)
				}
			}
		})
	}
}

// TestTheChosenValuesPhotographsLeadTheGallery holds the order the product page
// puts photographs in: those showing the variant on show, then those showing the
// product whichever value is chosen, then the other values', each group by
// position. The variant on show is the chosen one, or the default when nothing is
// chosen, so the landing page opens on the default variant's colour and not on
// whichever photograph was uploaded first.
func TestTheChosenValuesPhotographsLeadTheGallery(t *testing.T) {
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The seed tags pixelight-9-pro-02 with the black value at position 1. The
	// blue one borrows another product's embedded file, because a key naming no
	// embedded file is dropped from the gallery before it is rendered.
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_images (product_id, bundled_key, alt_text, position, option_value_id)
		SELECT p.id, m.key, m.value, m.position, v.id
		FROM products p
		JOIN product_option_values v ON v.product_id = p.id
		JOIN (VALUES ('星霧藍', 'aurora-edge-7-01.webp', 2)) AS m(value, key, position)
		  ON m.value = v.value
		WHERE p.slug = 'pixelight-9-pro'`); err != nil {
		t.Fatalf("tag a photograph: %v", err)
	}
	// An untagged photograph placed AFTER both colours', so position alone would
	// put the other colour's photograph ahead of it.
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_images (product_id, bundled_key, alt_text, position)
		SELECT id, 'nimbus-buds-pro-01.webp', '不分顏色', 3 FROM products
		WHERE slug = 'pixelight-9-pro'`); err != nil {
		t.Fatalf("add a late untagged photograph: %v", err)
	}

	first := assets.Image{BundledKey: "pixelight-9-pro-01.webp"}.URL()
	late := assets.Image{BundledKey: "nimbus-buds-pro-01.webp"}.URL()
	black := assets.Image{BundledKey: "pixelight-9-pro-02.webp"}.URL()
	blue := assets.Image{BundledKey: "aurora-edge-7-01.webp"}.URL()
	for _, tc := range []struct {
		name string
		sel  catalog.Selection
		want []string
	}{
		{"choosing black", catalog.Selection{"顏色": "曜石黑"}, []string{black, first, late, blue}},
		{"choosing blue and a capacity", catalog.Selection{"顏色": "星霧藍", "容量": "512GB"},
			[]string{blue, first, late, black}},
		// The default variant is the cheapest buyable one, which is blue.
		{"choosing nothing", nil, []string{blue, first, late, black}},
		{"choosing only a capacity", catalog.Selection{"容量": "512GB"},
			[]string{blue, first, late, black}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			view, err := catalog.NewStore(tx, slog.New(slog.DiscardHandler)).Product(ctx, "pixelight-9-pro", tc.sel)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got := make([]string, 0, len(view.Images))
			for _, img := range view.Images {
				got = append(got, img.URL)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("the gallery is\n  %v\nwant\n  %v", got, tc.want)
			}
			if !view.GalleryFollowsChoice() {
				t.Error("a gallery with tagged photographs does not follow the choice")
			}

			// And the page renders that order, with the gallery in the swap.
			var body strings.Builder
			if err := catalog.Product(catalog.ProductMeta(&view), &view).Render(ctx, &body); err != nil {
				t.Fatalf("render: %v", err)
			}
			markup := body.String()
			first := firstShot.FindStringSubmatch(markup)
			if len(first) < 2 || html.UnescapeString(first[1]) != tc.want[0] {
				t.Errorf("the page opens on %v, want %s", first, tc.want[0])
			}
			if !strings.Contains(markup, `hx-select-oob="#gallery,#buybar"`) {
				t.Error("the swatches leave the gallery out of the swap")
			}
		})
	}
}

var firstShot = regexp.MustCompile(`class="goen-pdp__shotimg" src="([^"]+)"`)

// gallery is the markup from #gallery up to #buybox, which is the whole of it.
func gallery(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `id="gallery"`)
	end := strings.Index(body, `id="buybox"`)
	if start < 0 || end <= start {
		t.Fatal("the page has no gallery before its buy column")
	}
	return body[start:end]
}

// optionHrefs is every choice the picker offers, in the order it offers them.
func optionHrefs(t *testing.T, body string) []string {
	t.Helper()
	hrefs := optionHref.FindAllStringSubmatch(body, -1)
	if len(hrefs) == 0 {
		t.Fatal("the page offers no option values")
	}
	out := make([]string, 0, len(hrefs))
	for _, m := range hrefs {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

var optionHref = regexp.MustCompile(`<a [^>]*class="goen-swatch[^"]*"[^>]*href="([^"]+)"`)

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
	if _, err := catalog.NewStore(tx, slog.New(slog.DiscardHandler)).Product(ctx, "pixelight-9-pro", nil); err == nil {
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
			code, body := getProduct(t, "pixelight-9-pro", tc.query)
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
	view, err := catalog.NewStore(pool, slog.New(slog.DiscardHandler)).Product(t.Context(), "aurora-slate-11",
		catalog.Selection{"容量": "128GB"})
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
	code, body := getProduct(t, "aurora-slate-11", sel("顏色", "曙光金", "容量", "128GB"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if got := strings.Count(body, "已售完"); got != 1 {
		t.Errorf("a sold-out combination says 已售完 %d times, want once", got)
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

	view, err := catalog.NewStore(tx, slog.New(slog.DiscardHandler)).Product(ctx, "pixelight-9-pro",
		catalog.Selection{"顏色": "星霧藍", "容量": "512GB"})
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
	code, body := getProduct(t, "pixelight-9-pro", sel("顏色", "螢光粉", "容量", "1TB"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "找不到這個組合") {
		t.Error("an impossible combination did not say so")
	}
	if !strings.Contains(body, "NT$33,900 起") {
		t.Error("an impossible combination lost the catalogue's starting price")
	}
	if !strings.Contains(body, "請選擇選項") || !strings.Contains(body, `type="submit" disabled`) || !strings.Contains(body, `name="variant" value=""`) {
		t.Error("an impossible combination must request a choice without offering a purchase variant")
	}
}

func TestPartialSelectionOffersNoButton(t *testing.T) {
	code, body := getProduct(t, "pixelight-9-pro", sel("顏色", "星霧藍"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(body, "請選擇選項") {
		t.Error("a partial selection does not ask for the rest")
	}
	if !strings.Contains(body, `type="submit" disabled`) {
		t.Error("the add-to-cart button is live on a partial selection")
	}
}

func TestDetailShowsWhatThePageIsFor(t *testing.T) {
	code, body := getProduct(t, "pixelight-9-pro", "")
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

func TestOnlyACustomerWhoReceivedTheProductMayReview(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := activeSlug(t)

	received := reviewer(t, "received")
	receive(t, received, slug)
	paidOnly := reviewer(t, "paid-only")
	buy(t, paidOnly, slug)
	browser := reviewer(t, "browser")

	for name, who := range map[string]uuid.UUID{"never bought": browser, "paid, not delivered": paidOnly} {
		standing, err := s.ReviewStanding(ctx, slug, who.String())
		if err != nil || standing != catalog.ReviewNotDelivered {
			t.Errorf("%s: ReviewStanding = %v, %v; want ReviewNotDelivered", name, standing, err)
		}
		if _, err := s.AddReview(ctx, slug, who.String(), &catalog.Review{
			Rating: 5, Body: "還沒收到商品,不能留下評價。",
		}); !errors.Is(err, catalog.ErrNotDelivered) {
			t.Errorf("%s: AddReview gave %v, want ErrNotDelivered", name, err)
		}
	}

	form := url.Values{"rating": {"5"}, "body": {"沒有收到商品也想送出評價。"}}
	req := httptest.NewRequestWithContext(
		user.NewContext(ctx, user.User{ID: browser.String()}),
		http.MethodPost, "/p/"+slug+"/reviews", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	catalog.NewHandler(s, slog.New(slog.DiscardHandler), "https://goen.example").Review(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("a post without a delivered order returned %d, want 422", res.Code)
	}

	if standing, err := s.ReviewStanding(ctx, slug, received.String()); err != nil || standing != catalog.ReviewOpen {
		t.Fatalf("a delivered order: ReviewStanding = %v, %v; want ReviewOpen", standing, err)
	}
	if errs, err := s.AddReview(ctx, slug, received.String(), &catalog.Review{
		Rating: 4, Body: "實際用過兩週,續航符合官方說法。",
	}); err != nil || len(errs) != 0 {
		t.Fatalf("a customer who received it could not review: %v %v", errs, err)
	}

	var rows int
	var verified bool
	if err := pool.QueryRow(ctx, `
		SELECT count(*), coalesce(bool_and(is_verified_purchase), false)
		FROM product_reviews r JOIN products p ON p.id = r.product_id
		WHERE p.slug = $1 AND r.user_id = ANY($2)`,
		slug, []uuid.UUID{received, paidOnly, browser}).Scan(&rows, &verified); err != nil {
		t.Fatalf("read reviews: %v", err)
	}
	if rows != 1 || !verified {
		t.Errorf("%d reviews written (verified %t), want only the received customer's, verified", rows, verified)
	}
}

func TestRetiringAPurchasedVariantDoesNotEraseVerifiedPurchase(t *testing.T) {
	ctx := t.Context()
	who := reviewer(t, "retired-variant")
	tx, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	slug := "retired-variant-" + uuid.NewString()
	var productID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO products (brand_id, category_id, slug, name, status, published_at)
		SELECT b.id, c.id, $1, '規格退役測試商品', 'draft', now()
		FROM brands b CROSS JOIN categories c
		WHERE c.parent_id IS NULL
		ORDER BY b.id, c.id LIMIT 1
		RETURNING id`, slug).Scan(&productID); queryErr != nil {
		t.Fatalf("create product: %v", queryErr)
	}
	var boughtVariant uuid.UUID
	for pos := range 2 {
		var id uuid.UUID
		if queryErr := tx.QueryRow(ctx, `
			INSERT INTO product_variants
				(product_id, sku, price_cents, stock_quantity, position)
			VALUES ($1, $2, 100000, 1, $3)
			RETURNING id`, productID,
			"RETIRED-"+strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")), pos).Scan(&id); queryErr != nil {
			t.Fatalf("create variant %d: %v", pos, queryErr)
		}
		if pos == 0 {
			boughtVariant = id
		}
	}
	if _, execErr := tx.Exec(ctx,
		`UPDATE products SET status = 'active' WHERE id = $1`, productID); execErr != nil {
		t.Fatalf("publish product: %v", execErr)
	}

	var orderID, lineID uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, user_id, shipping_version_id,
		                    shipping_method_code, shipping_method_name, shipping_cents)
		SELECT next_order_number(), $1, v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id`, who).Scan(&orderID); queryErr != nil {
		t.Fatalf("create order: %v", queryErr)
	}
	// Supply only variant_id, as older/import callers do. The schema must bind
	// and retain the durable product identity when the variant is retired.
	if queryErr := tx.QueryRow(ctx, `
		INSERT INTO order_lines
			(order_id, variant_id, sku, product_name, unit_price_cents, quantity)
		SELECT $1, pv.id, pv.sku, p.name, 100000, 1 FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2
		RETURNING order_lines.id`, orderID, boughtVariant).Scan(&lineID); queryErr != nil {
		t.Fatalf("create line: %v", queryErr)
	}
	if _, savepointErr := tx.Exec(ctx, `SAVEPOINT mismatched_order_line_product`); savepointErr != nil {
		t.Fatalf("savepoint: %v", savepointErr)
	}
	_, mismatchErr := tx.Exec(ctx, `
		INSERT INTO order_lines
			(order_id, product_id, variant_id, sku, product_name,
			 unit_price_cents, quantity, position)
		SELECT $1, p.id, $2, 'RETIRED-MISMATCH', '不相干商品', 1, 1, 1
		FROM products p WHERE p.id <> $3 ORDER BY p.id LIMIT 1`,
		orderID, boughtVariant, productID)
	pgErr, isPG := errors.AsType[*pgconn.PgError](mismatchErr)
	if mismatchErr == nil || !isPG || pgErr.ConstraintName != "order_lines_variant_product_fk" {
		t.Errorf("mismatched product/variant gave %v, want order_lines_variant_product_fk", mismatchErr)
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT mismatched_order_line_product`); rollbackErr != nil {
		t.Fatalf("rollback mismatch: %v", rollbackErr)
	}
	if _, privateErr := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'retired@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); privateErr != nil {
		t.Fatalf("create private data: %v", privateErr)
	}
	ref := "cs_retired_" + orderID.String()
	if _, openErr := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, ref); openErr != nil {
		t.Fatalf("open payment: %v", openErr)
	}
	if _, captureErr := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, ref); captureErr != nil {
		t.Fatalf("capture: %v", captureErr)
	}

	if _, retireErr := tx.Exec(ctx,
		`UPDATE product_variants SET is_active = false WHERE id = $1`, boughtVariant); retireErr != nil {
		t.Fatalf("retire purchased variant: %v", retireErr)
	}
	if _, savepointErr := tx.Exec(ctx, `SAVEPOINT hard_delete_retired_variant`); savepointErr != nil {
		t.Fatalf("savepoint hard delete: %v", savepointErr)
	}
	_, deleteErr := tx.Exec(ctx, `DELETE FROM product_variants WHERE id = $1`, boughtVariant)
	pgErr, isPG = errors.AsType[*pgconn.PgError](deleteErr)
	if deleteErr == nil || !isPG || pgErr.ConstraintName != "order_lines_variant_product_fk" {
		t.Errorf("hard delete of purchased variant gave %v, want order_lines_variant_product_fk", deleteErr)
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT hard_delete_retired_variant`); rollbackErr != nil {
		t.Fatalf("rollback hard delete: %v", rollbackErr)
	}
	var keptProduct uuid.UUID
	var keptVariant uuid.UUID
	if queryErr := tx.QueryRow(ctx, `
		SELECT product_id, variant_id FROM order_lines WHERE id = $1`, lineID).
		Scan(&keptProduct, &keptVariant); queryErr != nil {
		t.Fatalf("read retired order line: %v", queryErr)
	}
	if keptProduct != productID || keptVariant != boughtVariant {
		t.Fatalf("retired line kept product/variant %s/%s; want %s/%s",
			keptProduct, keptVariant, productID, boughtVariant)
	}

	for _, status := range []string{"picking", "shipped", "delivered"} {
		if _, moveErr := tx.Exec(ctx,
			`UPDATE orders SET fulfillment_status = $2 WHERE id = $1`, orderID, status); moveErr != nil {
			t.Fatalf("move the order to %s: %v", status, moveErr)
		}
	}
	s := catalog.NewStore(tx, slog.New(slog.DiscardHandler))
	standing, err := s.ReviewStanding(ctx, slug, who.String())
	if err != nil || standing != catalog.ReviewOpen {
		t.Fatalf("ReviewStanding after variant retirement = %v, %v; want ReviewOpen, nil", standing, err)
	}
	if errs, addErr := s.AddReview(ctx, slug, who.String(), &catalog.Review{
		Rating: 5, Body: "買過的規格退役後仍然保留已購買證明。",
	}); addErr != nil || len(errs) != 0 {
		t.Fatalf("AddReview after variant retirement = %v, %v", errs, addErr)
	}
	var badge bool
	if queryErr := tx.QueryRow(ctx, `
		SELECT is_verified_purchase FROM product_reviews
		WHERE product_id = $1 AND user_id = $2`, productID, who).Scan(&badge); queryErr != nil {
		t.Fatalf("read badge: %v", queryErr)
	}
	if !badge {
		t.Error("the historical purchase lost its verified badge with the variant")
	}
}

func TestOneReviewPerPersonPerProduct(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	who := reviewer(t, "once")
	slug := activeSlug(t)
	receive(t, who, slug)

	if _, err := s.AddReview(ctx, slug, who.String(), &catalog.Review{
		Rating: 5, Body: "第一次評價的內容。",
	}); err != nil {
		t.Fatalf("first review: %v", err)
	}
	if _, err := s.AddReview(ctx, slug, who.String(), &catalog.Review{
		Rating: 1, Body: "第二次評價的內容。",
	}); !errors.Is(err, catalog.ErrAlreadyReviewed) {
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

// The seeded reviews are what a demo visitor reads first. Each has a named
// customer behind it, none is dated in the future, and they spread over more
// than a month rather than all reading "today".
func TestTheSeededReviewsReadLikeCustomers(t *testing.T) {
	ctx := t.Context()
	var total, nameless, future, beforeAccount int
	var spanDays float64
	if err := pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE coalesce(u.full_name, '') = ''),
		       count(*) FILTER (WHERE r.created_at > now()),
		       count(*) FILTER (WHERE r.created_at < u.created_at),
		       coalesce(extract(epoch FROM max(r.created_at) - min(r.created_at)) / 86400, 0)::float8
		FROM product_reviews r LEFT JOIN users u ON u.id = r.user_id
		WHERE r.id::text LIKE '000000__-0000-4000-8000-0000000000__'`).
		Scan(&total, &nameless, &future, &beforeAccount, &spanDays); err != nil {
		t.Fatalf("read the seeded reviews: %v", err)
	}
	if total < 20 {
		t.Fatalf("found %d seeded reviews; the seed or this filter moved", total)
	}
	if nameless != 0 {
		t.Errorf("%d seeded reviews have no reviewer name and read as 匿名顧客", nameless)
	}
	if future != 0 {
		t.Errorf("%d seeded reviews are dated in the future", future)
	}
	if beforeAccount != 0 {
		t.Errorf("%d seeded reviews predate their reviewer's account", beforeAccount)
	}
	if spanDays < 30 {
		t.Errorf("the seeded reviews span %.1f days; they read as written at once", spanDays)
	}
}

func TestReviewValidation(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := activeSlug(t)

	long := strings.Repeat("字", catalog.MaxReviewBodyRunes+1)
	tests := []struct {
		name  string
		r     catalog.Review
		field string
	}{
		{"no rating", catalog.Review{Rating: 0, Body: "內容夠長了吧。"}, "rating"},
		{"rating above five", catalog.Review{Rating: 6, Body: "內容夠長了吧。"}, "rating"},
		{"rating below one", catalog.Review{Rating: -1, Body: "內容夠長了吧。"}, "rating"},
		{"empty body", catalog.Review{Rating: 5, Body: ""}, "body"},
		{"body too short", catalog.Review{Rating: 5, Body: "短"}, "body"},
		{"body too long", catalog.Review{Rating: 5, Body: long}, "body"},
		{"control character", catalog.Review{Rating: 5, Body: "內容\x00夾帶"}, "body"},
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
	receive(t, who, slug)
	atLimit := strings.Repeat("字", catalog.MaxReviewBodyRunes)
	if errs, err := s.AddReview(ctx, slug, who.String(), &catalog.Review{
		Rating: 3, Body: atLimit,
	}); err != nil || len(errs) > 0 {
		t.Errorf("a %d-rune review was refused: %v %v", catalog.MaxReviewBodyRunes, errs, err)
	}
}

func TestFeedbackCannotTargetAMissingOrInactiveProduct(t *testing.T) {
	ctx := t.Context()
	who := reviewer(t, "missing-feedback")
	missing := "missing-" + uuid.NewString()

	assertRefused := func(t *testing.T, s *catalog.Store, slug string) {
		t.Helper()
		if errs, err := s.AddReview(ctx, slug, who.String(), &catalog.Review{
			Rating: 5, Body: "這則評價不可以憑空消失。",
		}); !errors.Is(err, catalog.ErrNotFound) || len(errs) != 0 {
			t.Errorf("AddReview(%q) = %v, %v; want ErrNotFound", slug, errs, err)
		}
		if err := s.Ask(ctx, slug, who.String(), "這個商品還存在嗎？"); !errors.Is(err, catalog.ErrNotFound) {
			t.Errorf("Ask(%q) = %v; want ErrNotFound", slug, err)
		}
	}

	t.Run("missing", func(t *testing.T) {
		assertRefused(t, catalog.NewStore(pool, slog.New(slog.DiscardHandler)), missing)
	})
	t.Run("inactive", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		slug := activeSlug(t)
		receive(t, who, slug)
		store := catalog.NewStore(tx, slog.New(slog.DiscardHandler))
		if errs, err := store.AddReview(ctx, slug, who.String(), &catalog.Review{
			Rating: 4, Body: "商品下架之前已經留下這則評價。",
		}); err != nil || len(errs) != 0 {
			t.Fatalf("create prior review: %v, %v", errs, err)
		}
		if _, err := tx.Exec(ctx, `UPDATE products SET status = 'draft' WHERE slug = $1`, slug); err != nil {
			t.Fatalf("make product inactive: %v", err)
		}
		assertRefused(t, store, slug)

		form := url.Values{"rating": {"5"}, "body": {"第二次送出不該把 404 變成 500。"}}
		req := httptest.NewRequestWithContext(
			user.NewContext(ctx, user.User{ID: who.String()}),
			http.MethodPost, "/p/"+slug+"/reviews", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		catalog.NewHandler(store, slog.New(slog.DiscardHandler), "https://goen.example").Review(res, req)
		if res.Code != http.StatusNotFound {
			t.Errorf("reviewing an inactive product after a prior review returned %d, want 404", res.Code)
		}
		var reviews int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM product_reviews r JOIN products p ON p.id = r.product_id
			WHERE p.slug = $1 AND r.user_id = $2`, slug, who).Scan(&reviews); err != nil {
			t.Fatalf("count reviews: %v", err)
		}
		if reviews != 1 {
			t.Errorf("inactive repeat wrote %d reviews, want the original one", reviews)
		}
	})
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

func receive(t *testing.T, userID uuid.UUID, slug string) {
	t.Helper()
	buy(t, userID, slug)
	for _, status := range []string{"picking", "shipped", "delivered"} {
		if _, err := pool.Exec(t.Context(),
			`UPDATE orders SET fulfillment_status = $2 WHERE user_id = $1`, userID, status); err != nil {
			t.Fatalf("move the order to %s: %v", status, err)
		}
	}
}

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

func TestTheRestockConfirmationNamesOnlyTheAccountsOwnAddress(t *testing.T) {
	ctx := t.Context()
	vid, slug := soldOutVariant(t)
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	own := "own-" + waitingAddr(t)

	post := func(signedIn bool, addr, remote string) string {
		t.Helper()
		reqCtx := ctx
		if signedIn {
			reqCtx = user.NewContext(ctx, user.User{ID: reviewer(t, "notify").String(), Email: own})
		}
		form := url.Values{"email": {addr}, "variant": {vid.String()}}
		req := httptest.NewRequestWithContext(reqCtx, http.MethodPost, "/p/"+slug+"/notify",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		req.SetPathValue("slug", slug)
		res := httptest.NewRecorder()
		h.Notify(res, req)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", res.Code)
		}
		return res.Header().Get("Location")
	}

	if got := post(true, strings.ToUpper(own), "198.51.100.11:1"); !strings.Contains(got, "notify=account") {
		t.Errorf("own address, signed in: redirect %q does not say it was the account's", got)
	}
	got := post(true, "other-"+waitingAddr(t), "198.51.100.12:1")
	if !strings.Contains(got, "notify=1") || strings.Contains(got, "account") {
		t.Errorf("another address, signed in: redirect %q, want the generic outcome", got)
	}
	got = post(false, own, "198.51.100.13:1")
	if !strings.Contains(got, "notify=1") || strings.Contains(got, "account") {
		t.Errorf("signed out: redirect %q, want the generic outcome", got)
	}
}

func soldOutVariant(t *testing.T) (id uuid.UUID, slug string) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `
		SELECT pv.id, p.slug FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity <= pv.safety_stock
		ORDER BY pv.stock_quantity - pv.safety_stock
		LIMIT 1`).Scan(&id, &slug); err != nil {
		t.Fatalf("find variant: %v", err)
	}
	return id, slug
}

func TestRecommendationsComeFromTheProjection(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))

	// Two products bought together twice, which is the minimum that counts.
	a, b := twoProductsBoughtTogether(t, 2)

	view, err := s.Product(ctx, a, catalog.Selection{})
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

	after, loadErr := s.Product(ctx, a, catalog.Selection{})
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

func TestRecommendationsSurviveRetirementOfAPurchasedVariant(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	a, b := twoProductsBoughtTogether(t, 2)

	var productID, retiredVariant uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT p.id, pv.id
		FROM products p JOIN product_variants pv ON pv.product_id = p.id
		WHERE p.slug = $1
		ORDER BY pv.position, pv.id LIMIT 1`, a).Scan(&productID, &retiredVariant); err != nil {
		t.Fatalf("find purchased variant: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_variants (product_id, sku, price_cents, position)
		VALUES ($1, 'REC-' || upper(replace(gen_random_uuid()::text, '-', '')), 100000, 1)`,
		productID); err != nil {
		t.Fatalf("create surviving variant: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET is_active = false WHERE id = $1`, retiredVariant); err != nil {
		t.Fatalf("retire purchased variant: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	view, err := s.Product(ctx, a, catalog.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(view.AlsoBought) != 1 || view.AlsoBought[0].Slug != b {
		t.Fatalf("recommendations after variant retirement = %+v, want %q",
			view.AlsoBought, b)
	}
	var historicalLines int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_lines
		WHERE product_id = $1 AND variant_id = $2`, productID, retiredVariant).
		Scan(&historicalLines); err != nil {
		t.Fatalf("read historical lines: %v", err)
	}
	if historicalLines != 2 {
		t.Errorf("%d purchased lines retained product identity, want 2", historicalLines)
	}
}

func TestOneSharedOrderIsNotARecommendation(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	a, _ := twoProductsBoughtTogether(t, 1)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Product(ctx, a, catalog.Selection{})
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	// THREE unpaid orders: one would produce nothing whatever the committed
	// filter did, so a single order cannot tell the filter from the threshold.
	a, _ := twoProductsBoughtTogether(t, -3)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Product(ctx, a, catalog.Selection{})
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	a, b := twoProductsBoughtTogether(t, 2)

	if _, err := pool.Exec(ctx, `SELECT refresh_copurchases()`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	view, err := s.Product(ctx, a, catalog.Selection{})
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

	after, loadErr := s.Product(ctx, a, catalog.Selection{})
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
			SELECT $1, pv.id, pv.sku, p.name, 100000, 1, $3::integer FROM product_variants pv JOIN products p ON p.id = pv.product_id WHERE pv.id = $2`,
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	back := questions.NewStore(pool)
	slug := anyActiveProduct(t)
	customer := newCustomer(t)
	// Keep another administrator on the roster so changing the answer author's
	// current role exercises attribution without violating the last-admin rule.
	_ = newShopAuthor(t)
	staff := newShopAuthor(t)

	if err := s.Ask(ctx, slug, customer, "這台支援 PD 3.1 嗎?"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)

	// The customer answers first, or insertion order matches the intended
	// order by accident.
	if err := insertHistoricalCustomerAnswer(ctx, qID, customer, "我實測過可以。"); err != nil {
		t.Fatalf("customer answer: %v", err)
	}
	staffCtx := user.NewContext(ctx, user.User{ID: staff, Role: user.RoleAdmin})
	if err := back.AnswerQuestion(staffCtx, qID, staff, "支援,最高 45W。"); err != nil {
		t.Fatalf("staff answer: %v", err)
	}

	// The author is demoted to a plain customer AFTER writing both.
	if _, err := pool.Exec(ctx,
		`UPDATE users SET role = 'customer' WHERE id = $1`, uuid.MustParse(staff)); err != nil {
		t.Fatalf("demote: %v", err)
	}

	view, err := s.Product(ctx, slug, catalog.Selection{})
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

func TestAHiddenQuestionDisappearsWithItsAnswers(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "會被隱藏的問題"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	if err := insertHistoricalCustomerAnswer(ctx, qID, customer, "會一起消失的回答"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	before, err := s.Product(ctx, slug, catalog.Selection{})
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

	after, err := s.Product(ctx, slug, catalog.Selection{})
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

	staff := newShopAuthor(t)
	staffCtx := user.NewContext(ctx, user.User{ID: staff, Role: user.RoleAdmin})
	back := questions.NewStore(pool)
	if err := back.AnswerQuestion(staffCtx, qID, staff, "太遲了"); !errors.Is(err, questions.ErrNotFound) {
		t.Errorf("a hidden question's new answer = %v, want questions.ErrNotFound", err)
	}
}

// TestARejectedQuestionKeepsTheDraft holds the handler arm of the Q&A
// matrix: an over-limit signed-in POST answers 422 with the submitted body
// and the field error, not a redirect that empties the textarea.
func TestARejectedQuestionKeepsTheDraft(t *testing.T) {
	ctx := t.Context()
	slug := anyActiveProduct(t)
	customer := newCustomer(t)
	draft := strings.Repeat("問", catalog.MaxQuestionRunes+1)

	form := url.Values{"body": {draft}}
	req := httptest.NewRequestWithContext(
		user.NewContext(ctx, user.User{ID: customer}),
		http.MethodPost, "/p/"+slug+"/questions", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example").
		Ask(res, req)

	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected ask answered %d, want 422; Location=%q",
			res.Code, res.Header().Get("Location"))
	}
	if loc := res.Header().Get("Location"); loc != "" {
		t.Errorf("rejected ask redirected to %q; the draft is gone after a 303", loc)
	}
	body := res.Body.String()
	if !strings.Contains(body, draft) {
		t.Error("422 did not keep the submitted over-limit draft")
	}
	if !strings.Contains(body, `aria-invalid="true"`) {
		t.Error("422 did not mark the textarea invalid")
	}
}

// TestAnAskedQuestionIsStoredAndTheReaderReturnsToTheQuestions holds the
// success arm of the Q&A handler: a signed-in POST stores the question under
// the product and redirects to its questions.
func TestAnAskedQuestionIsStoredAndTheReaderReturnsToTheQuestions(t *testing.T) {
	slug := anyActiveProduct(t)
	customer := newCustomer(t)
	const question = "這個有附保固卡嗎？"

	form := url.Values{"body": {question}}
	req := httptest.NewRequestWithContext(
		user.NewContext(t.Context(), user.User{ID: customer}),
		http.MethodPost, "/p/"+slug+"/questions", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example").
		Ask(res, req)

	if res.Code != http.StatusSeeOther {
		t.Fatalf("Ask answered %d, want 303; body=%s", res.Code, res.Body.String())
	}
	if got, want := res.Header().Get("Location"), "/p/"+slug+"?ask=1#questions"; got != want {
		t.Errorf("Ask redirects to %q, want %q", got, want)
	}
	var stored int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM product_questions q JOIN products p ON p.id = q.product_id
		WHERE p.slug = $1 AND q.user_id = $2 AND q.body = $3`,
		slug, customer, question).Scan(&stored); err != nil {
		t.Fatalf("count stored questions: %v", err)
	}
	if stored != 1 {
		t.Errorf("%d stored questions from this customer on %s, want 1", stored, slug)
	}
}

func TestAQuestionIsBoundedInRunesNotBytes(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	// 300 Han characters: 900 bytes, and exactly the limit.
	atLimit := strings.Repeat("問", catalog.MaxQuestionRunes)
	if err := s.Ask(ctx, slug, customer, atLimit); err != nil {
		t.Errorf("a question of exactly %d characters was refused: %v",
			catalog.MaxQuestionRunes, err)
	}
	if err := s.Ask(ctx, slug, customer, atLimit+"問"); !errors.Is(err, catalog.ErrQuestionInvalid) {
		t.Errorf("a question one character over gave %v, want ErrQuestionInvalid", err)
	}
	for _, body := range []string{"", "   ", "\t\n"} {
		if err := s.Ask(ctx, slug, customer, body); !errors.Is(err, catalog.ErrQuestionInvalid) {
			t.Errorf("%q gave %v, want ErrQuestionInvalid", body, err)
		}
	}
}

func containsQuestion(qs []catalog.Question, body string) bool {
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := anyActiveProduct(t)
	customer := newCustomer(t)

	if err := s.Ask(ctx, slug, customer, "這題會留著"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	qID := latestQuestion(t)
	for _, body := range []string{"留下來的回答", "會被隱藏的回答"} {
		if err := insertHistoricalCustomerAnswer(ctx, qID, customer, body); err != nil {
			t.Fatalf("answer %q: %v", body, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		UPDATE product_answers SET hidden_at = now()
		WHERE question_id = $1 AND body = '會被隱藏的回答'`,
		uuid.MustParse(qID)); err != nil {
		t.Fatalf("hide answer: %v", err)
	}

	view, err := s.Product(ctx, slug, catalog.Selection{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var found *catalog.Question
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))

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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
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

	before, err := s.Product(ctx, slug, catalog.Selection{})
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

	after, err := s.Product(ctx, slug, catalog.Selection{})
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
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
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
	// Delivered, so that only the hidden review stands between them and the form.
	receive(t, userID, slug)

	if _, err := pool.Exec(ctx,
		`UPDATE product_reviews SET hidden_at = now() WHERE id = $1`, reviewID); err != nil {
		t.Fatalf("hide: %v", err)
	}

	standing, err := s.ReviewStanding(ctx, slug, userID.String())
	if err != nil {
		t.Fatalf("ReviewStanding: %v", err)
	}
	if standing == catalog.ReviewOpen {
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

// TestASimultaneousSecondReviewIsRefusedByName reaches the INSERT's own refusal,
// the mapping of product_reviews_author_key to ErrAlreadyReviewed: ReviewStanding
// answers first in every ordinary case, so only two racing submissions get there.
//
// The race is made deterministic rather than hoped for. T1 inserts the row and
// holds its transaction OPEN: under read committed the row is invisible, so
// ReviewStanding passes, and the second INSERT then blocks on the unique index until
// T1 commits. Two goroutines behind a start channel would finish microseconds
// apart and never overlap.
func TestASimultaneousSecondReviewIsRefusedByName(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	who := reviewer(t, "race")
	slug := activeSlug(t)
	receive(t, who, slug)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var holder int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
		t.Fatalf("read the holder's backend pid: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO product_reviews (product_id, user_id, rating, body, is_verified_purchase)
		SELECT p.id, $1, 5, '搶先寫進去的評價內容。', false
		FROM products p WHERE p.slug = $2`, who, slug); err != nil {
		t.Fatalf("hold the first review open: %v", err)
	}

	// The second submission sees nothing yet, so it gets past ReviewStanding and
	// then waits on the index.
	refused := make(chan error, 1)
	go func() {
		_, addErr := s.AddReview(context.WithoutCancel(ctx), slug, who.String(), &catalog.Review{
			Rating: 1, Body: "同時送出的第二則評價內容。",
		})
		refused <- addErr
	}()

	// Release T1 only once the second INSERT is waiting on T1's index entry. A
	// guessed pause lets a slow machine commit first; ReviewStanding then answers
	// and the INSERT's own refusal, the thing under test, is never reached.
	waitForInsertBehind(t, holder, "%name: CreateReview%", refused)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the first review: %v", err)
	}

	select {
	case addErr := <-refused:
		if !errors.Is(addErr, catalog.ErrAlreadyReviewed) {
			t.Errorf("a simultaneous second review gave %v, want ErrAlreadyReviewed — "+
				"the customer is shown a 500 instead of being told they have already reviewed it", addErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second review never returned")
	}
}

// waitForInsertBehind waits, up to a deadline, until a statement like pattern
// is waiting on a lock held by the backend holder. It stops the test if the
// statement's goroutine returns first (on done) or never gets there.
func waitForInsertBehind(t *testing.T, holder int32, pattern string, done <-chan error) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("%s returned before it waited on backend %d: %v", pattern, holder, err)
		case <-ticker.C:
			_, waiting, err := dbtest.Blocked(t.Context(), pool, dbtest.Match{QueryLike: pattern, BlockedBy: []int32{holder}})
			if err != nil {
				t.Fatalf("observe the lock wait: %v", err)
			}
			if waiting {
				return
			}
		case <-timer.C:
			t.Fatalf("%s never waited on backend %d", pattern, holder)
		}
	}
}

// TestLoadIgnoresThePagesOwnParameters holds the WIRING rather than the filter:
// OnlyOptionsOf has a unit test of its own, and Product's call to it needs one too.
// It drives the query the restock form's redirect produces, against a product
// that is in stock.
func TestLoadIgnoresThePagesOwnParameters(t *testing.T) {
	ctx := t.Context()
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	slug := activeSlug(t)

	clean, err := s.Product(ctx, slug, nil)
	if err != nil {
		t.Fatalf("load %s: %v", slug, err)
	}
	if !clean.SelectionOK {
		t.Fatalf("%s resolves no variant even with no parameters, so this proves nothing", slug)
	}

	// Exactly what POST /p/{slug}/notify redirects to, and what /compare links
	// back with.
	for _, sel := range []catalog.Selection{
		{"notify": "1"},
		{"ask": "1"},
		{"p": slug},
	} {
		got, loadErr := s.Product(ctx, slug, sel)
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

// Historical answers remain readable even without a customer reply endpoint.
func insertHistoricalCustomerAnswer(ctx context.Context, questionID, userID, body string) error {
	_, err := pool.Exec(ctx, `INSERT INTO product_answers (question_id,user_id,body,is_staff) VALUES ($1,$2,$3,false)`, uuid.MustParse(questionID), uuid.MustParse(userID), body)
	return err
}

// A refused restock request re-renders the product at 422 with the address as
// typed, and says which of two causes it was.
func TestARefusedRestockRequestKeepsTheAddressAndNamesItsCause(t *testing.T) {
	ctx := t.Context()
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	// The page's form posts to an action that carries the visitor's selection, so
	// the 422 lands on the variant the request named; a product with options
	// resolves no variant without it.
	selectionOf := func(variant uuid.UUID) string {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT o.name, v.value
			FROM variant_option_values vo
			JOIN product_options o ON o.id = vo.option_id
			JOIN product_option_values v ON v.id = vo.option_value_id
			WHERE vo.variant_id = $1`, variant)
		if err != nil {
			t.Fatalf("read the selection of %s: %v", variant, err)
		}
		defer rows.Close()
		q := url.Values{}
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				t.Fatalf("scan selection: %v", err)
			}
			q.Set(name, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read selection: %v", err)
		}
		return q.Encode()
	}
	notify := func(slug string, variant uuid.UUID, addr, remote string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{"email": {addr}, "variant": {variant.String()}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/p/"+slug+"/notify?"+selectionOf(variant),
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = remote
		req.SetPathValue("slug", slug)
		w := httptest.NewRecorder()
		h.Notify(w, req)
		return w
	}

	vid, slug := soldOutVariant(t)
	bad := notify(slug, vid, "shopper@gmail", "203.0.113.141:1000")
	if bad.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(bad.Body.String(), `value="shopper@gmail"`) ||
		!strings.Contains(bad.Body.String(), `id="notify-email-error"`) {
		t.Errorf("a mistyped address = %d; want 422 keeping the address with the email message", bad.Code)
	}

	var inStockID uuid.UUID
	var inStockSlug string
	if err := pool.QueryRow(ctx, `
		SELECT pv.id, p.slug
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE p.status = 'active' AND pv.is_active
		  AND pv.stock_quantity > pv.safety_stock
		LIMIT 1`).Scan(&inStockID, &inStockSlug); err != nil {
		t.Fatalf("find in-stock variant: %v", err)
	}
	back := notify(inStockSlug, inStockID, "shopper@gmail.com", "203.0.113.142:1000")
	if back.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(back.Body.String(), `id="notify-unavailable"`) ||
		strings.Contains(back.Body.String(), `id="notify-email-error"`) {
		t.Errorf("a variant back in stock = %d; want 422 with the unavailable message and not the email one", back.Code)
	}
}

// A product page offers comparison where its department does: phones inherit it
// from tech, and a book sits under a department that does not compare.
func TestAProductPageOffersComparisonWhereItsDepartmentDoes(t *testing.T) {
	ctx := t.Context()
	store := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	for slug, want := range map[string]bool{"pixelight-9-pro": true, "fernway-mountain-tea-seasons": false} {
		view, err := store.Product(ctx, slug, nil)
		if err != nil {
			t.Fatalf("load %s: %v", slug, err)
		}
		if view.Comparable != want {
			t.Errorf("%s: Comparable = %v, want %v", slug, view.Comparable, want)
		}
	}
}

func TestANotifyRequestWithNoOptionPickedIsRefusedWithTheirAddressKept(t *testing.T) {
	_, slug := soldOutVariant(t)
	h := catalog.NewHandler(catalog.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example")
	addr := "keep-" + waitingAddr(t)
	form := url.Values{"email": {addr}, "variant": {""}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/p/"+slug+"/notify",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "198.51.100.14:1"
	req.SetPathValue("slug", slug)
	res := httptest.NewRecorder()
	h.Notify(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", res.Code)
	}
	if body := res.Body.String(); !strings.Contains(body, `value="`+addr+`"`) {
		t.Errorf("the address %q was not kept", addr)
	}
}

func TestAOneColourProductIsBuyableOnceItsOtherChoiceIsPicked(t *testing.T) {
	var slug, option, value string
	if err := pool.QueryRow(t.Context(), `
		WITH counts AS (
			SELECT o.product_id, o.id AS option_id, o.name, count(DISTINCT vov.option_value_id) AS n
			FROM product_options o
			JOIN variant_option_values vov ON vov.option_id = o.id
			JOIN product_variants pv ON pv.id = vov.variant_id AND pv.is_active
			GROUP BY o.product_id, o.id, o.name
		)
		SELECT p.slug, m.name, v.value
		FROM products p
		JOIN counts m ON m.product_id = p.id AND m.n >= 2
		JOIN counts s ON s.product_id = p.id AND s.n = 1
		JOIN variant_option_values vov ON vov.option_id = m.option_id
		JOIN product_option_values v ON v.id = vov.option_value_id
		JOIN product_variants pv ON pv.id = vov.variant_id AND pv.is_active AND pv.stock_quantity > pv.safety_stock
		WHERE p.status = 'active'
		  AND (SELECT count(*) FROM product_options WHERE product_id = p.id) = 2
		LIMIT 1`).Scan(&slug, &option, &value); err != nil {
		t.Fatalf("find a product with one colour and a second choice: %v", err)
	}
	s := catalog.NewStore(pool, slog.New(slog.DiscardHandler))
	view, err := s.Product(t.Context(), slug, catalog.Selection{option: value})
	if err != nil {
		t.Fatalf("load %q: %v", slug, err)
	}
	if !view.Exact || !view.CanBuy() {
		t.Errorf("%s?%s=%s: Exact = %t, CanBuy = %t, want both: the single choice must be preselected", slug, option, value, view.Exact, view.CanBuy())
	}
}
