//go:build integration

package buyer_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/access"
	"github.com/koopa0/goen/internal/order/buyer"
)

func TestReturnLandingConfirmsOnlyTheFiledFlagAfterTheNativePOST(t *testing.T) {
	for _, locale := range i18n.Locales() {
		t.Run(locale.Tag(), func(t *testing.T) {
			ctx := i18n.WithLocale(t.Context(), locale)
			orderID, number, lines := shippedTwoLineOrder(t, 0)
			posted := postReasonThroughRoute(t, ctx, number, lines, "Returned item")
			if posted.Code != http.StatusSeeOther || posted.Header().Get("Location") != "/orders/"+number+"/return?filed=1" {
				t.Fatalf("native return POST = %d to %q, want its filed landing", posted.Code, posted.Header().Get("Location"))
			}
			app := returnsApplicationPool(t, "filed-landing-"+uuid.NewString())
			h := buyer.NewHandler(buyer.NewStore(app), access.NewStore(app, false), slog.New(slog.DiscardHandler))
			mux := http.NewServeMux()
			mux.HandleFunc("GET /orders/{number}/return", h.ReturnPage)
			for _, query := range []string{"", "?filed=0", "?filed=unknown", "?filed=1"} {
				req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/"+number+"/return"+query, http.NoBody)
				placedBy(t, req, number)
				res := httptest.NewRecorder()
				mux.ServeHTTP(res, req)
				if res.Code != http.StatusOK {
					t.Fatalf("filed landing = %d, want 200", res.Code)
				}
				doc, err := html.Parse(strings.NewReader(res.Body.String()))
				if err != nil {
					t.Fatal(err)
				}
				sent, inflight := 0, 0
				visitReturnNodes(doc, func(n *html.Node) {
					if n.Type != html.ElementNode {
						return
					}
					id, role := "", ""
					for _, attr := range n.Attr {
						if attr.Key == "id" {
							id = attr.Val
						}
						if attr.Key == "role" {
							role = attr.Val
						}
					}
					if id == "return-sent" && role == "status" {
						sent++
					}
					if id == "return-inflight" {
						inflight++
					}
				})
				wantSent, wantInflight := 0, 1
				if query == "?filed=1" {
					wantSent, wantInflight = 1, 0
				}
				if sent != wantSent || inflight != wantInflight || strings.Contains(res.Body.String(), i18n.T(ctx, i18n.KeyReturnRequestSent)) != (query == "?filed=1") {
					t.Errorf("filed query %q: sent=%d inflight=%d, want %d/%d with the sentence only on filed=1", query, sent, inflight, wantSent, wantInflight)
				}
				assertReturnRowCounts(t, orderID, 1, 1)
			}
		})
	}
}
