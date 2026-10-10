//go:build integration

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/account"
	"github.com/koopa0/goen/internal/admin/refunds"
	"github.com/koopa0/goen/internal/payment"
)

// TestUnstorableTextNeverReachesTheDatabase is the router against a real
// PostgreSQL, which refuses a NUL and bytes that are not UTF-8 in any text it
// is handed. Every route below passes a path value or a query value straight
// into a query, storefront and back office alike, and each answers 400 rather
// than the 500 that refusal becomes.
func TestUnstorableTextNeverReachesTheDatabase(t *testing.T) {
	ctx := t.Context()
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("build disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{
			StorePool: pool, Payments: gateway, BaseURL: "http://127.0.0.1",
		},
		BackOffice: BackOfficeConfig{
			AdminPool: pool, Payments: gateway, Refunder: refunds.NewRefunder(""),
		},
	}, slog.New(slog.DiscardHandler))

	staffEmail := "unstorable-" + uuid.NewString() + "@goen.invalid"
	var staffID uuid.UUID
	if err = pool.QueryRow(ctx, `INSERT INTO users (email, role) VALUES ($1, 'admin') RETURNING id`,
		staffEmail).Scan(&staffID); err != nil {
		t.Fatalf("create the staff account: %v", err)
	}
	token, err := account.NewToken()
	if err != nil {
		t.Fatalf("mint a session token: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, now() + interval '1 hour')`, account.HashToken(token), staffID); err != nil {
		t.Fatalf("open the staff session: %v", err)
	}

	for _, bad := range []string{"%E9", "%00"} {
		for _, target := range []string{
			"/search?q=" + bad,
			"/c/phones?brand=" + bad,
			"/c/" + bad,
			"/p/" + bad,
			"/s/" + bad,
			"/compare?p=" + bad,
			"/admin/customers?q=" + bad,
			"/admin/orders?q=" + bad,
			"/admin/warranty?q=" + bad,
			"/admin/products/" + bad,
		} {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
			//nolint:gosec // G124: the development session cookie a browser sends back
			req.AddCookie(&http.Cookie{Name: "goen_session", Value: token})
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Errorf("GET %s answered %d, want 400", target, res.Code)
			}
			if !strings.HasPrefix(res.Header().Get("Content-Type"), "text/html") || !strings.Contains(res.Body.String(), `class="notice__actions"`) {
				t.Errorf("GET %s did not render the shop's error page", target)
			}
		}
	}
}
