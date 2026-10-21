package checkout

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/shipping/storemap"
)

// aMerchantID stands for whatever id the environment names. It is opaque to
// goen: the only thing done with it is equality against what the callback
// repeats, so a made-up one exercises the rule exactly as the real one would.
const aMerchantID = "1000001"

// aNonce is a nonce of the exact shape storemap.NewNonce produces.
const aNonce = "0123456789abcdef0123"

// testMap is a store map on the B2C contract, the one these tests exercise.
func testMap(t *testing.T) *storemap.Client {
	t.Helper()
	m, err := storemap.New(aMerchantID, string(storemap.ModeB2C), "", "https://goen.test")
	if err != nil {
		t.Fatalf("build the store map: %v", err)
	}
	return m
}

// encodeState is the pickup cookie's own encoding, for the cases that feed it
// rubbish.
func encodeState(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestPickupRefusalDiagnosticsDoNotExposeSelectionSecrets(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := &Handler{storeMap: testMap(t), log: slog.New(slog.NewJSONHandler(&logs, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/checkout?pickup_n="+aNonce+"&pickup_chain=seven_eleven&pickup_store_code=131386&pickup_store_name=private-store", http.NoBody)
	//nolint:gosec // G124: stale local cookie used to exercise refusal diagnostics
	req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: encodeState("fedcba98765432100000|a|b|c")})
	view := &View{}
	if got := h.applyReturnedStore(req, view); got != http.StatusUnprocessableEntity || !view.PickupRefused {
		t.Fatalf("unmatched selection = %d, refused=%v", got, view.PickupRefused)
	}
	for _, want := range []string{`"pickup_cookie_count":1`, `"nonce_valid":true`, `"nonce_matched":false`, `"chain_offered":true`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("diagnostics missing %s: %s", want, logs.String())
		}
	}
	for _, secret := range []string{aNonce, "fedcba98765432100000", "131386", "private-store"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("diagnostics disclosed selection data %q", secret)
		}
	}
}

func TestMissingPickupNonceIsNotLoggedAsAMatch(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := &Handler{storeMap: testMap(t), log: slog.New(slog.NewJSONHandler(&logs, nil))}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/checkout?pickup_chain=seven_eleven&pickup_store_code=131386", http.NoBody)
	//nolint:gosec // G124: local selection cookie without a matching return nonce
	req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: encodeState(aNonce + "|a|b|c")})
	view := &View{}
	if got := h.applyReturnedStore(req, view); got != http.StatusUnprocessableEntity {
		t.Fatalf("return without a nonce = %d, want 422", got)
	}
	if !strings.Contains(logs.String(), `"nonce_matched":false`) {
		t.Errorf("missing nonce reported as a match: %s", logs.String())
	}
}

func TestASingleCookieSurvivesAMismatchedNonce(t *testing.T) {
	t.Parallel()
	const other = "fedcba98765432100000"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/checkout?pickup_n="+other, http.NoBody)
	//nolint:gosec // G124: the browser's own pickup cookie
	req.AddCookie(&http.Cookie{Name: "goen_pickup", Value: encodeState(aNonce + "|ship-1|mobile_carrier|addr-1")})
	h := &Handler{storeMap: testMap(t), log: slog.New(slog.DiscardHandler)}

	got := h.checkoutChoices(req)
	if got.Ship != "ship-1" || got.Invoice != "mobile_carrier" || got.Address != "addr-1" {
		t.Errorf("a mismatched nonce hid the saved choices: %+v", got)
	}
	state, ok := storemap.ReadCookie(req, false)
	if !ok || state.Nonce != aNonce {
		t.Errorf("the shopper's own nonce was replaced: %+v/%v", state, ok)
	}
	if storemap.Honoured(storemap.PostedStore{Chain: "seven_eleven", Code: "131386", Nonce: other}, state, ok) {
		t.Error("a mismatched nonce was honoured")
	}
}
