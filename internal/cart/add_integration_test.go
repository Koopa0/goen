//go:build integration

package cart_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ratelimit"
	"github.com/koopa0/goen/internal/user"
)

// TestConcurrentSignedInFirstAddsShareOneOwnedCart holds an uncommitted owned
// row so both HTTP first-adds miss ForUser and wait on carts_one_per_user.
// Releasing that row lets one insert win; the loser must reread it, not 500.
func TestConcurrentSignedInFirstAddsShareOneOwnedCart(t *testing.T) {
	ctx := t.Context()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, role, full_name)
		VALUES ('first-add-' || gen_random_uuid() || '@goen.invalid', 'customer', '王小明')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("register: %v", err)
	}
	firstVariant, secondVariant, _ := variantsOf(t, "signed-in-first-add", 2)

	blocker, beginErr := pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin owned-cart blocker: %v", beginErr)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(ctx)) }()
	if _, holdErr := blocker.Exec(ctx,
		`INSERT INTO carts (digest, user_id) VALUES ($1, $2)`,
		bearer.Digest("blocker-"+userID.String()), userID); holdErr != nil {
		t.Fatalf("hold uncommitted owned cart: %v", holdErr)
	}

	suffix := uuid.NewString()[:8]
	firstName, secondName := "first-add-a-"+suffix, "first-add-b-"+suffix
	firstHandler := cart.NewHandler(cart.NewStore(applicationPool(t, firstName)), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)

	secondHandler := cart.NewHandler(cart.NewStore(applicationPool(t, secondName)), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)

	type addResult struct {
		code int
		body string
	}
	postAdd := func(h *cart.Handler, variant uuid.UUID, quantity string) addResult {
		form := url.Values{
			"variant":  {variant.String()},
			"quantity": {quantity},
			"back":     {"signed-in-first-add"},
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/cart/items",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(user.NewContext(req.Context(), user.User{
			ID: userID.String(), Role: user.RoleCustomer,
		}))
		res := httptest.NewRecorder()
		h.AddItem(res, req)
		return addResult{code: res.Code, body: res.Body.String()}
	}

	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	firstRes, secondRes := make(chan addResult, 1), make(chan addResult, 1)
	go func() {
		got := postAdd(firstHandler, firstVariant, "2")
		firstRes <- got
		if got.code >= 500 {
			firstDone <- fmt.Errorf("first add status = %d; body=%s", got.code, got.body)
			return
		}
		firstDone <- nil
	}()
	go func() {
		got := postAdd(secondHandler, secondVariant, "3")
		secondRes <- got
		if got.code >= 500 {
			secondDone <- fmt.Errorf("second add status = %d; body=%s", got.code, got.body)
			return
		}
		secondDone <- nil
	}()
	waitForApplicationLock(t, firstName, firstDone)
	waitForApplicationLock(t, secondName, secondDone)

	if releaseErr := blocker.Rollback(ctx); releaseErr != nil {
		t.Fatalf("release owned-cart blocker: %v", releaseErr)
	}

	if firstErr := <-firstDone; firstErr != nil {
		t.Errorf("first add: %v", firstErr)
	}
	if secondErr := <-secondDone; secondErr != nil {
		t.Errorf("second add: %v", secondErr)
	}
	first := <-firstRes
	second := <-secondRes
	if first.code != http.StatusSeeOther {
		t.Errorf("first add status = %d, want 303; body=%s", first.code, first.body)
	}
	if second.code != http.StatusSeeOther {
		t.Errorf("second add status = %d, want 303; body=%s", second.code, second.body)
	}

	var owned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM carts WHERE user_id = $1`, userID).
		Scan(&owned); err != nil {
		t.Fatalf("count owned carts: %v", err)
	}
	if owned != 1 {
		t.Fatalf("owned carts = %d, want 1", owned)
	}

	var cartID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM carts WHERE user_id = $1`, userID).
		Scan(&cartID); err != nil {
		t.Fatalf("read owned cart: %v", err)
	}
	rows, err := pool.Query(ctx,
		`SELECT variant_id, quantity FROM cart_items WHERE cart_id = $1`, cartID)
	if err != nil {
		t.Fatalf("read cart lines: %v", err)
	}
	defer rows.Close()
	got := map[uuid.UUID]int32{}
	for rows.Next() {
		var variantID uuid.UUID
		var quantity int32
		if err := rows.Scan(&variantID, &quantity); err != nil {
			t.Fatalf("scan cart line: %v", err)
		}
		got[variantID] = quantity
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate cart lines: %v", err)
	}
	if got[firstVariant] != 2 || got[secondVariant] != 3 || len(got) != 2 {
		t.Errorf("cart lines = %v, want %s×2 and %s×3", got, firstVariant, secondVariant)
	}
}

func TestAddToCartReturnsToTheChosenVariant(t *testing.T) {
	const slug = "nimbus-buds-pro"
	vid := nimbusVariant(t, "雲白")
	h := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)

	res := postAddToProduct(t, h, vid, slug, "1")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	loc, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if loc.Query().Get("added") != "added" {
		t.Errorf("redirect is %q, want added=added", loc)
	}
	if loc.Query().Get("顏色") != "雲白" {
		t.Errorf("redirect dropped the colour selection: %q", loc)
	}

	// Browsers keep the #buybox fragment locally; it is not part of the GET.
	follow := httptest.NewRequestWithContext(t.Context(), http.MethodGet, loc.RequestURI(), http.NoBody)
	follow.SetPathValue("slug", slug)
	pres := httptest.NewRecorder()
	product.NewHandler(product.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example").
		Detail(pres, follow)
	if pres.Code != http.StatusOK {
		t.Fatalf("follow-up GET answered %d", pres.Code)
	}
	body := pres.Body.String()
	if got, ok := hiddenInputValue(body, "variant"); !ok || got != vid.String() {
		t.Errorf("returned purchase variant = %q, present = %t, want %s", got, ok, vid)
	}
	choose := i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyChooseOptions)
	if strings.Contains(body, choose) {
		t.Error("the returned page forgot the chosen variant after a successful add")
	}
	if !strings.Contains(body, i18n.T(i18n.WithLocale(t.Context(), i18n.ZhHant), i18n.KeyAddedToCart)) {
		t.Error("the returned page shows no success confirmation")
	}
}

func TestAddToCartRefusalKeepsTheChosenVariant(t *testing.T) {
	const slug = "aurora-slate-11"
	vid := soldOutAuroraVariant(t)
	h := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)

	res := postAddToProduct(t, h, vid, slug, "1")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	loc, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	if loc.Query().Get("added") != "unavailable" {
		t.Errorf("redirect is %q, want added=unavailable", loc)
	}
	if loc.Query().Get("顏色") != "曙光金" || loc.Query().Get("容量") != "128GB" {
		t.Errorf("redirect dropped the sold-out selection: %q", loc)
	}
}

func TestAddToCartRejectsAMismatchedBackSlug(t *testing.T) {
	vid := variantOf(t, "nimbus-buds-pro", true)
	h := cart.NewHandler(cart.NewStore(pool), access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)

	res := postAddToProduct(t, h, vid, "pixelight-9-pro", "1")
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body=%s", res.Code, res.Body.String())
	}
	if loc := res.Header().Get("Location"); loc != "/cart" {
		t.Errorf("mismatched back redirected to %q, want /cart", loc)
	}
}

func postAddToProduct(
	t *testing.T, h *cart.Handler, variantID uuid.UUID, slug, quantity string,
) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{
		"variant":  {variantID.String()},
		"quantity": {quantity},
		"back":     {slug},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cart/items",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	h.AddItem(res, req)
	return res
}

func nimbusVariant(t *testing.T, colour string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `
		SELECT pv.id
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		JOIN variant_option_values vov ON vov.variant_id = pv.id
		JOIN product_option_values pov ON pov.id = vov.option_value_id
		WHERE p.slug = 'nimbus-buds-pro' AND pov.value = $1
		LIMIT 1`, colour).Scan(&id)
	if err != nil {
		t.Fatalf("find nimbus variant %q: %v", colour, err)
	}
	return id
}

func soldOutAuroraVariant(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(t.Context(), `
		SELECT pv.id
		FROM product_variants pv
		JOIN products p ON p.id = pv.product_id
		JOIN variant_option_values vov ON vov.variant_id = pv.id
		JOIN product_option_values pov ON pov.id = vov.option_value_id
		JOIN product_options po ON po.id = vov.option_id
		WHERE p.slug = 'aurora-slate-11'
		  AND po.name = '顏色' AND pov.value = '曙光金'
		  AND EXISTS (
		    SELECT 1 FROM variant_option_values v2
		    JOIN product_option_values p2 ON p2.id = v2.option_value_id
		    JOIN product_options o2 ON o2.id = v2.option_id
		    WHERE v2.variant_id = pv.id AND o2.name = '容量' AND p2.value = '128GB'
		  )
		LIMIT 1`).Scan(&id)
	if err != nil {
		t.Fatalf("find sold-out aurora variant: %v", err)
	}
	return id
}

func TestAddRefusesWhatCannotBeSold(t *testing.T) {
	s := cart.NewStore(pool)
	id := newCart(t, s)

	unsellable := variantOf(t, "meridian-book-14", false) // wholly sold out in the seed
	err := s.Add(t.Context(), id, unsellable, 1)
	if err == nil {
		t.Fatal("a sold-out variant was added to a cart")
	}
	if !errors.Is(err, cart.ErrUnavailable) {
		t.Errorf("refused with %v, want ErrUnavailable — the store must recognise this "+
			"before the write, not leave it to a constraint violation", err)
	}

	ok := variantOf(t, "pixelight-9-pro", true)
	if err := s.Add(t.Context(), id, ok, 1); err != nil {
		t.Fatalf("a sellable variant was refused: %v", err)
	}
}

func TestAddClampsToWhatCanBeSupplied(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)

	vid := freshVariant(t, "stockfix-1")
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx,
		`SELECT stock_quantity, safety_stock FROM product_variants WHERE id = $1`,
		vid).Scan(&wasStock, &wasSafety); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 5, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	// 3 can be sold: 5 on hand less the floor of 2.
	id := newCart(t, s)
	if err := s.Add(ctx, id, vid, 10); !errors.Is(err, cart.ErrQuantityAdjusted) {
		t.Fatalf("add over stock returned %v, want ErrQuantityAdjusted", err)
	}

	var stored int32
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		id, vid).Scan(&stored); err != nil {
		t.Fatalf("read line: %v", err)
	}
	if stored != 3 {
		t.Errorf("asked for 10 of a variant with 3 sellable, cart holds %d; want 3", stored)
	}

	// Repeat add also clamps to what can be supplied without exceeding stock.
	if err := s.Add(ctx, id, vid, 2); !errors.Is(err, cart.ErrQuantityAdjusted) {
		t.Fatalf("repeat add over stock returned %v, want ErrQuantityAdjusted", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
		id, vid).Scan(&stored); err != nil {
		t.Fatalf("read repeat add line: %v", err)
	}
	if stored != 3 {
		t.Errorf("repeat add on variant with 3 sellable resulted in %d; want 3", stored)
	}
}

func TestAddAdjustedShowsNoticeOnTheProductPage(t *testing.T) {
	ctx := t.Context()
	s := cart.NewStore(pool)
	h := cart.NewHandler(s, access.NewStore(pool, false), slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)

	vid := freshVariant(t, "stockfix-add-notice")
	var slug string
	var wasStock, wasSafety int32
	if err := pool.QueryRow(ctx, `
		SELECT p.slug, pv.stock_quantity, pv.safety_stock
		FROM product_variants pv JOIN products p ON p.id = pv.product_id
		WHERE pv.id = $1`, vid).Scan(&slug, &wasStock, &wasSafety); err != nil {
		t.Fatalf("read variant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), //nolint:usetesting // t.Context is already cancelled in Cleanup
			`UPDATE product_variants SET stock_quantity = $2, safety_stock = $3 WHERE id = $1`,
			vid, wasStock, wasSafety)
	})
	// 3 can be sold: 5 on hand less the floor of 2.
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET stock_quantity = 5, safety_stock = 2 WHERE id = $1`,
		vid); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	// Two adds: the first asks for more than can be sold, the second repeats an
	// add onto a line that already holds 2.
	for _, tc := range []struct {
		name     string
		seed     int32
		quantity string
	}{
		{"first add over stock", 0, "10"},
		{"repeat add over stock", 2, "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, token := newCartSession(t, s)
			if tc.seed > 0 {
				if _, err := pool.Exec(ctx,
					`INSERT INTO cart_items (cart_id, variant_id, quantity) VALUES ($1, $2, $3)`,
					id, vid, tc.seed); err != nil {
					t.Fatalf("seed line: %v", err)
				}
			}
			form := url.Values{
				"variant": {vid.String()}, "quantity": {tc.quantity}, "back": {slug},
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/cart/items",
				strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "goen_cart", Value: token}) //nolint:gosec // G124: the browser's own cart cookie, read back by this handler
			res := httptest.NewRecorder()
			h.AddItem(res, req)
			if res.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303 (an adjusted add is not a server error); body=%s",
					res.Code, res.Body.String())
			}
			loc, err := url.Parse(res.Header().Get("Location"))
			if err != nil {
				t.Fatalf("parse redirect: %v", err)
			}
			if got := loc.Query().Get("added"); got != "adjusted" {
				t.Errorf("redirect is %q, want added=adjusted", loc)
			}

			var stored int32
			if err := pool.QueryRow(ctx,
				`SELECT quantity FROM cart_items WHERE cart_id = $1 AND variant_id = $2`,
				id, vid).Scan(&stored); err != nil {
				t.Fatalf("read line: %v", err)
			}
			if stored != 3 {
				t.Errorf("cart holds %d, want 3 (what can be sold)", stored)
			}

			for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
				lctx := i18n.WithLocale(ctx, locale)
				follow := httptest.NewRequestWithContext(lctx, http.MethodGet, loc.RequestURI(), http.NoBody)
				follow.SetPathValue("slug", slug)
				pres := httptest.NewRecorder()
				product.NewHandler(product.NewStore(pool, slog.New(slog.DiscardHandler)), slog.New(slog.DiscardHandler), "https://goen.example").
					Detail(pres, follow)
				if pres.Code != http.StatusOK {
					t.Fatalf("%v: product page answered %d", locale, pres.Code)
				}
				if want := i18n.T(lctx, i18n.KeyAddAdjusted); !strings.Contains(pres.Body.String(), want) {
					t.Errorf("%v: product page does not show %q", locale, want)
				}
			}
		})
	}
}
