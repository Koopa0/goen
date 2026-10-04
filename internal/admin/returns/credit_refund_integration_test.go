//go:build integration

package returns_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/admin/admintest"
)

// TestConfirmingTheApprovalOfACreditFundedReturnPaysItBackAsCredit holds the
// whole HTTP path for an order paid wholly with store credit: no card payment
// exists, so the approval must reach the customer as credit and not stall
// waiting for a provider.
func TestConfirmingTheApprovalOfACreditFundedReturnPaysItBackAsCredit(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(storeOver(pool, admintest.Refunder{}))
	requestID, _, accountID := admintest.CreditFundedReturn(t, pool, 2, 200000)

	form := url.Values{"decision": {"approved"}, "resolution": {"全額購物金"}, "confirm": {"approved"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", requestID.String())
	w := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Decide)(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("confirmed approval answered %d, want 303; body=%s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/admin/returns?ok=1"; got != want {
		t.Errorf("confirmed approval redirects to %q, want %q", got, want)
	}
	if status, refunds := returnPayoutOn(t, pool, requestID); status != "approved" || refunds != 0 {
		t.Errorf("after approval the return is %q with %d card refunds, want approved with 0", status, refunds)
	}
	if got := creditBalanceOf(t, accountID); got != 200000 {
		t.Errorf("credit balance after approval = %d, want 200000", got)
	}
}
