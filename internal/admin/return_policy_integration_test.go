//go:build integration

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
	"github.com/koopa0/goen/internal/shoptime"
	"github.com/koopa0/goen/internal/ui/pages"
)

func TestReturnDecisionEnforcesAdvertisedPolicy(t *testing.T) {
	isolated := isolatedAdminSeedPool(t)
	ctx, _ := staffContextOn(t, isolated)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
	customer := returns.NewStore(isolated)

	t.Run("statutory blank reason cannot be rejected", func(t *testing.T) {
		delivered := mustRFC3339(t, "2026-01-01T07:00:00+08:00")
		requested := mustRFC3339(t, "2026-01-06T12:00:00+08:00")
		requestID := returnedOrderAtWithReasonOn(t, isolated, delivered, requested, "")

		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("window = %q, want within — shop_today would have aged this January filing", window)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "原因未填", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("statutory rejection = %v, want ErrRefused", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("after a refused rejection the return is %q with %d refunds, want requested/0",
				status, refunds)
		}
	})

	t.Run("statutory blank reason may be approved and paid", func(t *testing.T) {
		number, lineID := deliveredOrderAtOn(t, isolated, shopNoonDaysAgo(t, 3))
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open statutory blank-reason return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("window = %q, want within", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve statutory blank-reason return: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("approved statutory return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "statutory" {
			t.Errorf("entitlement = %q, want statutory", got)
		}
	})

	t.Run("handler missing_reason and ineligible posts cannot close a statutory request", func(t *testing.T) {
		delivered := mustRFC3339(t, "2026-01-01T07:00:00+08:00")
		requested := mustRFC3339(t, "2026-01-06T12:00:00+08:00")
		requestID := returnedOrderAtWithReasonOn(t, isolated, delivered, requested, "")
		h := adminHandlerOver(isolated, s)

		for _, ground := range []string{"missing_reason", "ineligible"} {
			form := url.Values{
				"decision":         {"rejected"},
				"resolution":       {ground},
				"rejection_ground": {ground},
			}
			req := httptest.NewRequestWithContext(ctx, http.MethodPost,
				"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetPathValue("id", requestID.String())
			res := httptest.NewRecorder()
			h.Decide(res, req)
			if res.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s form rejection = %d %q, want 422",
					ground, res.Code, res.Header().Get("Location"))
			}
			if !strings.Contains(res.Body.String(), "aria-invalid") &&
				!strings.Contains(res.Body.String(), "七日內") &&
				!strings.Contains(res.Body.String(), "seven days") {
				t.Errorf("%s form rejection body does not name the statutory refusal", ground)
			}
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("after forged-ground posts the return is %q with %d refunds, want requested/0",
				status, refunds)
		}
	})

	t.Run("day 8 10 and 14 unknown cannot pay", func(t *testing.T) {
		for _, days := range []int{8, 10, 14} {
			delivered := shopNoonDaysAgo(t, days)
			number, lineID := deliveredOrderAtOn(t, isolated, delivered)
			if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
				Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
			}); err != nil {
				t.Fatalf("open day-%d return: %v", days, err)
			}
			requestID := openReturnIDOn(t, isolated, number)
			if window := queueWindow(t, s, requestID); window != "goodwill" {
				t.Fatalf("day-%d window = %q, want goodwill", days, window)
			}
			if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
				t.Fatalf("approve day-%d without assessment = %v, want ErrRefused", days, err)
			}
			if err := s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
				t.Fatalf("reject day-%d without assessment = %v, want ErrRefused", days, err)
			}
			if err := s.Decide(ctx, requestID.String(), "exception", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
				t.Fatalf("exception day-%d without assessment = %v, want ErrRefused", days, err)
			}
			status, refunds := returnPayoutOn(t, isolated, requestID)
			if status != "requested" || refunds != 0 {
				t.Errorf("day-%d unknown left %q with %d refunds, want requested/0", days, status, refunds)
			}
		}
	})

	t.Run("day 10 partial assessment cannot except until every fact is known", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "used", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if err := s.Assess(ctx, requestID.String(), "opened but packaging unchecked", []admin.LineEligibility{{
			OrderLineID: lineID,
			Unused:      "unmet",
			Packaging:   "unknown",
			Accessories: "unknown",
		}}); err != nil {
			t.Fatalf("assess partial unmet: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "exception", "goodwill exception", "1", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("exception partial goodwill = %v, want ErrRefused", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("partial goodwill exception left %q with %d refunds, want requested/0", status, refunds)
		}
		if received, restocked := inspectionCountsOn(t, isolated, requestID); received || restocked {
			t.Errorf("partial goodwill exception wrote receive/restock = %t/%t, want neither", received, restocked)
		}
	})

	t.Run("mixed unknown goodwill and late cannot be excepted", func(t *testing.T) {
		requestID, goodwillLine, _ := mixedWindowReturnOn(t, isolated,
			shopNoonDaysAgo(t, 10),
			shopNoonDaysAgo(t, 20),
			time.Now(),
		)
		if window := queueWindow(t, s, requestID); window != "mixed" {
			t.Fatalf("window = %q, want mixed", window)
		}
		if err := s.Assess(ctx, requestID.String(), "photos pending", []admin.LineEligibility{{
			OrderLineID: goodwillLine,
			Unused:      "unknown",
			Packaging:   "unknown",
			Accessories: "unknown",
		}}); err != nil {
			t.Fatalf("assess unknown goodwill line: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "exception", "late line goodwill", "1", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("exception mixed unknown goodwill + late = %v, want ErrRefused", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("mixed unknown exception left %q with %d refunds, want requested/0", status, refunds)
		}
		if received, restocked := inspectionCountsOn(t, isolated, requestID); received || restocked {
			t.Errorf("mixed unknown exception wrote receive/restock = %t/%t, want neither", received, restocked)
		}
	})

	t.Run("day 10 fully assessed unmet may except and pay", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "used", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if err := s.Assess(ctx, requestID.String(), "opened the parcel", []admin.LineEligibility{{
			OrderLineID: lineID,
			Unused:      "unmet",
			Packaging:   "met",
			Accessories: "met",
		}}); err != nil {
			t.Fatalf("assess unmet: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "exception", "goodwill exception", "1", uuid.NullUUID{}); err != nil {
			t.Fatalf("exception fully assessed unmet goodwill: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("exception unmet goodwill is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "exception" {
			t.Errorf("exception unmet goodwill entitlement = %q, want exception", got)
		}
	})

	t.Run("day 10 unmet cannot record goodwill and may be rejected or excepted", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "used", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if err := s.Assess(ctx, requestID.String(), "opened the parcel", []admin.LineEligibility{{
			OrderLineID: lineID,
			Unused:      "unmet",
			Packaging:   "met",
			Accessories: "met",
		}}); err != nil {
			t.Fatalf("assess unmet: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "1", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("approve unmet goodwill = %v, want ErrRefused", err)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "used", "1", uuid.NullUUID{}); err != nil {
			t.Fatalf("reject unmet goodwill: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "rejected" || refunds != 0 {
			t.Errorf("rejected unmet goodwill is %q with %d refunds, want rejected/0", status, refunds)
		}
	})

	t.Run("day 10 unmet exception pays without goodwill", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "used", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if err := s.Assess(ctx, requestID.String(), "opened the parcel", []admin.LineEligibility{{
			OrderLineID: lineID,
			Unused:      "unmet",
			Packaging:   "met",
			Accessories: "met",
		}}); err != nil {
			t.Fatalf("assess unmet: %v", err)
		}
		if received, restocked := inspectionCountsOn(t, isolated, requestID); received || restocked {
			t.Fatalf("assessment wrote receive/restock = %t/%t, want neither", received, restocked)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Fatalf("assessment itself left %q/%d, want requested/0", status, refunds)
		}
		if err := s.Decide(ctx, requestID.String(), "exception", "used; staff exception", "1", uuid.NullUUID{}); err != nil {
			t.Fatalf("exception unmet goodwill: %v", err)
		}
		status, refunds = returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("excepted unmet goodwill is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "exception" {
			t.Errorf("excepted unmet entitlement = %q, want exception", got)
		}
	})

	t.Run("day 10 all met records goodwill and pays", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if err := s.Assess(ctx, requestID.String(), "photos of unused unit", []admin.LineEligibility{{
			OrderLineID: lineID,
			Unused:      "met",
			Packaging:   "met",
			Accessories: "met",
		}}); err != nil {
			t.Fatalf("assess met: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "1", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve met goodwill: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("day-10 met return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "goodwill" {
			t.Errorf("day-10 entitlement = %q, want goodwill", got)
		}
	})

	t.Run("day 15 approval must be an explicit exception", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 15)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-15 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		if window := queueWindow(t, s, requestID); window != "after" {
			t.Fatalf("window = %q, want after", window)
		}
		found := queueRow(t, s, requestID)
		if found.Rescission() {
			t.Error("a day-15 request still reads as statutory entitlement")
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "goodwill exception", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("policy-approve day-15 = %v, want ErrRefused", err)
		}
		if err := s.Decide(ctx, requestID.String(), "exception", "goodwill exception", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("exception-approve day-15: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("day-15 return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "exception" {
			t.Errorf("day-15 entitlement = %q, want exception", got)
		}
	})

	t.Run("unexplained exception stays open", func(t *testing.T) {
		for _, resolution := range []string{"", "   "} {
			delivered := shopNoonDaysAgo(t, 15)
			number, lineID := deliveredOrderAtOn(t, isolated, delivered)
			if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
				Reason: "", Lines: map[string]int32{lineID.String(): 1},
			}); err != nil {
				t.Fatalf("open day-15 return: %v", err)
			}
			requestID := openReturnIDOn(t, isolated, number)
			if err := s.Decide(ctx, requestID.String(), "exception", resolution, "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
				t.Fatalf("unexplained exception %q = %v, want ErrRefused", resolution, err)
			}
			status, refunds := returnPayoutOn(t, isolated, requestID)
			if status != "requested" || refunds != 0 {
				t.Errorf("unexplained exception left %q with %d refunds, want requested/0", status, refunds)
			}
			if received, restocked := inspectionCountsOn(t, isolated, requestID); received || restocked {
				t.Errorf("unexplained exception wrote receive/restock = %t/%t, want neither", received, restocked)
			}
		}
	})

	t.Run("handler unexplained exception is 422 and keeps the draft", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 15)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-15 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		h := adminHandlerOver(isolated, s)
		form := url.Values{"decision": {"exception"}, "resolution": {"   "}}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", requestID.String())
		res := httptest.NewRecorder()
		h.Decide(res, req)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("blank exception HTTP = %d, want 422", res.Code)
		}
		body := res.Body.String()
		if !strings.Contains(body, `aria-invalid="true"`) {
			t.Error("422 did not mark the resolution field")
		}
		if !strings.Contains(body, `value="   "`) {
			t.Error("422 dropped the typed whitespace reason")
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("handler blank exception left %q/%d, want requested/0", status, refunds)
		}
	})

	t.Run("approved exception retry does not demand a new reason", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 15)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-15 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		stalled := admin.NewStore(isolated, fakeRefunder{
			refundErr: errors.New("read tcp 1.2.3.4:443: i/o timeout"),
		}, nil, nil)
		if err := stalled.Decide(ctx, requestID.String(), "exception", "beyond 14 days", "", uuid.NullUUID{}); err == nil {
			t.Fatal("a timed-out exception payout was reported as complete")
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Fatalf("stalled exception is %q/%d, want approved/1", status, refunds)
		}
		healthy := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
		if err := healthy.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("retry approved exception payout without a new reason: %v", err)
		}
		status, refunds = returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("retried exception is %q/%d, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "exception" {
			t.Errorf("retried entitlement = %q, want exception", got)
		}
	})

	t.Run("undelivered keeps the existing decide path", func(t *testing.T) {
		requestID, _ := returnedOrderOn(t, isolated, 1)
		if window := queueWindow(t, s, requestID); window != "undelivered" {
			t.Fatalf("window = %q, want undelivered", window)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("reject undelivered return: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "rejected" || refunds != 0 {
			t.Errorf("undelivered rejection is %q with %d refunds, want rejected/0", status, refunds)
		}
	})

	t.Run("partial delivery still decides against the delivered line", func(t *testing.T) {
		requestID := partiallyDeliveredReturnOn(t, isolated,
			mustRFC3339(t, "2026-01-01T07:00:00+08:00"),
			mustRFC3339(t, "2026-01-06T12:00:00+08:00"),
		)
		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("partial-delivery window = %q, want within", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve partial-delivery return: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("partial-delivery return is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "statutory" {
			t.Errorf("partial-delivery entitlement = %q, want statutory", got)
		}
	})

	t.Run("mixed statutory and goodwill unknown stays open", func(t *testing.T) {
		requestID, statutoryLine, goodwillLine := mixedWindowReturnOn(t, isolated,
			shopNoonDaysAgo(t, 3),
			shopNoonDaysAgo(t, 10),
			time.Now(),
		)
		if window := queueWindow(t, s, requestID); window != "mixed" {
			t.Fatalf("mixed window = %q, want mixed", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("approve mixed unknown = %v, want ErrRefused", err)
		}
		if err := s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("reject mixed statutory = %v, want ErrRefused", err)
		}
		if err := s.Assess(ctx, requestID.String(), "photos of both parcels", []admin.LineEligibility{
			{OrderLineID: statutoryLine, Unused: "met", Packaging: "met", Accessories: "met"},
			{OrderLineID: goodwillLine, Unused: "met", Packaging: "met", Accessories: "met"},
		}); err != nil {
			t.Fatalf("assess mixed met: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "1", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve mixed all-met: %v", err)
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "approved" || refunds != 1 {
			t.Errorf("mixed all-met is %q with %d refunds, want approved/1", status, refunds)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "goodwill" {
			t.Errorf("mixed all-met entitlement = %q, want goodwill", got)
		}
	})

	t.Run("a later unrelated shipment does not reopen the returned line", func(t *testing.T) {
		requestID := returnWithLaterUnrelatedShipmentOn(t, isolated,
			mustRFC3339(t, "2026-01-01T07:00:00+08:00"),
			mustRFC3339(t, "2026-01-06T12:00:00+08:00"),
			mustRFC3339(t, "2026-02-01T07:00:00+08:00"),
		)
		if window := queueWindow(t, s, requestID); window != "within" {
			t.Fatalf("unrelated later shipment window = %q, want within", window)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "", uuid.NullUUID{}); err != nil {
			t.Fatalf("approve after unrelated later shipment: %v", err)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "statutory" {
			t.Errorf("entitlement after later shipment = %q, want statutory", got)
		}
	})

	t.Run("assessment then a newer version refuses the stale decide", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		met := []admin.LineEligibility{{
			OrderLineID: lineID, Unused: "met", Packaging: "met", Accessories: "met",
		}}
		if err := s.Assess(ctx, requestID.String(), "first look", met); err != nil {
			t.Fatalf("assess v1: %v", err)
		}
		if err := s.Assess(ctx, requestID.String(), "second look", met); err != nil {
			t.Fatalf("assess v2: %v", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "1", uuid.NullUUID{}); !errors.Is(err, admin.ErrRefused) {
			t.Fatalf("stale version decide = %v, want ErrRefused", err)
		}
		if err := s.Decide(ctx, requestID.String(), "approved", "", "2", uuid.NullUUID{}); err != nil {
			t.Fatalf("current version decide: %v", err)
		}
		if got := decisionClaimOn(t, isolated, requestID); got != "goodwill" {
			t.Errorf("entitlement after version freeze = %q, want goodwill", got)
		}
	})

	t.Run("handler assess then decide pays and keeps the draft on 422", func(t *testing.T) {
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAtOn(t, isolated, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnIDOn(t, isolated, number)
		h := adminHandlerOver(isolated, s)

		assess := url.Values{
			"basis":                          {"photos on the ticket"},
			"unused_" + lineID.String():      {"unknown"},
			"packaging_" + lineID.String():   {"unknown"},
			"accessories_" + lineID.String(): {"unknown"},
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/returns/"+requestID.String()+"/assess", strings.NewReader(assess.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", requestID.String())
		res := httptest.NewRecorder()
		h.Assess(res, req)
		if res.Code != http.StatusSeeOther ||
			res.Header().Get("Location") != "/admin/returns?assessed=1" {
			t.Fatalf("assess unknown = %d %q, want assessed redirect",
				res.Code, res.Header().Get("Location"))
		}

		decide := url.Values{
			"decision":           {"approved"},
			"assessment_version": {"1"},
		}
		req = httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(decide.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", requestID.String())
		res = httptest.NewRecorder()
		h.Decide(res, req)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("approve unknown facts = %d, want 422", res.Code)
		}
		body := res.Body.String()
		if !strings.Contains(body, `role="alert"`) {
			t.Error("422 did not announce the decision refusal")
		}
		if !strings.Contains(body, i18n.T(ctx, i18n.KeyAdminRetErrIncomplete)) {
			t.Error("422 hid the incomplete-assessment refusal")
		}
		status, refunds := returnPayoutOn(t, isolated, requestID)
		if status != "requested" || refunds != 0 {
			t.Errorf("unknown-approve left %q/%d, want requested/0", status, refunds)
		}
	})
}

func TestReviewClearedAssessmentBasisSurvivesRefusal(t *testing.T) {
	ctx, _ := staffContext(t)
	s := admin.NewStore(pool, fakeRefunder{}, nil, nil)
	customer := returns.NewStore(pool)

	const storedBasis = "old evidence withdrawn by staff"
	openDay10 := func(t *testing.T) (uuid.UUID, uuid.UUID) {
		t.Helper()
		delivered := shopNoonDaysAgo(t, 10)
		number, lineID := deliveredOrderAt(t, delivered)
		if err := customer.Open(ctx, number, uuid.NullUUID{}, &returns.Request{
			Reason: "box opened", Lines: map[string]int32{lineID.String(): 1},
		}); err != nil {
			t.Fatalf("open day-10 return: %v", err)
		}
		requestID := openReturnID(t, number)
		if err := s.Assess(ctx, requestID.String(), storedBasis, []admin.LineEligibility{{
			OrderLineID: lineID, Unused: "met", Packaging: "met", Accessories: "met",
		}}); err != nil {
			t.Fatalf("seed assessment: %v", err)
		}
		return requestID, lineID
	}

	postAssess := func(t *testing.T, h *admin.Handler, requestID, lineID uuid.UUID, basis string, unused string) *httptest.ResponseRecorder {
		t.Helper()
		form := url.Values{
			"basis":                          {basis},
			"unused_" + lineID.String():      {unused},
			"packaging_" + lineID.String():   {"met"},
			"accessories_" + lineID.String(): {"met"},
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/returns/"+requestID.String()+"/assess", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", requestID.String())
		res := httptest.NewRecorder()
		h.Assess(res, req)
		return res
	}

	assertRefusedBasis := func(t *testing.T, body string, requestID uuid.UUID, want string) {
		t.Helper()
		id := "basis-" + requestID.String()
		input := inputElementByID(t, body, id)
		if got := inputAttribute(t, input, "value"); got != want {
			t.Fatalf("refused assessment draft basis=%q invalid=%q; want submitted %q and invalid=true",
				got, inputAttribute(t, input, "aria-invalid"), want)
		}
		if inputAttribute(t, input, "aria-invalid") != "true" {
			t.Fatalf("basis %q was not marked aria-invalid", id)
		}
	}

	assertStoredBasis := func(t *testing.T, requestID uuid.UUID) {
		t.Helper()
		var persisted string
		if err := pool.QueryRow(t.Context(), `
			SELECT basis FROM return_eligibility_assessments
			WHERE return_request_id = $1 ORDER BY version DESC LIMIT 1`,
			requestID).Scan(&persisted); err != nil {
			t.Fatalf("read stored basis: %v", err)
		}
		if persisted != storedBasis {
			t.Fatalf("stored basis = %q, want unchanged %q", persisted, storedBasis)
		}
	}

	t.Run("blank basis clearing", func(t *testing.T) {
		requestID, lineID := openDay10(t)
		h := adminHandlerOver(pool, s)
		res := postAssess(t, h, requestID, lineID, "", "unmet")
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("cleared basis assess = %d, want 422", res.Code)
		}
		assertRefusedBasis(t, res.Body.String(), requestID, "")
		assertStoredBasis(t, requestID)
	})

	t.Run("whitespace basis clearing", func(t *testing.T) {
		requestID, lineID := openDay10(t)
		h := adminHandlerOver(pool, s)
		res := postAssess(t, h, requestID, lineID, "   ", "unmet")
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("whitespace basis assess = %d, want 422", res.Code)
		}
		assertRefusedBasis(t, res.Body.String(), requestID, "   ")
		assertStoredBasis(t, requestID)
	})

	t.Run("nonempty basis edit", func(t *testing.T) {
		requestID, lineID := openDay10(t)
		h := adminHandlerOver(pool, s)
		edited := strings.Repeat("x", 501)
		res := postAssess(t, h, requestID, lineID, edited, "unmet")
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("edited basis assess = %d, want 422", res.Code)
		}
		assertRefusedBasis(t, res.Body.String(), requestID, edited)
		assertStoredBasis(t, requestID)
	})

	t.Run("decision-only refusal keeps saved assessment", func(t *testing.T) {
		requestID, lineID := openDay10(t)
		if err := s.Assess(ctx, requestID.String(), storedBasis, []admin.LineEligibility{{
			OrderLineID: lineID, Unused: "unknown", Packaging: "unknown", Accessories: "unknown",
		}}); err != nil {
			t.Fatalf("reassess unknown: %v", err)
		}
		h := adminHandlerOver(pool, s)
		decide := url.Values{
			"decision":           {"approved"},
			"assessment_version": {"2"},
		}
		req := httptest.NewRequestWithContext(ctx, http.MethodPost,
			"/admin/returns/"+requestID.String()+"/decide", strings.NewReader(decide.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", requestID.String())
		res := httptest.NewRecorder()
		h.Decide(res, req)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("approve without reassess = %d, want 422", res.Code)
		}
		body := res.Body.String()
		id := "basis-" + requestID.String()
		input := inputElementByID(t, body, id)
		if got := inputAttribute(t, input, "value"); got != storedBasis {
			t.Fatalf("decision-only refusal basis = %q, want saved %q", got, storedBasis)
		}
		if inputAttribute(t, input, "aria-invalid") == "true" {
			t.Fatalf("decision-only refusal marked basis %q invalid", id)
		}
	})
}

func TestTwoStaffCannotBothRejectAStatutoryRequest(t *testing.T) {
	isolated := isolatedAdminSeedPool(t)
	ctx, _ := staffContextOn(t, isolated)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
	requestID := returnedOrderAtWithReasonOn(t, isolated,
		mustRFC3339(t, "2026-01-01T07:00:00+08:00"),
		mustRFC3339(t, "2026-01-06T12:00:00+08:00"),
		"",
	)

	errc := make(chan error, 2)
	for range 2 {
		go func() {
			errc <- s.Decide(ctx, requestID.String(), "rejected", "", "", uuid.NullUUID{})
		}()
	}
	for range 2 {
		if err := <-errc; !errors.Is(err, admin.ErrRefused) {
			t.Errorf("concurrent statutory rejection = %v, want ErrRefused", err)
		}
	}
	status, refunds := returnPayoutOn(t, isolated, requestID)
	if status != "requested" || refunds != 0 {
		t.Errorf("after two refused rejections the return is %q with %d refunds, want requested/0",
			status, refunds)
	}
}

func TestTwoStaffStillSerialiseALateException(t *testing.T) {
	isolated := isolatedAdminSeedPool(t)
	ctx, _ := staffContextOn(t, isolated)
	s := admin.NewStore(isolated, fakeRefunder{}, nil, nil)
	requestID := returnedOrderAtWithReasonOn(t, isolated,
		mustRFC3339(t, "2026-01-01T12:00:00+08:00"),
		mustRFC3339(t, "2026-01-16T12:00:00+08:00"),
		"",
	)
	if window := queueWindow(t, s, requestID); window != "after" {
		t.Fatalf("window = %q, want after", window)
	}

	errc := make(chan error, 2)
	for range 2 {
		go func() {
			errc <- s.Decide(ctx, requestID.String(), "exception", "exception", "", uuid.NullUUID{})
		}()
	}
	var won, lost int
	for range 2 {
		err := <-errc
		switch {
		case err == nil:
			won++
		case errors.Is(err, admin.ErrRefused):
			lost++
		default:
			t.Errorf("late concurrent exception = %v, want nil or ErrRefused", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Errorf("late concurrent exception won=%d lost=%d, want 1/1", won, lost)
	}
	status, refunds := returnPayoutOn(t, isolated, requestID)
	if status != "approved" || refunds != 1 {
		t.Errorf("late concurrent exception is %q with %d refunds, want approved/1", status, refunds)
	}
	if got := decisionClaimOn(t, isolated, requestID); got != "exception" {
		t.Errorf("late concurrent entitlement = %q, want exception", got)
	}
}

func mustRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse %s: %v", value, err)
	}
	return ts
}

func shopNoonDaysAgo(t *testing.T, days int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(shoptime.Zone)
	if err != nil {
		t.Fatalf("load %s: %v", shoptime.Zone, err)
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -days)
}

func openReturnID(t *testing.T, number string) uuid.UUID {
	t.Helper()
	return openReturnIDOn(t, pool, number)
}

func openReturnIDOn(t *testing.T, p *pgxpool.Pool, number string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := p.QueryRow(t.Context(), `
		SELECT r.id FROM return_requests r
		JOIN orders o ON o.id = r.order_id
		WHERE o.order_number = $1 AND r.status = 'requested'`, number).Scan(&id); err != nil {
		t.Fatalf("find open return for %s: %v", number, err)
	}
	return id
}

func queueRow(t *testing.T, s *admin.Store, requestID uuid.UUID) pages.AdminReturn {
	t.Helper()
	view, err := s.Returns(t.Context())
	if err != nil {
		t.Fatalf("read the queue: %v", err)
	}
	for i := range view.Rows {
		if view.Rows[i].ID == requestID.String() {
			return view.Rows[i]
		}
	}
	t.Fatalf("return %s is not in the queue", requestID)
	return pages.AdminReturn{}
}

func queueWindow(t *testing.T, s *admin.Store, requestID uuid.UUID) string {
	t.Helper()
	return queueRow(t, s, requestID).Window
}

func returnPayoutOn(t *testing.T, p *pgxpool.Pool, requestID uuid.UUID) (status string, refunds int) {
	t.Helper()
	if err := p.QueryRow(t.Context(),
		`SELECT status FROM return_requests WHERE id = $1`, requestID).Scan(&status); err != nil {
		t.Fatalf("read return status: %v", err)
	}
	if err := p.QueryRow(t.Context(),
		`SELECT count(*) FROM refunds WHERE return_request_id = $1`, requestID).Scan(&refunds); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return status, refunds
}

func inspectionCountsOn(t *testing.T, p *pgxpool.Pool, requestID uuid.UUID) (received, restocked bool) {
	t.Helper()
	var receivedN, restockedN int
	if err := p.QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE received_quantity IS NOT NULL),
		       count(*) FILTER (WHERE restocked_quantity IS NOT NULL AND restocked_quantity > 0)
		FROM return_request_lines
		WHERE return_request_id = $1`, requestID).Scan(&receivedN, &restockedN); err != nil {
		t.Fatalf("read inspection counts: %v", err)
	}
	return receivedN > 0, restockedN > 0
}

func mixedWindowReturnOn(t *testing.T, p *pgxpool.Pool, statutoryDelivered, goodwillDelivered, requested time.Time) (
	requestID, statutoryLine, goodwillLine uuid.UUID,
) {
	t.Helper()
	ctx := t.Context()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		VALUES ($1, $2, '法定視窗', 50000, 1, 0) RETURNING id`,
		orderID, "MIX-STAT-"+uuid.NewString()[:8]).Scan(&statutoryLine); err != nil {
		t.Fatalf("create statutory line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
		VALUES ($1, $2, '優惠視窗', 50000, 1, 1) RETURNING id`,
		orderID, "MIX-GOOD-"+uuid.NewString()[:8]).Scan(&goodwillLine); err != nil {
		t.Fatalf("create goodwill line: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'mixed-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_mixed_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 100000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 100000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)

	var firstShipment, secondShipment uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, '黑貓', 'T-MIX-S-' || $2, $3, $4)
		RETURNING id`, orderID, number, statutoryDelivered.Add(-48*time.Hour), statutoryDelivered).
		Scan(&firstShipment); err != nil {
		t.Fatalf("create statutory parcel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, firstShipment, statutoryLine); err != nil {
		t.Fatalf("ship statutory line: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, '黑貓', 'T-MIX-G-' || $2, $3, $4)
		RETURNING id`, orderID, number, goodwillDelivered.Add(-48*time.Hour), goodwillDelivered).
		Scan(&secondShipment); err != nil {
		t.Fatalf("create goodwill parcel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, secondShipment, goodwillLine); err != nil {
		t.Fatalf("ship goodwill line: %v", err)
	}

	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason, created_at)
		VALUES ($1, 'mixed', $2) RETURNING id`, orderID, requested).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1), ($1, $2, $4, 1)`,
		orderID, requestID, statutoryLine, goodwillLine); err != nil {
		t.Fatalf("create return lines: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID, statutoryLine, goodwillLine
}

func decisionClaimOn(t *testing.T, p *pgxpool.Pool, requestID uuid.UUID) string {
	t.Helper()
	var entitlement string
	if err := p.QueryRow(t.Context(), `
		SELECT coalesce("after"->>'entitlement', '')
		FROM audit_events
		WHERE action = 'return.decide' AND entity_id = $1
		ORDER BY occurred_at DESC LIMIT 1`, requestID).Scan(&entitlement); err != nil {
		t.Fatalf("read decision entitlement: %v", err)
	}
	return entitlement
}

func partiallyDeliveredReturnOn(t *testing.T, p *pgxpool.Pool, delivered, requested time.Time) uuid.UUID {
	t.Helper()
	return twoLineReturnOn(t, p, delivered, time.Time{}, requested, false)
}

func returnWithLaterUnrelatedShipmentOn(t *testing.T, p *pgxpool.Pool, delivered, requested, later time.Time) uuid.UUID {
	t.Helper()
	return twoLineReturnOn(t, p, delivered, later, requested, true)
}

func insertPartialReturnSecondShipment(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	orderID uuid.UUID,
	number string,
	secondLine uuid.UUID,
	delivered, extra time.Time,
) {
	t.Helper()
	if extra.IsZero() {
		var undeliveredID uuid.UUID
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_shipments (order_id, carrier, tracking_number, shipped_at)
			VALUES ($1, '黑貓', 'T-PART-U-' || $2, $3)
			RETURNING id`, orderID, number, delivered.Add(-24*time.Hour)).Scan(&undeliveredID); err != nil {
			t.Fatalf("create undelivered parcel: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
			VALUES ($1, $2, $3, 1)`, orderID, undeliveredID, secondLine); err != nil {
			t.Fatalf("ship undelivered line: %v", err)
		}
		return
	}
	var laterID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, '黑貓', 'T-PART-L-' || $2, $3, $4)
		RETURNING id`, orderID, number, extra.Add(-24*time.Hour), extra).Scan(&laterID); err != nil {
		t.Fatalf("create later parcel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, laterID, secondLine); err != nil {
		t.Fatalf("ship later line: %v", err)
	}
}

func twoLineReturnOn(t *testing.T, p *pgxpool.Pool, delivered, extra, requested time.Time, returnFirstOnly bool) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orderID uuid.UUID
	var number string
	var lines [2]uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO orders (order_number, shipping_version_id, shipping_method_code,
		                    shipping_method_name, shipping_cents)
		SELECT next_order_number(), v.id, sm.code, v.name, 0
		FROM shipping_method_versions v JOIN shipping_methods sm ON sm.id = v.method_id
		ORDER BY v.effective_at LIMIT 1
		RETURNING id, order_number`).Scan(&orderID, &number); err != nil {
		t.Fatalf("create order: %v", err)
	}
	for i := range lines {
		if err := tx.QueryRow(ctx, `
			INSERT INTO order_lines (order_id, sku, product_name, unit_price_cents, quantity, position)
			VALUES ($1, $2, '部分送達退貨', 100000, 1, $3) RETURNING id`,
			orderID, "PARTIAL-RET-"+uuid.NewString()[:8], i).Scan(&lines[i]); err != nil {
			t.Fatalf("create line %d: %v", i, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_private_data (order_id, email, recipient_name, phone,
		                                postal_code, city, district, street)
		VALUES ($1, 'partial-return@example.com', '收件', '0912345678',
		        '110', '台北市', '信義區', '路 1 號')`, orderID); err != nil {
		t.Fatalf("create private data: %v", err)
	}
	sessionID := "cs_ret_partial_" + number
	if _, err := tx.Exec(ctx, `SELECT open_payment($1, $2, 200000)`, orderID, sessionID); err != nil {
		t.Fatalf("open payment: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT capture_payment($1, 200000, NULL, NULL)`, sessionID); err != nil {
		t.Fatalf("capture payment: %v", err)
	}
	moveOrderToShipped(t, tx, orderID)

	var firstShipment uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO order_shipments (
			order_id, carrier, tracking_number, shipped_at, delivered_at
		) VALUES ($1, '黑貓', 'T-PART-D-' || $2, $3, $4)
		RETURNING id`, orderID, number, delivered.Add(-48*time.Hour), delivered).Scan(&firstShipment); err != nil {
		t.Fatalf("create delivered parcel: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO order_shipment_lines (order_id, shipment_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, firstShipment, lines[0]); err != nil {
		t.Fatalf("ship delivered line: %v", err)
	}
	insertPartialReturnSecondShipment(t, ctx, tx, orderID, number, lines[1], delivered, extra)

	var requestID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO return_requests (order_id, reason, created_at)
		VALUES ($1, '', $2) RETURNING id`, orderID, requested).Scan(&requestID); err != nil {
		t.Fatalf("create return request: %v", err)
	}
	returned := lines[0]
	if !returnFirstOnly && extra.IsZero() {
		returned = lines[0]
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO return_request_lines (order_id, return_request_id, order_line_id, quantity)
		VALUES ($1, $2, $3, 1)`, orderID, requestID, returned); err != nil {
		t.Fatalf("create return line: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return requestID
}
