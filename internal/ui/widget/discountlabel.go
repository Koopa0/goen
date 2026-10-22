package widget

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
)

// DiscountLabel names a discount line: the reason when there is one, "Discount" when not.
func DiscountLabel(ctx context.Context, reason string) string {
	if reason == "" {
		return i18n.T(ctx, i18n.KeyDiscount)
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDiscountFor), reason)
}
