//go:build integration

package cart_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/product"
	"github.com/koopa0/goen/internal/ratelimit"
)

func TestCancellingTheLastHoldQueuesRestockNotices(t *testing.T) {
	testLastHoldRestock(t, false)
}

func TestExpiringTheLastHoldQueuesRestockNotices(t *testing.T) {
	testLastHoldRestock(t, true)
}

func testLastHoldRestock(t *testing.T, expired bool) {
	t.Helper()
	ctx := t.Context()
	variant := freshVariant(t, "restock-last-hold")
	if _, err := pool.Exec(ctx,
		`UPDATE product_variants SET safety_stock = 9 WHERE id = $1`, variant); err != nil {
		t.Fatal(err)
	}
	age := -time.Hour
	if expired {
		age = time.Hour
	}
	orderID := heldOrder(t, variant, age, false)
	number := numberOf(t, orderID)
	if stock := stockOf(t, variant); stock != 9 {
		t.Fatalf("held shelf stock = %d, want safety stock 9", stock)
	}
	var slug string
	if err := pool.QueryRow(ctx, `
		SELECT p.slug FROM products p JOIN product_variants v ON v.product_id = p.id
		WHERE v.id = $1`, variant).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	appPool := storeRolePool(t)
	var role string
	if err := appPool.QueryRow(ctx, `SELECT current_user`).Scan(&role); err != nil || role != "store" {
		t.Fatalf("application role = %q, %v", role, err)
	}
	log := slog.New(slog.DiscardHandler)
	notices := product.NewHandler(product.NewStore(appPool), log, "https://goen.example")
	for _, locale := range i18n.Locales() {
		form := url.Values{"variant": {variant.String()}, "email": {locale.Tag() + "-waiting@example.com"}}
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodPost,
			"/p/"+slug+"/notify", strings.NewReader(form.Encode()))
		req.SetPathValue("slug", slug)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := httptest.NewRecorder()
		notices.Notify(res, req)
		if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "notify=1") {
			t.Fatalf("%s subscription = %d %s", locale.Tag(), res.Code, res.Header().Get("Location"))
		}
	}
	store := cart.NewStore(appPool)
	if expired {
		for pass := range 2 {
			if _, _, err := store.Sweep(ctx, log); err != nil {
				t.Fatalf("expiry sweep %d: %v", pass, err)
			}
		}
	} else {
		h := cart.NewHandler(store, log, false,
			ratelimit.New(ratelimit.Config{Every: time.Millisecond, Burst: 1000, TTL: time.Hour, MaxKeys: 1000}), nil, nil)
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/cancel", http.NoBody)
		req.SetPathValue("number", number)
		req.AddCookie(placedCookie(t, store, number))
		res := httptest.NewRecorder()
		h.CancelOrder(res, req)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("cancel = %d, body=%s", res.Code, res.Body.String())
		}
	}
	if stock := stockOf(t, variant); stock != 10 {
		t.Fatalf("released shelf stock = %d, want 10 above safety stock 9", stock)
	}
	var waiting, queued int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM stock_notifications
		WHERE variant_id = $1 AND notified_at IS NULL`, variant).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM outbox_messages m JOIN stock_notifications n ON n.id::text = m.dedupe_key
		WHERE n.variant_id = $1 AND m.topic = 'catalogue.restocked'`, variant).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	t.Logf("stock recovery accepted; stock=10 safety=9; unclaimed=%d restock_outbox=%d", waiting, queued)
	if waiting != 0 || queued != len(i18n.Locales()) {
		t.Fatalf("restock after release: unclaimed=%d queued=%d, want 0/%d", waiting, queued, len(i18n.Locales()))
	}
}
