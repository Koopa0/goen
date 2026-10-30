package coupon

import (
	"context"
	"fmt"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
)

// DiscountLabel names a discount line: the reason when there is one, "Discount" when not.
func DiscountLabel(ctx context.Context, reason string) string {
	if reason == "" {
		return i18n.T(ctx, i18n.KeyDiscount)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountFor), reason)
}

// Redeemed is the coupon a placed order used, as its redemption joins it. The zero value is an order that used none.
type Redeemed struct {
	Code        string
	Description string
	Kind        Kind
}

// Used is true for an order that redeemed a coupon. The discount amount cannot say: a free-delivery code
// takes nothing off the goods.
func (r Redeemed) Used() bool { return r.Code != "" }

// Label names the order's discount row by its code. A description that already contains the code is left out,
// because it is free text and often begins with the code, which would read twice.
func (r Redeemed) Label(ctx context.Context) string {
	switch {
	case r.Code == "":
		return i18n.T(ctx, i18n.KeyDiscount)
	case r.Kind == FreeShipping:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountCodeFreeShipping), r.Code)
	case r.Description == "" || strings.Contains(strings.ToUpper(r.Description), strings.ToUpper(r.Code)):
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountCode), r.Code)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountCodeNamed), r.Code, r.Description)
	}
}
