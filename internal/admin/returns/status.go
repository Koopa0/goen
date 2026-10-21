package returns

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/order/returns"
)

func statusLabel(ctx context.Context, s returns.Status) string {
	switch s {
	case returns.StatusRequested:
		return i18n.T(ctx, i18n.KeyAdminReturnRequested)
	case returns.StatusApproved:
		return i18n.T(ctx, i18n.KeyAdminReturnApproved)
	case returns.StatusRejected:
		return i18n.T(ctx, i18n.KeyAdminReturnRejected)
	case returns.StatusCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnCompleted)
	default:
		return string(s)
	}
}

// statusText is a return's status as the queue shows it. A refund before
// shipment that has finished is a cancellation, and a finished refund of units
// that were never sent is just that: nothing came back, so "completed" would
// read as a return that did.
func statusText(ctx context.Context, s returns.Status, beforeShipment, unsent bool) string {
	switch {
	case beforeShipment && s == returns.StatusCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnCancelledRefunded)
	case unsent && s == returns.StatusCompleted:
		return i18n.T(ctx, i18n.KeyAdminReturnUnsentRefunded)
	}
	return statusLabel(ctx, s)
}
