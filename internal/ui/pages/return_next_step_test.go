package pages

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"time"

	nethtml "golang.org/x/net/html"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/returns"
)

func TestReturnFormExplainsEachParcelsDeadlineAndNextStep(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 12, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		locale i18n.Locale
		state  string
	}{
		{i18n.ZhHant, "待審核"},
		{i18n.En, "Awaiting review"},
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
			if !strings.Contains(body, tc.state) {
				t.Errorf("rendered open request state lacks %q", tc.state)
			}
		})
	}
}

func TestReturnFormSaysWhatHappensNextOnlyWhenARequestCanBeSent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		view ReturnsView
		want bool
	}{
		{"returnable", ReturnsView{Number: "GO-NEXT", Lines: []ReturnsLine{{ID: "line", Returnable: 1}}}, true},
		{"open request", ReturnsView{Number: "GO-NEXT", HasOpen: true, Lines: []ReturnsLine{{ID: "line", Returnable: 1}}}, false},
		{"nothing returnable", ReturnsView{Number: "GO-NEXT", Lines: []ReturnsLine{{ID: "line"}}}, false},
	} {
		for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
			t.Run(tc.name+"/"+string(locale), func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				var rendered strings.Builder
				if err := Returns(ReturnsMeta(ctx, tc.view.Number), tc.view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				got := strings.Contains(html.UnescapeString(rendered.String()), i18n.T(ctx, i18n.KeyReturnSub))
				if got != tc.want {
					t.Errorf("next-step sentence shown = %v, want %v", got, tc.want)
				}
			})
		}
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
				view := ReturnsView{Number: "GO-PENDING", Pickup: pickup, Shipments: []OrderShipment{{RescissionDays: 8}}}
				var rendered strings.Builder
				if err := Returns(ReturnsMeta(ctx, view.Number), view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				key := i18n.KeyOrderRescissionAwaits
				if pickup {
					key = i18n.KeyOrderRescissionAwaitsPickup
				}
				sentence := i18n.Count(ctx, key, 8, 8)
				if !strings.Contains(html.UnescapeString(rendered.String()), sentence) {
					t.Errorf("undelivered parcel missing its existing order-page sentence %q", sentence)
				}
			})
		}
	}
}

// The page cannot tell whether the order may still be cancelled (a paid order
// that has not shipped may not), and a refunded one has nothing left to cancel,
// so it states the fact the refusal states and does not send the customer to a
// button the order page may not show.
func TestNothingReturnableIsStatedAsTheRefusalStatesIt(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		t.Run(string(locale), func(t *testing.T) {
			t.Parallel()
			ctx := i18n.WithLocale(t.Context(), locale)
			view := ReturnsView{Number: "GO-NONE", Lines: []ReturnsLine{{ID: "line"}}}
			var rendered strings.Builder
			if err := Returns(ReturnsMeta(ctx, view.Number), view).Render(ctx, &rendered); err != nil {
				t.Fatal(err)
			}
			doc, err := nethtml.Parse(strings.NewReader(rendered.String()))
			if err != nil {
				t.Fatal(err)
			}
			var notice *nethtml.Node
			for n := range doc.Descendants() {
				if n.Type == nethtml.ElementNode && attrValue(n, "id") == "return-nothing" {
					notice = n
				}
			}
			if notice == nil {
				t.Fatal("no notice says that nothing can be returned")
			}
			var text strings.Builder
			for n := range notice.Descendants() {
				if n.Type == nethtml.TextNode {
					text.WriteString(n.Data)
				}
			}
			if got, want := strings.TrimSpace(text.String()), i18n.T(ctx, i18n.KeyReturnNothingShort); got != want {
				t.Errorf("notice = %q, want the refusal's sentence %q", got, want)
			}
		})
	}
}
