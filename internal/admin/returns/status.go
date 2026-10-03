package returns

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	returnrules "github.com/koopa0/goen/internal/returns"
)

func statusLabel(ctx context.Context, s returnrules.Status) string {
	switch s {
	case returnrules.StatusRequested:
		return i18n.T(ctx, i18n.KeyAdminReturnRequested)
	case returnrules.StatusApproved:
		return i18n.T(ctx, i18n.KeyAdminReturnApproved)
	case returnrules.StatusRejected:
		return i18n.T(ctx, i18n.KeyAdminReturnRejected)
	case returnrules.StatusCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnCompleted)
	default:
		return string(s)
	}
}

// statusText is a return's status as the queue shows it. A refund before
// shipment that has finished is a cancellation: nothing came back, so
// "completed" would read as a return that did.
func statusText(ctx context.Context, s returnrules.Status, beforeShipment bool) string {
	if beforeShipment && s == returnrules.StatusCompleted {
		return i18n.T(ctx, i18n.KeyAdminReturnCancelledRefunded)
	}
	return statusLabel(ctx, s)
}
