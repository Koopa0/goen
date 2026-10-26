package checkout

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice/issuer"
)

// checkMobileBarcode refuses only a barcode the provider says does not exist. A
// provider outage, refusal or timeout falls back to shape validation, so an
// external failure never costs a shopper their checkout.
// Local fields, the quote and a prior successful attempt are handled before
// this point, so an idempotent retry never asks.
func (h *Handler) checkMobileBarcode(w http.ResponseWriter, r *http.Request, inv *Invoice, view *View) bool {
	if !inv.Type.NeedsMobileBarcode() || !h.invoiceGateway.Enabled() {
		return true
	}
	status, err := h.invoiceGateway.CheckBarcode(r.Context(), inv.MobileBarcode)
	if err != nil {
		h.log.WarnContext(r.Context(), "check mobile barcode", "error", err)
		return true
	}
	if status != issuer.BarcodeMissing {
		return true
	}
	view.Errors = map[string]string{"invoice_carrier": i18n.T(r.Context(), i18n.KeyMobileBarcodeMissing)}
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, view)
	return false
}
