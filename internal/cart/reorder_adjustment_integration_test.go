//go:build integration

package cart_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/cart"
	"github.com/koopa0/goen/internal/i18n"
)

func TestReorderReportsAdjustedQuantities(t *testing.T) {
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, scenario := range []struct {
			name     string
			existing int32
			skipped  bool
		}{{"already full", 3, false}, {"normal", 1, false}, {"adjusted and unavailable", 3, true}} {
			t.Run(string(locale)+"/"+scenario.name, func(t *testing.T) {
				ctx := i18n.WithLocale(t.Context(), locale)
				s := cart.NewStore(storeRolePool(t))
				live, empty, _ := threeVariants(t, "reorder-adjustment")
				if _, err := pool.Exec(ctx, `SELECT record_inventory_movement($1,-7,'adjustment',$2,NULL,NULL,NULL)`, live, "reorder-cap:"+live.String()); err != nil {
					t.Fatal(err)
				}
				lines := map[uuid.UUID]int32{live: 2}
				if scenario.skipped {
					lines[empty] = 1
					emptyTheShelfFor(t, empty)
				}
				number := orderOfVariants(t, lines)
				basket, token := newCartSession(t, s)
				if err := s.Add(ctx, basket, live, scenario.existing); err != nil {
					t.Fatal(err)
				}
				h := cart.NewHandler(s, slog.New(slog.DiscardHandler), false, testLimiter(), nil, nil)
				request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/orders/"+number+"/reorder", http.NoBody)
				request.SetPathValue("number", number)
				request.AddCookie(placedCookie(t, s, number))
				cartCookie := &http.Cookie{Name: "goen_cart", Value: token} //nolint:gosec // G124: fixture browser cart cookie.
				request.AddCookie(cartCookie)
				result := httptest.NewRecorder()
				h.ReorderItems(result, request)
				if result.Code != http.StatusSeeOther {
					t.Fatalf("reorder status=%d body=%s", result.Code, result.Body.String())
				}
				location, parseErr := url.Parse(result.Header().Get("Location"))
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				adjusted := scenario.existing == 3
				if (location.Query().Get("qty") == "adjusted") != adjusted {
					t.Errorf("adjustment redirect=%s, want adjusted=%t", location, adjusted)
				}
				var quantity int32
				if err := pool.QueryRow(ctx, `SELECT quantity FROM cart_items WHERE cart_id=$1 AND variant_id=$2`, basket, live).Scan(&quantity); err != nil {
					t.Fatal(err)
				}
				if quantity != 3 {
					t.Errorf("reorder quantity=%d, want 3", quantity)
				}
				follow := httptest.NewRequestWithContext(ctx, http.MethodGet, location.String(), http.NoBody)
				follow.AddCookie(cartCookie)
				shown := httptest.NewRecorder()
				h.Page(shown, follow)
				if shown.Code != http.StatusOK {
					t.Fatalf("cart status=%d", shown.Code)
				}
				notice := fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderAll), 1)
				if adjusted {
					notice = i18n.T(ctx, i18n.KeyReorderAdjusted)
					if scenario.skipped {
						notice = fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderAdjustedPartial), 1)
					}
					if strings.Contains(shown.Body.String(), fmt.Sprintf(i18n.T(ctx, i18n.KeyReorderAll), 1)) {
						t.Error("adjusted reorder claims all quantities were added")
					}
				}
				if !strings.Contains(shown.Body.String(), notice) {
					t.Errorf("reorder notice missing %q", notice)
				}
			})
		}
	}
}
