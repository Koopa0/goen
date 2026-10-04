package cart

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestMixedTaxCheckoutUsesTheCartExplanation(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/checkout", http.NoBody)
			response := httptest.NewRecorder()
			view := pages.CheckoutView{Cart: pages.CartView{Lines: []pages.CartLine{{Name: "Item", Quantity: 1, UnitCents: 100, Available: 1}}}}
			handler := &Handler{log: slog.New(slog.DiscardHandler)}
			handler.answerPlacement(response, request, uuid.Nil, &Address{}, &view, "", ErrMixedTaxTypes)
			if response.Code != http.StatusUnprocessableEntity {
				t.Errorf("mixed-tax checkout status=%d, want 422", response.Code)
			}
			if !strings.Contains(response.Body.String(), i18n.T(ctx, i18n.KeyCartMixedTaxTypes)) {
				t.Error("checkout lost the cart's mixed-tax explanation")
			}
		})
	}
}
