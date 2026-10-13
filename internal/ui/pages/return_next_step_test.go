package pages

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
)

func TestReturnFormExplainsEachParcelsDeadlineAndNextStep(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		locale      i18n.Locale
		next, state string
	}{
		{i18n.ZhHant, "送出後，審核結果會顯示在這筆訂單的頁面上。同意後，款項依原付款方式退回。", "待審核"},
		{i18n.En, "Once you send it, the decision appears on this order's page. If we approve it, the money goes back the way you paid.", "Awaiting review"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), tc.locale)
			view := ReturnsView{Number: "GO-RETURN", Now: now, HasOpen: true, Existing: []ReturnsExisting{{Status: returns.StatusRequested}}, Shipments: []OrderShipment{
				{DeliveredAt: now.AddDate(0, 0, -7), RescissionEnds: now},
				{DeliveredAt: now, RescissionEnds: now.AddDate(0, 0, 7)},
			}, Lines: []ReturnsLine{{ID: "line", Returnable: 2}}}
			var rendered strings.Builder
			if err := Returns(ReturnsMeta(ctx, view.Number), view).Render(ctx, &rendered); err != nil {
				t.Fatal(err)
			}
			body := html.UnescapeString(rendered.String())
			for i, parcel := range view.Shipments {
				order := OrderView{Now: now, Shipments: view.Shipments}
				sentence := fmt.Sprintf(i18n.T(ctx, i18n.KeyOrderRescissionEnds), order.RescissionLastDay(ctx, parcel))
				if !strings.Contains(body, sentence) || !strings.Contains(body, order.ParcelTitle(ctx, i)) {
					t.Errorf("parcel %d missing its existing order-page deadline sentence or label", i+1)
				}
			}
			if !strings.Contains(body, tc.next) {
				t.Error("missing owner-quoted next-step sentence")
			}
			if !strings.Contains(body, tc.state) {
				t.Errorf("rendered open request state lacks %q", tc.state)
			}
		})
	}
}

func TestOpenRequestShowsOnlyItsNoticeEvenWithAStaleQuantityRefusal(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		ctx := i18n.WithLocale(t.Context(), locale)
		view := ReturnsView{Number: "GO-OPEN", HasOpen: true, HasDraft: true, FormRefusal: i18n.T(ctx, i18n.KeyReturnTooMany), Reason: "Kept reason", Lines: []ReturnsLine{{ID: "line", Quantity: "2"}}}
		var rendered strings.Builder
		if err := Returns(ReturnsMeta(ctx, view.Number), view).Render(ctx, &rendered); err != nil {
			t.Fatal(err)
		}
		body := html.UnescapeString(rendered.String())
		if strings.Contains(body, i18n.T(ctx, i18n.KeyReturnTooMany)) || strings.Contains(body, `id="return-form-error"`) {
			t.Error("open request shows a quantity refusal")
		}
		if strings.Count(body, i18n.T(ctx, i18n.KeyReturnInFlight)) != 1 {
			t.Error("open request must show one notice")
		}
		if !strings.Contains(body, `value="2"`) || !strings.Contains(body, "Kept reason") {
			t.Error("422 draft input was lost")
		}
	}
}

func TestReturnFormUsesTheExistingPendingDeliverySentence(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, pickup := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/pickup=%v", locale, pickup), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := ReturnsView{Number: "GO-PENDING", Pickup: pickup, Shipments: []OrderShipment{{}}}
				var rendered strings.Builder
				if err := Returns(ReturnsMeta(ctx, view.Number), view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				key := i18n.KeyOrderRescissionAwaits
				if pickup {
					key = i18n.KeyOrderRescissionAwaitsPickup
				}
				sentence := i18n.Count(ctx, key, RescissionDays, RescissionDays)
				if !strings.Contains(html.UnescapeString(rendered.String()), sentence) {
					t.Errorf("undelivered parcel missing its existing order-page sentence %q", sentence)
				}
			})
		}
	}
}
