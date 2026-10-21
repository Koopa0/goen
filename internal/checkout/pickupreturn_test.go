package checkout

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/shipping/storemap"
)

// TestTheStoreMapReturnIsWorthNothingToWhoeverDrivesIt holds the one route
// exempt from the cross-origin defence to its contract: it reads a bounded
// form and nothing else, answers without setting any cookie or reading this
// browser's, and cannot be cached. Whether a store is honoured is decided at
// /checkout against the nonce this browser alone holds.
func TestTheStoreMapReturnIsWorthNothingToWhoeverDrivesIt(t *testing.T) {
	t.Parallel()
	h := NewHandler(&Store{}, &cart.Handler{}, &access.Store{}, slog.New(slog.DiscardHandler), true, nil, testMap(t))

	callback := url.Values{
		"MerchantID": {aMerchantID}, "MerchantTradeNo": {"ABCDEFGHIJ1234567890"},
		"LogisticsSubType": {"UNIMART"}, "CVSStoreID": {"131386"},
		"CVSStoreName": {"南港園區"}, "CVSAddress": {"台北市南港區三重路19-2號"},
		"ExtraData": {aNonce},
	}
	post := func(body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, storemap.ReturnPath,
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range cookies {
			req.AddCookie(c)
		}
		res := httptest.NewRecorder()
		h.PickupReturn(res, req)
		return res
	}

	t.Run("a body past the callback bound", func(t *testing.T) {
		t.Parallel()
		padded := url.Values{}
		for k, v := range callback {
			padded[k] = v
		}
		padded.Set("CVSOutSide", strings.Repeat("0", storemap.MaxCallbackBytes))
		if res := post(padded.Encode()); res.Code != http.StatusBadRequest {
			t.Errorf("an oversized callback answered %d, want 400", res.Code)
		}
	})
	t.Run("a body that is not a form", func(t *testing.T) {
		t.Parallel()
		if res := post("MerchantID=%zz&ExtraData=" + aNonce); res.Code != http.StatusBadRequest {
			t.Errorf("an unreadable callback answered %d, want 400", res.Code)
		}
	})
	t.Run("a well-formed callback", func(t *testing.T) {
		t.Parallel()
		bare := post(callback.Encode())
		if bare.Code != http.StatusOK {
			t.Fatalf("a well-formed callback answered %d, want 200", bare.Code)
		}
		if got := bare.Header().Values("Set-Cookie"); len(got) != 0 {
			t.Errorf("the callback set cookies %q; whoever drives it must not bind this browser", got)
		}
		if got := bare.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}

		// This browser's own pickup state changes nothing about the answer.
		held := &http.Cookie{Name: storemap.CookieNameFor(true), Value: "c3RhdGU"} //nolint:gosec // G124: a request cookie, as the browser sends it
		withCookie := post(callback.Encode(), held)
		if withCookie.Body.String() != bare.Body.String() ||
			len(withCookie.Header().Values("Set-Cookie")) != 0 {
			t.Error("the callback's answer depends on this browser's cookie")
		}
	})
}
