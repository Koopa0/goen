package cart

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/pages"
)

// checkMobileCarrier refuses a barcode the provider says does not exist, and
// nothing else. The check is auxiliary: a provider outage, a refusal to answer
// or a timeout places the order on shape validation alone, as before the check
// existed, so an external failure never costs a shopper their checkout. Only a
// definite "does not exist" is a reason to stop.
//
// Local fields and the quote have already passed, and a prior successful
// attempt is answered before this point, so an idempotent retry never asks.
func (h *Handler) checkMobileCarrier(w http.ResponseWriter, r *http.Request, inv *Invoice, view *pages.CheckoutView) bool {
	if !inv.Type.NeedsCarrier() || h.carriers == nil {
		return true
	}
	status, err := h.carriers.CheckBarcode(r.Context(), inv.Carrier)
	if err != nil {
		h.log.WarnContext(r.Context(), "check mobile carrier", "error", err)
		return true
	}
	if status != invoice.CarrierMissing {
		return true
	}
	view.Errors = map[string]string{"invoice_carrier": i18n.T(r.Context(), i18n.KeyCarrierMissing)}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
	return false
}
