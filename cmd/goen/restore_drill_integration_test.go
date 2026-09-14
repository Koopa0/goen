//go:build integration

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/payment"
)

func loadRestoreFixture(t *testing.T) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate restore drill test file")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	path := filepath.Join(root, "seed", "restore_fixture.sql")
	sql, err := os.ReadFile(path) //nolint:gosec // G304: path is anchored under the repository root
	if err != nil {
		t.Fatalf("read restore fixture: %v", err)
	}
	if _, err := pool.Exec(t.Context(), string(sql)); err != nil {
		t.Fatalf("load restore fixture: %v", err)
	}
}

func restoredRouter(t *testing.T) http.Handler {
	t.Helper()
	storePool, err := openPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(storePool.Close)

	adminPool, err := openAdminPool(t.Context(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	t.Cleanup(adminPool.Close)

	gateway, err := payment.NewGateway("", "", "http://127.0.0.1")
	if err != nil {
		t.Fatalf("open payment gateway: %v", err)
	}
	return newRouter(&RouterConfig{
		Pool:      storePool,
		AdminPool: adminPool,
		Payments:  gateway,
		Refunder:  admin.NewRefunder(""),
		BaseURL:   "http://127.0.0.1",
	}, slog.New(slog.DiscardHandler))
}

func TestRestoredApplicationServesCatalogMediaAndOrders(t *testing.T) {
	loadRestoreFixture(t)
	handler := restoredRouter(t)

	productReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p/pixelight-9-pro", http.NoBody)
	productRes := httptest.NewRecorder()
	handler.ServeHTTP(productRes, productReq)
	if productRes.Code != http.StatusOK {
		t.Fatalf("product page status = %d, want 200", productRes.Code)
	}
	body := productRes.Body.String()
	if !strings.Contains(body, "Pixelight 9 Pro 5G") {
		t.Fatalf("product page body missing the restored catalogue name")
	}

	var digest string
	if err := pool.QueryRow(t.Context(), `
		SELECT encode(sha256(decode('010203726573746f7265', 'hex')), 'hex')`).Scan(&digest); err != nil {
		t.Fatalf("read image digest: %v", err)
	}
	mediaReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/media/"+digest, http.NoBody)
	mediaRes := httptest.NewRecorder()
	handler.ServeHTTP(mediaRes, mediaReq)
	if mediaRes.Code != http.StatusOK {
		t.Fatalf("media status = %d, want 200", mediaRes.Code)
	}
	if got := mediaRes.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/") {
		t.Fatalf("media content-type = %q, want an image", got)
	}
	if _, err := io.ReadAll(mediaRes.Body); err != nil {
		t.Fatalf("read media body: %v", err)
	}

	store := cart.NewStore(pool)
	grantWriter := httptest.NewRecorder()
	grantReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if err := store.RememberOrder(t.Context(), grantWriter, grantReq, "GO-260914-000001", false); err != nil {
		t.Fatalf("grant customer order access: %v", err)
	}
	var placedCookie *http.Cookie
	for _, c := range grantWriter.Result().Cookies() {
		if c.Name == "goen_placed" {
			placedCookie = c
			break
		}
	}
	if placedCookie == nil {
		t.Fatal("RememberOrder set no goen_placed cookie")
	}
	orderReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/orders/GO-260914-000001", http.NoBody)
	orderReq.AddCookie(placedCookie)
	orderRes := httptest.NewRecorder()
	handler.ServeHTTP(orderRes, orderReq)
	if orderRes.Code != http.StatusOK {
		t.Fatalf("authorized customer order view status = %d, want 200", orderRes.Code)
	}
	if !strings.Contains(orderRes.Body.String(), "GO-260914-000001") {
		t.Fatal("customer order page did not name the restored order")
	}
}
