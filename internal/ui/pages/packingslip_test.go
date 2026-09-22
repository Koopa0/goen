package pages

import (
	"bytes"
	"strings"
	"testing"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/pickup"
)

func TestPackingSlipContainsOnlyFulfilmentData(t *testing.T) {
	t.Parallel()
	for _, locale := range []i18n.Locale{i18n.ZhHant, i18n.En} {
		for _, destination := range []Delivery{
			{PostalCode: "100", City: "Taipei", Street: "Long shipping street 123"},
			{PickupBrand: pickup.FamilyMart, PickupStoreName: "Pickup branch", PickupStoreCode: "STORE123"},
		} {
			ctx := i18n.WithLocale(t.Context(), locale)
			order := &AdminOrderView{
				Number: "GO-SLIP-143", Recipient: "Parcel Recipient", Phone: "0912345678", Address: destination.Line(),
				Email: "private@example.com", CustomerNote: "CUSTOMER-SECRET", StaffNote: "STAFF-SECRET",
				InvoiceCarrier: "/SECRET1", InvoiceTaxID: "87654321",
				Lines: []OrderLine{{SKU: "SKU-143", Name: "Snapshot product", Label: "Blue / Large", Quantity: 3, UnitCents: 12345600}},
			}
			var out bytes.Buffer
			if err := AdminPackingSlip(PackingSlipFromOrder(order)).Render(ctx, &out); err != nil {
				t.Fatal(err)
			}
			html := out.String()
			for _, want := range []string{order.Number, order.Recipient, order.Phone, order.Address, "SKU-143", "Snapshot product", "Blue / Large", ">3<", i18n.T(ctx, i18n.KeyPackingSlipQuantity)} {
				if !strings.Contains(html, want) {
					t.Errorf("packing slip missing %q", want)
				}
			}
			for _, secret := range []string{order.Email, order.CustomerNote, order.StaffNote, order.InvoiceCarrier, order.InvoiceTaxID, "123,456", "goen-adminbar", "goen-admin__nav", "invoice/", "token="} {
				if strings.Contains(html, secret) {
					t.Errorf("packing slip leaks %q", secret)
				}
			}
		}
	}
}
