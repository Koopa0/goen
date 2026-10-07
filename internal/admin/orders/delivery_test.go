package orders

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order"
	"github.com/koopa0/goen/internal/pickup"
	"github.com/koopa0/goen/internal/web"
)

func TestValidatedDeliveryPreservesFieldRefusals(t *testing.T) {
	t.Parallel()
	valid := DeliveryCorrection{
		Email: "shopper@example.com", Recipient: "Recipient", Phone: "0912345678",
		PostalCode: "110", City: "City", District: "District", Street: "1 Street",
		PickupChain: pickup.SevenEleven,
	}
	for _, tt := range []struct {
		name string
		to destination.Kind
		change func(*DeliveryCorrection)
		want []web.FieldRefusal
	}{
		{
			name: "short phone", to: destination.Address,
			change: func(d *DeliveryCorrection) { d.Phone = " 123 " },
			want: []web.FieldRefusal{{Field: "phone", MessageKey: i18n.KeyPhoneMalformed}},
		},
		{
			name: "pickup code without name", to: destination.PickupPoint,
			change: func(d *DeliveryCorrection) { d.PickupStoreCode = " a123 " },
			want: []web.FieldRefusal{{Field: "pickup_store_name", MessageKey: i18n.KeyAddressIncomplete}},
		},
		{
			name: "pickup name without code", to: destination.PickupPoint,
			change: func(d *DeliveryCorrection) { d.PickupStoreName = " Store " },
			want: []web.FieldRefusal{{Field: "pickup_store_code", MessageKey: i18n.KeyStoreCodeMalformed}},
		},
		{
			name: "recipient field identity", to: destination.Address,
			change: func(d *DeliveryCorrection) { d.Recipient = " " },
			want: []web.FieldRefusal{{Field: "name", MessageKey: i18n.KeyNameRequired}},
		},
		{
			name: "postal and non-postal failures", to: destination.Address,
			change: func(d *DeliveryCorrection) {
				d.Email, d.Recipient, d.Phone, d.PostalCode = "invalid", " ", "123", "11A"
			},
			want: []web.FieldRefusal{
				{Field: "email", MessageKey: i18n.KeyCheckoutEmailMalformed},
				{Field: "name", MessageKey: i18n.KeyNameRequired},
				{Field: "phone", MessageKey: i18n.KeyPhoneMalformed},
				{Field: "postal_code", MessageKey: i18n.KeyPostalCodeMalformed},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			draft := valid
			tt.change(&draft)
			before := draft
			addr, err := validatedDelivery(&draft, tt.to)
			if addr != nil {
				t.Errorf("validatedDelivery(refused draft) = %+v, want nil address", addr)
			}
			wrapped := fmt.Errorf("correcting delivery: %w", err)
			refusal, ok := errors.AsType[*DeliveryValidationError](wrapped)
			if !ok {
				t.Fatalf("validatedDelivery(refused draft) error = %v, want DeliveryValidationError", err)
			}
			if diff := cmp.Diff(tt.want, refusal.Fields); diff != "" {
				t.Errorf("delivery field refusals mismatch (-want +got):\n%s", diff)
			}
			if !errors.Is(wrapped, ErrInvalid) {
				t.Errorf("delivery validation error = %v, want ErrInvalid in chain", wrapped)
			}
			if _, ok := errors.AsType[*DeliveryPostalError](wrapped); ok {
				t.Errorf("field validation error = %v, want no delivery-zone error", wrapped)
			}
			if diff := cmp.Diff(before, draft); diff != "" {
				t.Errorf("refused raw draft changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidatedDeliveryNormalizesOnlyItsSuccessfulCopy(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		to destination.Kind
		want order.Delivery
	}{
		{
			name: "address", to: destination.Address,
			want: order.Delivery{
				To: destination.Address, Email: "shopper@example.com", RecipientName: "Recipient", Phone: "0912345678",
				PostalCode: "110", City: "City", District: "District", Street: "1 Street",
			},
		},
		{
			name: "pickup", to: destination.PickupPoint,
			want: order.Delivery{
				To: destination.PickupPoint, Email: "shopper@example.com", RecipientName: "Recipient", Phone: "0912345678",
				PickupChain: pickup.SevenEleven, PickupStoreCode: "A123", PickupStoreName: "Store",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			draft := DeliveryCorrection{
				Email: " shopper@example.com ", Recipient: " Recipient ", Phone: " ０９１２３４５６７８ ",
				PostalCode: " １１０ ", City: " City ", District: " District ", Street: " 1 Street ",
				PickupChain: " seven_eleven ", PickupStoreCode: " a123 ", PickupStoreName: " Store ",
			}
			before := draft
			addr, err := validatedDelivery(&draft, tt.to)
			if err != nil {
				t.Fatalf("validatedDelivery(valid %s) = %v, want success", tt.name, err)
			}
			if diff := cmp.Diff(&tt.want, addr); diff != "" {
				t.Errorf("normalized delivery mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(before, draft); diff != "" {
				t.Errorf("successful raw draft changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeliveryFormOfKeepsSubmittedText(t *testing.T) {
	t.Parallel()
	values := url.Values{
		"email": {" shopper@example.com "}, "recipient": {" Recipient "}, "phone": {" １２３ "},
		"postal_code": {" １１０ "}, "city": {" City "}, "district": {" District "}, "street": {" 1 Street "},
		"pickup_chain": {" seven_eleven "}, "pickup_store_code": {" a123 "}, "pickup_store_name": {" Store "},
	}
	want := &DeliveryCorrection{
		Email: " shopper@example.com ", Recipient: " Recipient ", Phone: " １２３ ",
		PostalCode: " １１０ ", City: " City ", District: " District ", Street: " 1 Street ",
		PickupChain: " seven_eleven ", PickupStoreCode: " a123 ", PickupStoreName: " Store ",
	}
	if diff := cmp.Diff(want, deliveryFormOf(values.Get)); diff != "" {
		t.Errorf("submitted delivery draft mismatch (-want +got):\n%s", diff)
	}
}
