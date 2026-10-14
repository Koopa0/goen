//go:build integration

package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/twofactor"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/payment"
)

func TestSchemaOnlyShopRendersStorefrontAndBackOffice(t *testing.T) {
	ctx := t.Context()
	owner := dbtest.Pool(t)
	assertCounts := func(wantUsers int) {
		t.Helper()
		var products, orders, users int
		if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM products),
			(SELECT count(*) FROM orders), (SELECT count(*) FROM users)`).Scan(&products, &orders, &users); err != nil {
			t.Fatalf("count empty shop: %v", err)
		}
		if products != 0 || orders != 0 || users != wantUsers {
			t.Fatalf("products/orders/users = %d/%d/%d, want 0/0/%d", products, orders, users, wantUsers)
		}
	}
	assertCounts(0)
	if _, err := owner.Exec(ctx, `SELECT upsert_staff('you@example.com', '', 'admin')`); err != nil {
		t.Fatalf("create first owner: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	storePool, err := openPool(ctx, owner.Config().ConnString(), log)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(storePool.Close)
	adminPool, err := openAdminPool(ctx, owner.Config().ConnString(), log)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)
	var userID string
	if readErr := owner.QueryRow(ctx, `SELECT id FROM users WHERE email = 'you@example.com'`).Scan(&userID); readErr != nil {
		t.Fatalf("read first owner: %v", readErr)
	}
	token, err := account.NewStore(storePool).StartSession(ctx, userID, "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("start owner session: %v", err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	if verifyErr := twofactor.NewStore(adminPool, key).MarkVerified(ctx, account.HashToken(token)); verifyErr != nil {
		t.Fatalf("verify owner session: %v", verifyErr)
	}
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: storePool, Payments: gateway, BaseURL: "http://127.0.0.1"},
		BackOffice: BackOfficeConfig{AdminPool: adminPool, Payments: gateway, Refunder: gateway, TOTPKey: key},
	}, log)
	for _, locale := range []string{"en", "zh-Hant"} {
		for _, page := range []struct {
			path   string
			staff  bool
			status int
		}{
			{"/", false, http.StatusOK},
			{"/search?q=empty-shop", false, http.StatusOK},
			{"/deals", false, http.StatusOK},
			{"/cart", false, http.StatusOK},
			{"/signin", false, http.StatusOK},
			{"/forgot", false, http.StatusOK},
			{"/shipping", false, http.StatusOK},
			{"/returns", false, http.StatusOK},
			{"/payment", false, http.StatusOK},
			{"/c/empty-shop", false, http.StatusNotFound},
			{"/admin", true, http.StatusOK},
			{"/admin/orders", true, http.StatusOK},
			{"/admin/products", true, http.StatusOK},
			{"/admin/stock", true, http.StatusOK},
			{"/admin/returns", true, http.StatusOK},
			{"/admin/customers", true, http.StatusOK},
			{"/admin/reports", true, http.StatusOK},
		} {
			t.Run(locale+page.path, func(t *testing.T) {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, page.path, http.NoBody)
				req.Header.Set("Accept-Language", locale)
				if page.staff {
					req.Header.Set("Cookie", "goen_session="+token)
				}
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				if res.Code != page.status {
					t.Fatalf("status = %d, want %d; body: %s", res.Code, page.status, res.Body.String())
				}
				if !strings.Contains(res.Body.String(), "<!doctype html>") {
					t.Fatalf("response did not render an HTML document: %s", res.Body.String())
				}
			})
		}
	}
	assertCounts(1)
}
