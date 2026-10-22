package shipping

import (
	"math"
	"testing"

	"github.com/koopa0/goen/internal/shipping"

	"github.com/koopa0/goen/internal/i18n"
)

// TestNewMethod_ValidateDollarOverflow: shipping dollar inputs are bounded
// before multiplication. Validate refuses a fee and a free-over amount of
// MaxInt64 dollars instead of overflowing when they become cents.
func TestNewMethod_ValidateDollarOverflow(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)

	method := (&NewMethod{
		Code: "overflow", Destination: "address", Name: "Overflow",
		FeeDollars: math.MaxInt64, FreeOverDollars: math.MaxInt64,
	}).Validate(ctx)
	if method["fee"] == "" || method["free_over"] == "" {
		t.Fatalf("shipping overflow fields were accepted: %v", method)
	}
}

// TestNewMethod_ValidateParcelCeilings: method parcel limits stop at the schema
// ceilings. Validate refuses a longest side, three-side sum or weight one above
// its ceiling and accepts each at the ceiling.
func TestNewMethod_ValidateParcelCeilings(t *testing.T) {
	t.Parallel()
	ctx := i18n.WithLocale(t.Context(), i18n.En)
	for _, tt := range []struct {
		name  string
		field string
		errs  func() map[string]string
	}{
		{name: "method longest", field: "max_parcel_longest", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelLongestMM: shipping.MaxParcelLongestMM + 1}).Validate(ctx)
		}},
		{name: "method sum", field: "max_parcel_sum", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelSumMM: shipping.MaxParcelSumMM + 1}).Validate(ctx)
		}},
		{name: "method weight", field: "max_parcel_weight", errs: func() map[string]string {
			return (&NewMethod{Code: "bound", Destination: "address", Name: "Bound",
				MaxParcelWeightG: shipping.MaxParcelWeightG + 1}).Validate(ctx)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if errs := tt.errs(); errs[tt.field] == "" {
				t.Errorf("missing %q refusal: %v", tt.field, errs)
			}
		})
	}

	atCeilings := (&NewMethod{
		Code: "bound", Destination: "address", Name: "Bound",
		MaxParcelLongestMM: shipping.MaxParcelLongestMM, MaxParcelSumMM: shipping.MaxParcelSumMM,
		MaxParcelWeightG: shipping.MaxParcelWeightG,
	}).Validate(ctx)
	for _, field := range []string{"max_parcel_longest", "max_parcel_sum", "max_parcel_weight"} {
		if atCeilings[field] != "" {
			t.Errorf("method ceiling %q refused: %v", field, atCeilings)
		}
	}
}
