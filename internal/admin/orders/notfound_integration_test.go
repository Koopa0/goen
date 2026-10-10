//go:build integration

package orders_test

import (
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/campaigns"
	"github.com/koopa0/goen/internal/admin/customers"
	"github.com/koopa0/goen/internal/admin/products"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/media"
	"github.com/koopa0/goen/internal/user"
)

func TestMissingRecordsKeepVerifiedStaffInTheBackOffice(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	ac := access.New(log, func(*http.Request) (bool, error) { return true, nil })
	admintest.OrderDesk(admintest.OrderStore(pool, admintest.Refunder{}, nil, admintest.DisabledInvoiceWriter{})).Routes(mux, ac)
	admintest.ProductDesk(pool, products.NewStore(pool)).Routes(mux, ac)
	customers.NewHandler(customers.NewStore(pool), log).Routes(mux, ac)
	campaigns.NewHandler(campaigns.NewStore(pool), media.NewHandler(media.NewStore(pool), log), log).Routes(mux, ac)
	unverified := http.NewServeMux()
	unverifiedAC := access.New(log, func(*http.Request) (bool, error) { return false, nil })
	admintest.OrderDesk(admintest.OrderStore(pool, admintest.Refunder{}, nil, admintest.DisabledInvoiceWriter{})).Routes(unverified, unverifiedAC)
	admintest.ProductDesk(pool, products.NewStore(pool)).Routes(unverified, unverifiedAC)
	customers.NewHandler(customers.NewStore(pool), log).Routes(unverified, unverifiedAC)
	campaigns.NewHandler(campaigns.NewStore(pool), media.NewHandler(media.NewStore(pool), log), log).Routes(unverified, unverifiedAC)

	staffCtx, _ := admintest.StaffContext(t, pool)
	number := "GO-261005-999999"
	cases := []struct{ path, list, zh, en string }{
		{"/admin/orders/" + number, "/admin/orders", "找不到 " + number + " 這筆訂單", "Order " + number + " not found"},
		{"/admin/products/missing-" + uuid.NewString(), "/admin/products", "找不到這個商品", "Product not found"},
		{"/admin/customers/" + uuid.NewString(), "/admin/customers", "找不到這位顧客", "Customer not found"},
		{"/admin/campaigns/missing-" + uuid.NewString(), "/admin/campaigns", "找不到這個活動", "Campaign not found"},
	}
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, tc := range cases {
			t.Run(locale.Tag()+tc.list, func(t *testing.T) {
				ctx := i18n.WithLocale(staffCtx, locale)
				stepUpRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, tc.path, nil)
				stepUpResponse := httptest.NewRecorder()
				unverified.ServeHTTP(stepUpResponse, stepUpRequest)
				if stepUpResponse.Code != http.StatusSeeOther || stepUpResponse.Header().Get("Location") != "/admin/verify" || strings.Contains(stepUpResponse.Body.String(), "goen-admin__navlist") {
					t.Error("missing record bypasses the second factor")
				}
				for _, role := range []user.Role{user.RoleStaff, user.RoleAdmin} {
					req := httptest.NewRequestWithContext(user.NewContext(ctx, user.User{ID: uuid.NewString(), Role: role}), http.MethodGet, tc.path, nil)
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					body := res.Body.String()
					heading := tc.zh
					if locale == i18n.En {
						heading = tc.en
					}
					if res.Code != http.StatusNotFound || !strings.Contains(body, "goen-admin__navlist") || !strings.Contains(body, html.EscapeString(heading)) {
						t.Errorf("%s %s missing record: status=%d, admin navigation=%t, heading=%t", role, tc.list, res.Code, strings.Contains(body, "goen-admin__navlist"), strings.Contains(body, html.EscapeString(heading)))
					}
					if !strings.Contains(body, `class="goen-btn goen-btn--primary" href="`+tc.list+`"`) {
						t.Errorf("%s missing record has no primary return to %s", role, tc.list)
					}
					if strings.Contains(body, "goen-newsletter") || strings.Contains(body, "goen-header__search") {
						t.Error("missing record renders storefront chrome")
					}
					if tc.list == "/admin/orders" {
						input := admintest.InputElementByID(t, body, "order-search")
						if admintest.InputAttribute(t, input, "value") != number || !strings.Contains(body, `method="get" action="/admin/orders"`) {
							t.Error("missing order does not retain the number in a GET search")
						}
					}
				}
				for _, outsider := range []bool{false, true} {
					outsiderCtx := i18n.WithLocale(t.Context(), locale)
					if outsider {
						outsiderCtx = user.NewContext(outsiderCtx, user.User{ID: uuid.NewString(), Role: user.RoleCustomer})
					}
					req := httptest.NewRequestWithContext(outsiderCtx, http.MethodGet, tc.path, nil)
					res := httptest.NewRecorder()
					mux.ServeHTTP(res, req)
					want := httptest.NewRecorder()
					access.NotFound(want, req, log)
					if res.Code != http.StatusNotFound || res.Body.String() != want.Body.String() || strings.Contains(res.Body.String(), `href="/admin`) {
						t.Error("outsider no longer gets the unchanged storefront 404")
					}
				}
			})
		}
	}
}
