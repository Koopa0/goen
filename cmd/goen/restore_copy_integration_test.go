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
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/account"
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

	operatorID := uuid.New()
	if _, insertErr := copyPool.Exec(ctx, `INSERT INTO users (id, email, role)
		VALUES ($1, 'restore-operator@example.com', 'admin')`, operatorID); insertErr != nil {
		t.Fatalf("create restored-copy operator: %v", insertErr)
	}
	accounts := account.NewStore(copyPool)
	operatorToken, err := accounts.StartSession(ctx, operatorID.String(), "restore drill", "127.0.0.1")
	if err != nil {
		t.Fatalf("start operator session: %v", err)
	}
	if _, verifyErr := copyPool.Exec(ctx, `UPDATE sessions SET totp_verified_at = now()
		WHERE token_hash = $1`, account.HashToken(operatorToken)); verifyErr != nil {
		t.Fatalf("verify owned operator session: %v", verifyErr)
	}
	customerToken, err := accounts.StartSession(ctx, "55555555-5555-4555-8555-555555555555", "restore drill", "127.0.0.1")
	if err != nil {
		t.Fatalf("start customer session: %v", err)
	}
	for _, path := range []string{"/admin/orders/GO-260914-000001", "/admin/returns"} {
		for _, viewer := range []struct {
			name, token string
			status      int
		}{
			{"operator", operatorToken, http.StatusOK},
			{"customer", customerToken, http.StatusNotFound},
			{"anonymous", "", http.StatusNotFound},
		} {
			t.Run(path+"/"+viewer.name, func(t *testing.T) {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
				if viewer.token != "" {
					req.AddCookie(&http.Cookie{Name: "goen_session", Value: viewer.token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
				}
				res := httptest.NewRecorder()
				handler.ServeHTTP(res, req)
				if res.Code != viewer.status {
					t.Fatalf("restored %s view status = %d, want %d", viewer.name, res.Code, viewer.status)
				}
				containsOrder := strings.Contains(restoredMainText(t, res.Body.String()), "GO-260914-000001")
				if containsOrder != (viewer.status == http.StatusOK) {
					t.Fatalf("restored order disclosure = %t for %s", containsOrder, viewer.name)
				}
			})
		}
	}
}

func restoredMainText(t *testing.T, body string) string {
	t.Helper()
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inMain bool) {
		inMain = inMain || node.Type == html.ElementNode && node.Data == "main"
		if inMain && node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inMain)
		}
	}
	walk(document, false)
	return text.String()
}
