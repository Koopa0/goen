package outbox

// PasswordResetRequest is a queued forgotten-password request. It names the
// account by id and never by address, and names none when the address has no
// account: the request is queued either way, so asking about an address costs
// the same whether or not it belongs to somebody.
type PasswordResetRequest struct {
	UserID string `json:"user_id"`
	Locale string `json:"locale"`
}

// AccountRegistration is a queued registration. It names the account by id and
// never by address: the new one, or the one that already held the address. Next
// is the same-site path the registrant was headed for, carried to the link.
type AccountRegistration struct {
	UserID  string `json:"user_id"`
	Created bool   `json:"created"`
	Locale  string `json:"locale"`
	Next    string `json:"next"`
}

// InvoiceDue is an order whose sale became final, so its 統一發票 is owed now:
// 營業稅法 §32's 時限表 invoices a prepaid sale when the money arrives, not at
// dispatch.
type InvoiceDue struct {
	OrderNumber string `json:"order_number"`
	// Trigger is the provider event that captured the payment, the payment staff
	// attributed, or the checkout that store credit paid in full. It is the
	// system claim's request id.
	Trigger string `json:"trigger"`
}

// InvoiceVoidDue is an order its customer cancelled after its 統一發票 was
// owed: store credit paid it in full at checkout.
type InvoiceVoidDue struct {
	OrderNumber string `json:"order_number"`
	// Trigger is the cancellation, and the system void's request id.
	Trigger string `json:"trigger"`
}
