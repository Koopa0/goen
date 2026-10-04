//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/admin"
	"github.com/koopa0/goen/internal/admin/admintest"
	"github.com/koopa0/goen/internal/i18n"
)

func TestDispatchWithARecordedTrackingNumberIsRefusedOnTheField(t *testing.T) {
	ctx, staff := staffContext(t)
	s := admin.NewStore(pool, admintest.Refunder{}, nil, nil)
	h := adminHandlerOver(s)
	first, second := shippableOrder(t, "zh-Hant"), shippableOrder(t, "zh-Hant")
	tracking := "DUP-" + uuid.NewString()[:8]
	if err := s.Ship(ctx, first, admin.Dispatch{Carrier: "black_cat", Tracking: tracking},
		uuid.NullUUID{UUID: staff, Valid: true}); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}

	form := url.Values{"carrier": {"black_cat"}, "tracking": {tracking}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/orders/"+second+"/ship", strings.NewReader(form.Encode()))
	req.SetPathValue("number", second)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	backOffice.RequireStaff(h.Ship)(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a reused tracking number answered %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`aria-invalid="true"`, `aria-describedby="ship-tracking-error"`,
		`id="ship-tracking-error"`, `value="` + tracking + `"`, i18n.T(ctx, i18n.KeyAdminTrackingTaken)} {
		if !strings.Contains(body, want) {
			t.Errorf("refused dispatch is missing %q", want)
		}
	}
	var parcels int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM order_shipments s JOIN orders o ON o.id = s.order_id WHERE o.order_number = $1`,
		second).Scan(&parcels); err != nil {
		t.Fatal(err)
	}
	if parcels != 0 {
		t.Errorf("the refused dispatch left %d parcels on the second order", parcels)
	}
}
