//go:build integration

package returns_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/admin/loyalty"
	"github.com/koopa0/goen/internal/i18n"
)

func TestCreditSourceLinkShowsOnlyItsReturnRequest(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	first, number, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)
	other := returnedOrderAtOn(t, pool, time.Now().Add(-24*time.Hour), time.Now())
	appPool := admintest.AdminRolePool(t, pool)
	h := handlerOver(storeOver(appPool, admintest.Refunder{}))
	for _, locale := range i18n.Locales() {
		req := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/returns?request="+first.String(), nil)
		res := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(h.Queue)(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "/admin/returns/"+first.String()+"/decide") || strings.Contains(res.Body.String(), "/admin/returns/"+other.String()+"/decide") {
			t.Errorf("%s source link = %d, does not isolate its return", locale.Tag(), res.Code)
		}
	}
	form := url.Values{"decision": {"approved"}, "resolution": {"credit refund"}, "confirm": {"approved"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+first.String()+"/decide", strings.NewReader(form.Encode()))
	req.SetPathValue("id", first.String())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Decide)(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("credit refund = %d %s", res.Code, res.Body.String())
	}
	var customerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT user_id FROM store_credit_accounts WHERE id = $1`, accountID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	view, err := loyalty.NewStore(appPool).CreditForCustomer(ctx, customerID.String())
	if err != nil {
		t.Fatal(err)
	}
	var spend, refund bool
	for _, row := range view.Rows {
		if row.AmountCents < 0 {
			spend = row.OrderNumber == number && row.SourceHref() == "/admin/orders/"+number && row.BalanceCents == 0
		}
		if row.ReturnID == first.String() {
			refund = row.OrderNumber == number && row.SourceHref() == "/admin/returns?request="+first.String() && row.BalanceCents == 200000 && row.ActorName != ""
		}
	}
	if !spend || !refund {
		t.Errorf("real spend and return credit lost their sources or balances: %+v", view.Rows)
	}
	credit := loyalty.NewHandler(loyalty.NewStore(appPool), slog.New(slog.DiscardHandler))
	for _, locale := range i18n.Locales() {
		get := httptest.NewRequestWithContext(i18n.WithLocale(ctx, locale), http.MethodGet, "/admin/credit?customer="+customerID.String(), nil)
		page := httptest.NewRecorder()
		admintest.BackOffice.RequireStaff(credit.Credit)(page, get)
		for _, href := range []string{"/admin/orders/" + number, "/admin/returns?request=" + first.String(), "/admin/customers/" + customerID.String()} {
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `href="`+href+`"`) {
				t.Errorf("%s credit page lost source %s: %d", locale.Tag(), href, page.Code)
			}
		}
	}
}
