package invoice

import "github.com/koopa0/goen/internal/i18n"

// OperationKindLabels and OperationStatusLabels name the closed sets an invoice
// operation's kind and status come from.
var (
	OperationKindLabels = map[string]i18n.Key{
		"issue":     i18n.KeyAuditInvoiceIssue,
		"allowance": i18n.KeyAuditInvoiceAllowance,
		"void":      i18n.KeyAuditInvoiceVoid,
	}
	OperationStatusLabels = map[string]i18n.Key{
		"pending":        i18n.KeyAdminTimelineInvoicePending,
		"not_sent":       i18n.KeyAdminTimelineInvoiceNotSent,
		"awaiting_buyer": i18n.KeyAdminTimelineInvoiceAwaitingBuyer,
		"attention":      i18n.KeyAdminTimelineInvoiceAttention,
		"succeeded":      i18n.KeyAdminTimelineInvoiceSucceeded,
		"rejected":       i18n.KeyAdminTimelineInvoiceRejected,
	}
)
