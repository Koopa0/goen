//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/payment"
)

func loadRestoreFixtureOn(ctx context.Context, pool *pgxpool.Pool) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("locate restore drill test file")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	path := filepath.Join(root, "seed", "restore_fixture.sql")
	sql, err := os.ReadFile(path) //nolint:gosec // G304: path is anchored under the repository root
	if err != nil {
		return err
	}
	_, execErr := pool.Exec(ctx, string(sql))
	return execErr
}

func restoredRouter(t *testing.T, databaseURL string) http.Handler {
	t.Helper()
	storePool, err := openPool(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open store pool: %v", err)
	}
	t.Cleanup(storePool.Close)

	adminPool, err := openAdminPool(t.Context(), databaseURL)
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
	source := dbtest.Pool(t)
	ctx := t.Context()
	if err := loadRestoreFixtureOn(ctx, source); err != nil {
		t.Fatalf("load restore fixture: %v", err)
	}

	handler := restoredRouter(t, source.Config().ConnString())

	productReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/pixelight-9-pro", http.NoBody)
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
	if scanErr := source.QueryRow(ctx, `
		SELECT encode(sha256(decode('010203726573746f7265', 'hex')), 'hex')`).Scan(&digest); scanErr != nil {
		t.Fatalf("read image digest: %v", scanErr)
	}
	mediaReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/media/"+digest, http.NoBody)
	mediaRes := httptest.NewRecorder()
	handler.ServeHTTP(mediaRes, mediaReq)
	if mediaRes.Code != http.StatusOK {
		t.Fatalf("media status = %d, want 200", mediaRes.Code)
	}
	if got := mediaRes.Header().Get("Content-Type"); !strings.HasPrefix(got, "image/") {
		t.Fatalf("media content-type = %q, want an image", got)
	}
	if _, readErr := io.ReadAll(mediaRes.Body); readErr != nil {
		t.Fatalf("read media body: %v", readErr)
	}

	store := cart.NewStore(source)
	grantWriter := httptest.NewRecorder()
	grantReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	if grantErr := store.RememberOrder(ctx, grantWriter, grantReq, "GO-260914-000001", false); grantErr != nil {
		t.Fatalf("grant customer order access: %v", grantErr)
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
	orderReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/GO-260914-000001", http.NoBody)
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
