//go:build integration

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/payment"
)

// TestNewRouterReadyz: /readyz stays 200 "ready" while the admin pool is
// saturated and then closed, and answers 503 "storefront database unreachable"
// only when the store pool is closed, while /healthz still answers 200.
func TestNewRouterReadyz(t *testing.T) {
	store, err := openPool(t.Context(), pool.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(store.Close)
	admin, err := openAdminPool(t.Context(), pool.Config().ConnString(), quietLog)
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(admin.Close)
	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("disabled payment gateway: %v", err)
	}
	router := newRouter(&RouterConfig{
		Storefront: StorefrontConfig{StorePool: store, Payments: gateway, BaseURL: "http://127.0.0.1"},
		BackOffice: BackOfficeConfig{AdminPool: admin, Payments: gateway},
	}, slog.New(slog.DiscardHandler))
	check := func(path string, wantStatus int, wantBody string) {
		t.Helper()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if res.Code != wantStatus || res.Body.String() != wantBody {
			t.Errorf("GET %s = %d %q, want %d %q", path, res.Code, res.Body.String(), wantStatus, wantBody)
		}
	}
	check("/readyz", http.StatusOK, "ready\n")
	held := make([]*pgxpool.Conn, 0, int(admin.Config().MaxConns))
	release := func() {
		for _, conn := range held {
			conn.Release()
		}
		held = nil
	}
	t.Cleanup(release)
	for range int(admin.Config().MaxConns) {
		conn, acquireErr := admin.Acquire(t.Context())
		if acquireErr != nil {
			t.Fatalf("hold admin connection: %v", acquireErr)
		}
		held = append(held, conn)
	}
	check("/readyz", http.StatusOK, "ready\n")
	release()
	admin.Close()
	check("/readyz", http.StatusOK, "ready\n")
	store.Close()
	check("/readyz", http.StatusServiceUnavailable, "storefront database unreachable\n")
	check("/healthz", http.StatusOK, "ok\n")
}
