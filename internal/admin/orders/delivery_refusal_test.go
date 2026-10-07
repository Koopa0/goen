package orders

import (
	"html"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func TestDeliveryRefusalShowsAnAbsentControlsReason(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, tc := range []struct {
			name   string
			pickup bool
			field  string
		}{
			{name: "address with pickup chain", field: "pickup_chain"},
			{name: "address with pickup code", field: "pickup_store_code"},
			{name: "address with pickup name", field: "pickup_store_name"},
			{name: "pickup with postcode", pickup: true, field: "postal_code"},
			{name: "pickup with city", pickup: true, field: "city"},
			{name: "pickup with district", pickup: true, field: "district"},
			{name: "pickup with street", pickup: true, field: "street"},
		} {
			t.Run(string(locale)+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				draft := DeliveryCorrection{
					Email: " proposed@example.com ", Recipient: " Proposed <recipient> ", Phone: " 123 ",
					PostalCode: "110", City: " New city ", District: " New district ", Street: " Proposed <street> ",
					PickupChain: pickup.SevenEleven, PickupStoreCode: " a123 ", PickupStoreName: " Proposed <store> ",
				}
				before := draft
				view := admin.OrderView{
					Number: "GO-REFUSAL", Correctable: true, PickupDestination: tc.pickup,
					PickupChains: pages.PickupChainChoices(), Recipient: "Saved recipient",
				}
				applyDeliveryRefusals(ctx, &view, &draft, []web.FieldRefusal{
					{Field: "phone", MessageKey: i18n.KeyPhoneMalformed},
					{Field: tc.field, MessageKey: i18n.KeyFieldHasControlChars},
				})
				var rendered strings.Builder
				if err := admin.Order(layouts.Page{Title: view.Number}, &view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				body := rendered.String()
				want := html.EscapeString(i18n.T(ctx, i18n.KeyFieldHasControlChars))
				if !strings.Contains(body, want) || !strings.Contains(body, `class="goen-notice__lead"`) {
					t.Errorf("absent control %q has no localized refusal alert %q", tc.field, want)
				}
				if strings.Contains(body, `name="`+tc.field+`"`) {
					t.Errorf("opposite destination control %q appeared in the form", tc.field)
				}
				for _, value := range []string{draft.Email, draft.Recipient, draft.Phone} {
					if !strings.Contains(body, `value="`+html.EscapeString(value)+`"`) {
						t.Errorf("refused delivery lost submitted value %q", value)
					}
				}
				if !strings.Contains(body, `aria-describedby="d-phone-error"`) || !strings.Contains(body, html.EscapeString(i18n.T(ctx, i18n.KeyPhoneMalformed))) {
					t.Error("the visible phone refusal lost its field association or reason")
				}
				if diff := cmp.Diff(before, draft); diff != "" {
					t.Errorf("refused raw draft changed (-want +got):\n%s", diff)
				}
			})
		}
	}
}

func TestDeliveryRefusalKeepsVisibleErrorsBesideTheirControls(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.En, i18n.ZhHant} {
		for _, pickupDestination := range []bool{false, true} {
			t.Run(string(locale)+"/"+map[bool]string{false: "address", true: "pickup"}[pickupDestination], func(t *testing.T) {
				t.Parallel()
				ctx := i18n.WithLocale(t.Context(), locale)
				view := admin.OrderView{Number: "GO-REFUSAL", Correctable: true, PickupDestination: pickupDestination}
				applyDeliveryRefusals(ctx, &view, &DeliveryCorrection{}, []web.FieldRefusal{
					{Field: "name", MessageKey: i18n.KeyNameRequired},
					{Field: "phone", MessageKey: i18n.KeyPhoneRequired},
				})
				var rendered strings.Builder
				if err := admin.Order(layouts.Page{}, &view).Render(ctx, &rendered); err != nil {
					t.Fatal(err)
				}
				body := rendered.String()
				if strings.Contains(body, `class="goen-notice__lead"`) {
					t.Error("visible field refusals also produced a form-level refusal")
				}
				for _, id := range []string{"d-recipient-error", "d-phone-error"} {
					if !strings.Contains(body, `aria-describedby="`+id+`"`) {
						t.Errorf("visible refusal lost field association %q", id)
					}
				}
			})
		}
	}
}
