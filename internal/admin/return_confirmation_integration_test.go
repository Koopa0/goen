//go:build integration

package admin_test

import (
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/returns"
)

func TestReturnDecisionHTTPRequiresConfirmation(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	for _, decision := range []string{"approved", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			var id uuid.UUID
			if decision == "approved" {
				now := time.Now()
				id = returnedOrderAtOn(t, pool, now.Add(-24*time.Hour), now)
			} else {
				id, _ = returnedOrder(t, 1)
			}
			form := url.Values{"decision": {decision}, "resolution": {"Recorded decision"}}
			post := func() *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+id.String()+"/decide", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				req.SetPathValue("id", id.String())
				w := httptest.NewRecorder()
				h.RequireStaff(h.Decide)(w, req)
				return w
			}
			w := post()
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `name="confirm"`) || !strings.Contains(w.Body.String(), `name="decision" value="`+decision+`"`) {
				t.Fatalf("decision did not render its confirmation: %d %s", w.Code, w.Body.String())
			}
			if status, n := returnPayoutOn(t, pool, id); status != "requested" || n != 0 {
				t.Fatalf("preview moved state/money: %s/%d", status, n)
			}
			form.Set("confirm", decision)
			w = post()
			if w.Code != http.StatusSeeOther {
				t.Fatalf("confirmed decision = %d %s", w.Code, w.Body.String())
			}
			wantRefunds := 0
			if decision == "approved" {
				wantRefunds = 1
			}
			if status, n := returnPayoutOn(t, pool, id); status != decision || n != wantRefunds {
				t.Fatalf("confirmed decision = %s/%d, want %s/%d", status, n, decision, wantRefunds)
			}
		})
	}
}

func TestReturnRejectionRequiresAReasonAtTheStoreBoundary(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	id, _ := returnedOrder(t, 1)
	err := s.Decide(ctx, id.String(), "rejected", " \t ", "", uuid.NullUUID{})
	refused, ok := errors.AsType[*admin.FormRefusalError](err)
	if !ok || refused.Kind != returns.RefuseRejectionReason || refused.Field != "resolution" {
		t.Fatalf("blank rejection = %v, want resolution/rejection_reason", err)
	}
	if status, n := returnPayoutOn(t, pool, id); status != "requested" || n != 0 {
		t.Fatalf("blank rejection moved state/money: %s/%d", status, n)
	}
}

// A confirmed rejection without a reason is refused where the operator is
// looking: 422 on the queue page, the reason field marked invalid and still
// holding what was typed, and the return untouched.
func TestReturnRejectionWithoutAReasonIsRefusedAtTheHandler(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	h := adminHandlerOver(pool, s)
	for name, blank := range map[string]string{"empty": "", "whitespace": " \t "} {
		t.Run(name, func(t *testing.T) {
			id, _ := returnedOrder(t, 1)
			form := url.Values{"decision": {"rejected"}, "confirm": {"rejected"}, "resolution": {blank}}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+id.String()+"/decide", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.RequireStaff(h.Decide)(w, req)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("blank rejection = %d, want 422: %s", w.Code, w.Body.String())
			}
			field := regexp.MustCompile(`<input[^>]*id="res-` + id.String() + `"[^>]*>`).FindString(w.Body.String())
			if field == "" {
				t.Fatalf("no reason field for the return in %s", w.Body.String())
			}
			if !strings.Contains(field, `aria-invalid="true"`) {
				t.Errorf("reason field is not marked invalid: %s", field)
			}
			if blank != "" && !strings.Contains(field, `value="`+html.EscapeString(blank)+`"`) {
				t.Errorf("reason field lost what was submitted: %s", field)
			}
			if status, n := returnPayoutOn(t, pool, id); status != "requested" || n != 0 {
				t.Errorf("blank rejection moved state/money: %s/%d", status, n)
			}
		})
	}
}
