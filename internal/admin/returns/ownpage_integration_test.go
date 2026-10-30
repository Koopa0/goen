//go:build integration

package returns_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin/admintest"
)

const ownPageFlag = `<input type="hidden" name="from" value="return">`

// postFromOwnPage posts form to a return's handler as a staff member, as the
// return's own page does: with the flag that says where it came from.
func postFromOwnPage(t *testing.T, handle http.HandlerFunc, id uuid.UUID, action string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	ctx, _ := admintest.StaffContext(t, pool)
	form.Set("from", "return")
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/returns/"+id.String()+"/"+action, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", id.String())
	res := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(handle)(res, req)
	return res
}

// TestHandler_OwnPageAnswers: a form posted from a return's own page answers
// back to that page, with the notice the queue would have shown, so the staff
// member stays on the return they acted on.
func TestHandler_OwnPageAnswers(t *testing.T) {
	h := handlerOver(storeOver(pool, admintest.HealthyGateway(t)))
	wantAnswer := func(t *testing.T, res *httptest.ResponseRecorder, notice string, id uuid.UUID) {
		t.Helper()
		want := "/admin/returns?" + notice + "=1&request=" + id.String()
		if res.Code != http.StatusSeeOther || res.Header().Get("Location") != want {
			t.Errorf("answer = %d %q, want 303 %q", res.Code, res.Header().Get("Location"), want)
		}
	}

	t.Run("a decision that took, then the inspection and the closing", func(t *testing.T) {
		now := time.Now()
		id := returnedOrderAtOn(t, pool, now.Add(-24*time.Hour), now)
		line := returnLineID(t, id).String()
		res := postFromOwnPage(t, h.Decide, id, "decide", url.Values{"decision": {"approved"}, "confirm": {"approved"}, "resolution": {"refund"}})
		wantAnswer(t, res, "approved", id)
		res = postFromOwnPage(t, h.Inspect, id, "inspect", url.Values{"received_" + line: {"2"}})
		wantAnswer(t, res, "inspected", id)
		res = postFromOwnPage(t, h.Complete, id, "complete", url.Values{"resolution": {"done"}})
		wantAnswer(t, res, "closed", id)
	})
	t.Run("a decision the database refused", func(t *testing.T) {
		id, _ := admintest.ReturnedOrder(t, pool, 1)
		res := postFromOwnPage(t, h.Decide, id, "decide", url.Values{"decision": {"approved"}, "confirm": {"approved"}, "resolution": {strings.Repeat("界", 301)}})
		wantAnswer(t, res, "refused", id)
	})
	t.Run("a decision still to be confirmed", func(t *testing.T) {
		id, _ := admintest.ReturnedOrder(t, pool, 1)
		res := postFromOwnPage(t, h.Decide, id, "decide", url.Values{"decision": {"approved"}})
		body := res.Body.String()
		for _, want := range []string{ownPageFlag, `href="/admin/returns?request=` + id.String() + `"`} {
			if res.Code != http.StatusOK || !strings.Contains(body, want) {
				t.Errorf("confirmation = %d, lacks %s", res.Code, want)
			}
		}
	})
	t.Run("an assessment", func(t *testing.T) {
		id := returnedOrderAtOn(t, pool, admintest.ShopNoonDaysAgo(t, 10), time.Now())
		line := returnLineID(t, id).String()
		res := postFromOwnPage(t, h.Assess, id, "assess", url.Values{
			"basis":               {"photos on the ticket"},
			"unused_" + line:      {"unknown"},
			"packaging_" + line:   {"unknown"},
			"accessories_" + line: {"unknown"},
		})
		wantAnswer(t, res, "assessed", id)
	})
}

// TestHandler_OwnPageRefusals: a refusal of a form posted from a return's own
// page is drawn on that page, not on the queue's first page, so the error and
// the drafts are where the staff member is looking and the page still posts
// back to itself.
func TestHandler_OwnPageRefusals(t *testing.T) {
	h := handlerOver(storeOver(pool, admintest.HealthyGateway(t)))
	// Any other open return is on the queue's page and must not be on the own
	// page of this one.
	other, _ := admintest.ReturnedOrder(t, pool, 1)
	awaiting := admintest.PreapprovedReturn(t, pool)
	open, _ := admintest.ReturnedOrder(t, pool, 1)
	awaitingLine := returnLineID(t, awaiting).String()

	tests := []struct {
		name   string
		handle http.HandlerFunc
		action string
		id     uuid.UUID
		form   url.Values
		kept   []string
	}{
		{
			name: "inspection", handle: h.Inspect, action: "inspect", id: awaiting,
			form: url.Values{"received_" + awaitingLine: {" +oops "}, "note_" + awaitingLine: {" kept note "}},
			kept: []string{`value=" +oops "`, `value=" kept note "`},
		},
		{
			name: "decision", handle: h.Decide, action: "decide", id: open,
			form: url.Values{"decision": {"rejected"}, "confirm": {"rejected"}, "resolution": {" \t "}},
			kept: []string{`aria-invalid="true"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := postFromOwnPage(t, tt.handle, tt.id, tt.action, tt.form)

			body := res.Body.String()
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("refusal = %d, want 422: %s", res.Code, body)
			}
			if !strings.Contains(body, "/admin/returns/"+tt.id.String()+"/"+tt.action) {
				t.Errorf("the refusal does not draw the return's own form")
			}
			if strings.Contains(body, "/admin/returns/"+other.String()+"/") {
				t.Errorf("the refusal draws the queue, with another return's forms, not the return's own page")
			}
			if !strings.Contains(body, ownPageFlag) {
				t.Errorf("the refused form no longer posts back to the return's own page")
			}
			for _, want := range tt.kept {
				if !strings.Contains(body, want) {
					t.Errorf("the refusal lost %s", want)
				}
			}
		})
	}
}

// TestHandler_QueueUnknownRequest: a return's own page for an id that names no
// return is not found, rather than a queue that reads as empty.
func TestHandler_QueueUnknownRequest(t *testing.T) {
	ctx, _ := admintest.StaffContext(t, pool)
	h := handlerOver(storeOver(pool, admintest.HealthyGateway(t)))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/returns?request="+uuid.NewString(), http.NoBody)
	res := httptest.NewRecorder()
	admintest.BackOffice.RequireStaff(h.Queue)(res, req)
	if res.Code != http.StatusNotFound {
		t.Errorf("own page of an unknown return = %d, want 404", res.Code)
	}
}
