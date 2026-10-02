package cart

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ui/pages"
)

// checkMobileBarcode refuses a barcode the provider says does not exist, and
// nothing else. The check is auxiliary: a provider outage, a refusal to answer
// or a timeout places the order on shape validation alone, as before the check
// existed, so an external failure never costs a shopper their checkout. Only a
// definite "does not exist" is a reason to stop.
//
// Local fields and the quote have already passed, and a prior successful
// attempt is answered before this point, so an idempotent retry never asks.
func (h *Handler) checkMobileBarcode(w http.ResponseWriter, r *http.Request, inv *Invoice, view *pages.CheckoutView) bool {
	if !inv.Type.NeedsMobileBarcode() || h.barcodeChecker == nil {
		return true
	}
	status, err := h.barcodeChecker.CheckBarcode(r.Context(), inv.MobileBarcode)
	if err != nil {
		h.log.WarnContext(r.Context(), "check mobile barcode", "error", err)
		return true
	}
	if status != invoice.BarcodeMissing {
		return true
	}
	view.Errors = map[string]string{"invoice_carrier": i18n.T(r.Context(), i18n.KeyMobileBarcodeMissing)}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
	return false
}
