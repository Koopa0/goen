package cart

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/invoice"
	"github.com/koopa0/goen/internal/ratelimit"
)

// Local fields and the current commercial quote have already passed. A prior
// successful attempt is answered before this point, so provider downtime cannot
// turn an idempotent retry into a new order or a refusal of an existing order.
func (h *Handler) checkMobileCarrier(w http.ResponseWriter, r *http.Request, cartID uuid.UUID, submission *checkoutSubmission) bool {
	if !submission.invoice.UsesMobileCarrier() || h.carriers == nil {
		return true
	}
	for _, key := range []string{"cart:" + cartID.String(), "ip:" + ratelimit.ClientIP(r)} {
		if _, ok := h.carrierLimit.Allow(key); !ok {
			submission.view.CarrierCheckNotice = i18n.T(r.Context(), i18n.KeyCarrierCheckLimited)
			h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
			return false
		}
	}
	status, err := h.carriers.CheckBarcode(r.Context(), submission.invoice.Carrier)
	if err == nil {
		switch status {
		case invoice.CarrierExists:
			return true
		case invoice.CarrierMissing:
			submission.view.Errors = map[string]string{"invoice_carrier": i18n.T(r.Context(), i18n.KeyCarrierMissing)}
			h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
			return false
		case invoice.CarrierUnknown:
		}
	}
	// The posted choice never bypasses a known N or the rate limiter. It is
	// accepted only after this attempt actually asked and got no verdict.
	if r.Context().Err() == nil && r.PostFormValue("invoice_carrier_continue") == "1" {
		return true
	}
	submission.view.CarrierCheckNotice = i18n.T(r.Context(), i18n.KeyCarrierCheckUnavailable)
	submission.view.CarrierCheckUnavailable = true
	h.renderCheckout(w, r, http.StatusUnprocessableEntity, &submission.view)
	return false
}
