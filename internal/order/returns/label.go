package returns

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// Label is the back office's words for w, as the return queue and the audit
// trail show it. A value ParsePolicyWindow refuses is a programming error, so
// it panics rather than print a window nobody counted.
func (w PolicyWindow) Label(ctx context.Context) string {
	switch w {
	case WindowStatutory:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowWithin)
	case WindowGoodwill:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowGoodwill)
	case WindowLate:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowAfter)
	case WindowUndelivered:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowUndelivered)
	case WindowMixed:
		return i18n.T(ctx, i18n.KeyAdminReturnWindowMixed)
	default:
		panic("returns: unknown policy window: " + string(w))
	}
}

// Label is the staff member's code as the back office words it.
func (r UnsentReason) Label(ctx context.Context) string {
	switch r {
	case UnsentDamaged:
		return i18n.T(ctx, i18n.KeyUnsentReasonDamaged)
	case UnsentLost:
		return i18n.T(ctx, i18n.KeyUnsentReasonLost)
	case UnsentSupplierFailed:
		return i18n.T(ctx, i18n.KeyUnsentReasonSupplierFailed)
	case UnsentCustomerWithdrew:
		return i18n.T(ctx, i18n.KeyUnsentReasonCustomerWithdrew)
	default:
		return string(r)
	}
}
