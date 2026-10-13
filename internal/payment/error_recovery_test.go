package payment

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
)

func TestInaccessiblePaymentOffersOrderLookup(t *testing.T) {
	t.Parallel()
	h := &Handler{access: &orderaccess.Store{}, log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodGet, "/orders/GO-260101-000001/pay", http.NoBody)
		r.SetPathValue("number", "GO-260101-000001")
		w := httptest.NewRecorder()
		h.Page(w, r)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
		assertPaymentRecovery(t, w.Body.String(), "/orders/find", "/signin")
	}
}

func TestPaymentFailuresKeepTheOrderWithinReach(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	for _, locale := range i18n.Locales() {
		for _, fail := range []http.HandlerFunc{h.paymentConflict, h.serverError} {
			r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/orders/GO-260101-000001/pay", http.NoBody)
			r.SetPathValue("number", "GO-260101-000001")
			w := httptest.NewRecorder()
			fail(w, r)
			assertPaymentRecovery(t, w.Body.String(), "/orders/GO-260101-000001", "/contact")
		}
	}
}

func assertPaymentRecovery(t *testing.T, body, primary, secondary string) {
	t.Helper()
	_, actions, ok := strings.Cut(body, `class="notice__actions"`)
	if !ok {
		t.Fatal("missing recovery actions")
	}
	actions, _, _ = strings.Cut(actions, "</div>")
	_, first, _ := strings.Cut(actions, `href="`)
	first, _, _ = strings.Cut(first, `"`)
	if first != primary || !strings.Contains(actions, `href="`+secondary+`"`) {
		t.Errorf("recovery actions = %s, want primary %s and secondary %s", actions, primary, secondary)
	}
}
