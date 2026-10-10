//go:build integration

package payment_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/orderaccess"
	"github.com/koopa0/goen/internal/payment"
)

func TestDisabledPaymentStartOffersOrderAndContact(t *testing.T) {
	number, _ := order(t, 100000)
	gateway, err := payment.NewGateway("", "", "http://goen.example")
	if err != nil {
		t.Fatal(err)
	}
	h := payment.NewHandler(payment.NewStore(pool), gateway, orderaccess.NewStore(pool, false), slog.New(slog.DiscardHandler))
	for _, locale := range i18n.Locales() {
		r := httptest.NewRequestWithContext(i18n.WithLocale(t.Context(), locale), http.MethodPost, "/orders/"+number+"/pay", http.NoBody)
		r.SetPathValue("number", number)
		placedBy(t, r, number)
		w := httptest.NewRecorder()
		h.Start(w, r)
		_, actions, ok := strings.Cut(w.Body.String(), `class="notice__actions"`)
		if w.Code != http.StatusServiceUnavailable || !ok {
			t.Fatalf("disabled payment start: status=%d actions present=%t", w.Code, ok)
		}
		actions, _, _ = strings.Cut(actions, "</div>")
		_, href, _ := strings.Cut(actions, `href="`)
		href, _, _ = strings.Cut(href, `"`)
		if href != "/orders/"+number || !strings.Contains(actions, `href="/contact"`) || strings.Contains(actions, "/contact?") {
			t.Errorf("disabled payment actions=%s, want order and contact without prefilling", actions)
		}
	}
}
