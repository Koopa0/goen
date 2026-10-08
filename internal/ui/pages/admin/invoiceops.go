package admin

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// InvoiceOperationLabels and InvoiceOperationStatuses name the closed sets an
// invoice operation's kind and status come from.
var (
	InvoiceOperationLabels = map[string]i18n.Key{
		"issue":     i18n.KeyAuditInvoiceIssue,
		"allowance": i18n.KeyAuditInvoiceAllowance,
		"void":      i18n.KeyAuditInvoiceVoid,
	}
	InvoiceOperationStatuses = map[string]i18n.Key{
		"pending":        i18n.KeyAdminTimelineInvoicePending,
		"not_sent":       i18n.KeyAdminTimelineInvoiceNotSent,
		"awaiting_buyer": i18n.KeyAdminTimelineInvoiceAwaitingBuyer,
		"attention":      i18n.KeyAdminTimelineInvoiceAttention,
		"succeeded":      i18n.KeyAdminTimelineInvoiceSucceeded,
		"rejected":       i18n.KeyAdminTimelineInvoiceRejected,
	}
)

func (c StrandedClaim) KindText(ctx context.Context) string {
	if k, ok := InvoiceOperationLabels[c.Kind]; ok {
		return i18n.T(ctx, k)
	}
	return c.Kind
}

func (c StrandedClaim) StatusText(ctx context.Context) string {
	if k, ok := InvoiceOperationStatuses[c.Status]; ok {
		return i18n.T(ctx, k)
	}
	return c.Status
}
