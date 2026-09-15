//go:build integration

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/db/dbtest"
	"github.com/koopa0/goen/internal/restore"
)

func TestRestoredCopyApplicationServesAuthorizedViews(t *testing.T) {
	source := dbtest.Pool(t)
	ctx := t.Context()
	if err := loadRestoreFixtureOn(ctx, source); err != nil {
		t.Fatalf("load restore fixture: %v", err)
	}

	copyName := "restore_app_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	copyURL, _, cleanup, err := restore.DumpCopy(ctx, source.Config().ConnString(), copyName)
	if err != nil {
		t.Fatalf("dump and restore copy: %v", err)
	}
	defer cleanup()

	copyPool, err := pgxpool.New(ctx, copyURL)
	if err != nil {
		t.Fatalf("open copy pool: %v", err)
	}
	defer copyPool.Close()

	handler := restoredRouter(t, copyURL)

	productReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/p/pixelight-9-pro", http.NoBody)
	productRes := httptest.NewRecorder()
	handler.ServeHTTP(productRes, productReq)
	if productRes.Code != http.StatusOK {
		t.Fatalf("product page status = %d, want 200", productRes.Code)
	}
	if !strings.Contains(productRes.Body.String(), "Pixelight 9 Pro 5G") {
		t.Fatal("product page body missing the restored catalogue name")
	}

	var digest string
	if scanErr := copyPool.QueryRow(ctx, `
		SELECT encode(sha256(decode('010203726573746f7265', 'hex')), 'hex')`).Scan(&digest); scanErr != nil {
		t.Fatalf("read image digest: %v", scanErr)
	}
	mediaReq := httptest.NewRequestWithContext(ctx, http.MethodGet, "/media/"+digest, http.NoBody)
	mediaRes := httptest.NewRecorder()
	handler.ServeHTTP(mediaRes, mediaReq)
	if mediaRes.Code != http.StatusOK {
		t.Fatalf("media status = %d, want 200", mediaRes.Code)
	}
	if _, readErr := io.ReadAll(mediaRes.Body); readErr != nil {
		t.Fatalf("read media body: %v", readErr)
	}

	store := cart.NewStore(copyPool)
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

	adminStore := admin.NewStore(copyPool, admin.NewRefunder(""), nil, nil)
	order, err := adminStore.Order(ctx, "GO-260914-000001")
	if err != nil {
		t.Fatalf("operator order view: %v", err)
	}
	if order.Number != "GO-260914-000001" {
		t.Fatalf("operator order number = %q", order.Number)
	}

	returnQueue, err := adminStore.Returns(ctx)
	if err != nil {
		t.Fatalf("operator return queue: %v", err)
	}
	if len(returnQueue.Rows) == 0 {
		t.Fatal("operator return queue is empty on the restored copy")
	}
	found := false
	for _, row := range returnQueue.Rows {
		if row.OrderNumber == "GO-260914-000001" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("operator return queue did not list the restored open return")
	}
}
