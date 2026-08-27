package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// CreateShippingMethod serves POST /admin/shipping/method.
func (h *Handler) CreateShippingMethod(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	m := &NewMethod{
		Code:        r.PostFormValue("code"),
		Destination: r.PostFormValue("destination"),
		// Zero is "no stated limit", the honest default for home delivery.
		MaxParcelLongestMM: parseSafetyStock(r.PostFormValue("max_parcel_longest")),
		MaxParcelSumMM:     parseSafetyStock(r.PostFormValue("max_parcel_sum")),
		MaxParcelWeightG:   parseSafetyStock(r.PostFormValue("max_parcel_weight")),
		Name:               r.PostFormValue("name"),
		NameEn:             r.PostFormValue("name_en"),
		Carrier:            r.PostFormValue("carrier"),
		CarrierEn:          r.PostFormValue("carrier_en"),
		FeeDollars:         dollars(r.PostFormValue("fee")),
		FreeOverDollars:    dollars(r.PostFormValue("free_over")),
	}
	errs, err := h.store.CreateMethod(r.Context(), m)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create shipping method", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{
			Code: m.Code, Destination: m.Destination,
			Name: m.Name, NameEn: m.NameEn,
			Carrier: m.Carrier, CarrierEn: m.CarrierEn,
			Fee: r.PostFormValue("fee"), FreeOver: r.PostFormValue("free_over"),
		}, pages.AdminZoneDraft{}, pages.AdminZonePrefixesDraft{})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// SetShippingMethodActive serves POST /admin/shipping/method/{id}/active. A
// method is switched OFF, never deleted: past orders name their version.
func (h *Handler) SetShippingMethodActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetMethodActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "1")
	if err != nil {
		h.log.WarnContext(r.Context(), "toggle shipping method", "error", err)
		h.notFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
}

// CreateShippingZone serves POST /admin/shipping/zone.
func (h *Handler) CreateShippingZone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	z := &NewZone{
		Code:     r.PostFormValue("code"),
		Name:     r.PostFormValue("name"),
		NameEn:   r.PostFormValue("name_en"),
		Prefixes: r.PostFormValue("prefixes"),
	}
	errs, err := h.store.CreateZone(r.Context(), z)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create shipping zone", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{}, pages.AdminZoneDraft{
			Code: z.Code, Name: z.Name, NameEn: z.NameEn, Prefixes: z.Prefixes,
		}, pages.AdminZonePrefixesDraft{})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// SetZonePrefixes serves POST /admin/shipping/zone/{id}/prefixes.
func (h *Handler) SetZonePrefixes(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	zoneID, prefixes := r.PathValue("id"), r.PostFormValue("prefixes")
	errs, err := h.store.SetZonePrefixes(r.Context(), zoneID, prefixes)
	switch {
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "set zone prefixes", "error", err)
		h.notFound(w, r)
	case err != nil:
		h.log.ErrorContext(r.Context(), "set zone prefixes", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, pages.AdminMethodDraft{}, pages.AdminZoneDraft{},
			pages.AdminZonePrefixesDraft{ZoneID: zoneID, Prefixes: prefixes})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

// DeleteShippingZone serves POST /admin/shipping/zone/{id}/delete.
func (h *Handler) DeleteShippingZone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.DeleteZone(r.Context(), r.PathValue("id"))
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInUse):
		http.Redirect(w, r, "/admin/shipping?inuse=1", http.StatusSeeOther)
	default:
		h.log.WarnContext(r.Context(), "delete shipping zone", "error", err)
		h.notFound(w, r)
	}
}

// rejectShippingForm re-renders /admin/shipping at 422 with what was typed in it.
func (h *Handler) rejectShippingForm(
	w http.ResponseWriter, r *http.Request, errs map[string]string,
	method pages.AdminMethodDraft, zone pages.AdminZoneDraft, prefixes pages.AdminZonePrefixesDraft,
) {
	view, err := h.store.Shipping(r.Context())
	if err != nil {
		h.serverError(w, r)
		return
	}
	view.Errors, view.MethodDraft, view.ZoneDraft, view.PrefixDraft = errs, method, zone, prefixes
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, pages.AdminShipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

// dollars reads a whole-dollar amount; a blank or unparseable box is zero,
// which NewMethod.Validate reads per field.
func dollars(v string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Shipping serves GET /admin/shipping.
func (h *Handler) Shipping(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Shipping(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping configuration", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, pages.AdminShipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

// PublishShippingVersion serves POST /admin/shipping/version.
func (h *Handler) PublishShippingVersion(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	fee, feeErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("fee")), 10, 64)
	if feeErr != nil {
		http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
		return
	}
	// An empty threshold is "no free shipping" and not zero, and ParseInt
	// refuses "" rather than answering 0.
	var freeOver int64
	if raw := strings.TrimSpace(r.PostFormValue("free_over")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
			return
		}
		freeOver = parsed
	}

	err := h.store.PublishShippingVersion(r.Context(), ShippingVersion{
		MethodID: r.PostFormValue("method"),
		Name:     r.PostFormValue("name"),
		Carrier:  r.PostFormValue("carrier"),
		// Optional; the checkout's chooser reads them.
		NameEn:          r.PostFormValue("name_en"),
		CarrierEn:       r.PostFormValue("carrier_en"),
		FeeDollars:      fee,
		FreeOverDollars: freeOver,
	})
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

// SetZoneSurcharge serves POST /admin/shipping/surcharge.
func (h *Handler) SetZoneSurcharge(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	// An empty box means zero here, which CLEARS the surcharge: the field renders
	// blank when there is none, so submitting it untouched must be a no-op.
	var amount int64
	if raw := strings.TrimSpace(r.PostFormValue("amount")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
			return
		}
		amount = parsed
	}

	err := h.store.SetZoneSurcharge(r.Context(), r.PostFormValue("version"),
		r.PostFormValue("zone"), amount)
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

// redirectShipping turns a store error into the page's own answer.
func (h *Handler) redirectShipping(w http.ResponseWriter, r *http.Request, err error, ok string) {
	switch {
	case err == nil:
		http.Redirect(w, r, ok, http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/shipping?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipping change refused", "error", err)
		http.Redirect(w, r, "/admin/shipping?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change shipping", "error", err)
		h.serverError(w, r)
	}
}
