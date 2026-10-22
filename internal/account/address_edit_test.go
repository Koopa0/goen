package account

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/user"
)

// TestHandler_EditAddressSignedOut: address edit and update require an account
// before they read any input.
func TestHandler_EditAddressSignedOut(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, handler := range []http.HandlerFunc{h.EditAddress, h.UpdateAddress} {
		res := httptest.NewRecorder()
		handler(res, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/account/addresses/update", strings.NewReader("address=forged")))
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/signin" {
			t.Errorf("signed-out address edit = %d to %q, want 303 to /signin", res.Code, res.Header().Get("Location"))
		}
	}
}

// TestHandler_EditAddressUnreadable: an address edit or update does not report
// success when the saved address cannot be read.
func TestHandler_EditAddressUnreadable(t *testing.T) {
	t.Parallel()
	dbPool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/nothing")
	if err != nil {
		t.Fatal(err)
	}
	dbPool.Close()
	h := NewHandler(NewStore(dbPool), slog.New(slog.DiscardHandler), false, false)
	ctx := user.NewContext(t.Context(), user.User{ID: "11111111-1111-4111-8111-111111111111"})
	for _, addressID := range []string{"22222222-2222-4222-8222-222222222222", "invalid-id"} {
		for _, handler := range []http.HandlerFunc{h.EditAddress, h.UpdateAddress} {
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/addresses/update", strings.NewReader(url.Values{"address": {addressID}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("address", addressID)
			res := httptest.NewRecorder()
			handler(res, req)
			want := http.StatusInternalServerError
			if addressID == "invalid-id" {
				want = http.StatusNotFound
			}
			if res.Code != want || res.Header().Get("Location") != "" {
				t.Errorf("unreadable address edit = %d to %q, want %d without a success redirect", res.Code, res.Header().Get("Location"), want)
			}
		}
	}
}
