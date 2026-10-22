//go:build integration

package warranty_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/warranty"
)

func TestWarrantyNextExpiryMatchesRegistrationAcrossShopCalendarBoundaries(t *testing.T) {
	store := warranty.NewStore(warrantyStoreRolePool(t))
	handler := nextWarrantyRouter(store)
	for _, tc := range []struct {
		name, delivered, expiry string
		months                  int
	}{
		{name: "month end", delivered: "2026-01-31T12:00:00+08:00", months: 1, expiry: "2026-02-28"},
		{name: "leap month end", delivered: "2028-01-31T12:00:00+08:00", months: 1, expiry: "2028-02-29"},
		{name: "leap anniversary", delivered: "2028-02-29T12:00:00+08:00", months: 12, expiry: "2029-02-28"},
		{name: "before shop midnight", delivered: "2026-02-28T15:59:00Z", months: 1, expiry: "2026-03-28"},
		{name: "after shop midnight", delivered: "2026-02-28T16:01:00Z", months: 1, expiry: "2026-04-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, locale := range i18n.Locales() {
				t.Run(locale.Tag(), func(t *testing.T) {
					delivered, err := time.Parse(time.RFC3339, tc.delivered)
					if err != nil {
						t.Fatal(err)
					}
					f := newFixture(t, 1, tc.months, parcel{units: 1, arrived: true, deliveredAt: delivered})
					ready, err := store.Ready(t.Context(), f.userID)
					if err != nil || len(ready) != 1 || ready[0].Line.CoverEnds == nil || ready[0].Line.CoverEnds.ISO() != tc.expiry {
						t.Fatalf("next expiry for %s = %+v (%v), want %s", tc.delivered, ready, err, tc.expiry)
					}
					form := nextWarrantyGet(t, handler, f, locale, "/account/warranty/"+f.number)
					if !strings.Contains(form.Body.String(), tc.expiry) || !strings.Contains(form.Body.String(), `name="unit" value="1"`) {
						t.Error("next-unit form omits its independently expected calendar expiry")
					}
					posted := nextWarrantyPost(t, handler, f, locale, "1", "CALENDAR-"+f.lineID.String())
					if posted.Code != http.StatusSeeOther {
						t.Fatalf("registration = %d, want 303: %s", posted.Code, posted.Body.String())
					}
					var saved string
					if err := pool.QueryRow(t.Context(), `SELECT expires_on::text FROM warranty_registrations WHERE order_line_id=$1 AND unit_no=1`, f.lineID).Scan(&saved); err != nil {
						t.Fatal(err)
					}
					if saved != tc.expiry {
						t.Errorf("registered expiry = %q, want %q", saved, tc.expiry)
					}
					follow := nextWarrantyGet(t, handler, f, locale, posted.Header().Get("Location"))
					confirmation := warrantyResponseText(warrantyResponseNodes(t, follow.Body.String())["warranty-registered"])
					if !strings.Contains(confirmation, tc.expiry) {
						t.Errorf("confirmation = %q, want expiry %s", confirmation, tc.expiry)
					}
				})
			}
		})
	}
}
