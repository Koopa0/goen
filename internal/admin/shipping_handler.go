package admin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

func (h *Handler) CreateShippingMethod(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	m, draft, errs := methodFormOf(r)
	maps.Copy(errs, m.Validate(r.Context()))
	if len(errs) > 0 {
		h.rejectShippingForm(w, r, errs, &shippingDrafts{method: draft})
		return
	}
	errs, err := h.store.CreateMethod(r.Context(), m)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create shipping method", "error", err)
		h.serverError(w, r)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, &shippingDrafts{method: draft})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

func methodFormOf(r *http.Request) (*NewMethod, admin.MethodDraft, map[string]string) {
	draft := admin.MethodDraft{
		Code: r.PostFormValue("code"), Destination: r.PostFormValue("destination"),
		Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en"),
		Carrier: r.PostFormValue("carrier"), CarrierEn: r.PostFormValue("carrier_en"),
		Fee: r.PostFormValue("fee"), FreeOver: r.PostFormValue("free_over"),
		MaxLongest: r.PostFormValue("max_parcel_longest"),
		MaxSum:     r.PostFormValue("max_parcel_sum"),
		MaxWeight:  r.PostFormValue("max_parcel_weight"),
	}
	errs := map[string]string{}
	parse := func(raw string, max int32, field string) int32 {
		value, ok := parseBoundedInt(raw, max)
		if !ok {
			errs[field] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodParcelLimit), max)
		}
		return value
	}
	fee, feeOK := dollars(draft.Fee, false)
	if !feeOK {
		errs["fee"] = i18n.T(r.Context(), i18n.KeyFormMethodFee)
	}
	freeOver, freeOverOK := dollars(draft.FreeOver, true)
	if !freeOverOK {
		errs["free_over"] = i18n.T(r.Context(), i18n.KeyFormMethodFreeOver)
	}
	return &NewMethod{
		Code:               r.PostFormValue("code"),
		Destination:        r.PostFormValue("destination"),
		MaxParcelLongestMM: parse(draft.MaxLongest, parcelLongestCeilingMM, "max_parcel_longest"),
		MaxParcelSumMM:     parse(draft.MaxSum, parcelSumCeilingMM, "max_parcel_sum"),
		MaxParcelWeightG:   parse(draft.MaxWeight, parcelWeightCeilingG, "max_parcel_weight"),
		Name:               r.PostFormValue("name"),
		NameEn:             r.PostFormValue("name_en"),
		Carrier:            r.PostFormValue("carrier"),
		CarrierEn:          r.PostFormValue("carrier_en"),
		FeeDollars:         fee,
		FreeOverDollars:    freeOver,
	}, draft, errs
}

// SetShippingMethodActive switches a method OFF, never deletes it: past orders name their version.
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
		h.rejectShippingForm(w, r, errs, &shippingDrafts{zone: admin.ZoneDraft{
			Code: z.Code, Name: z.Name, NameEn: z.NameEn, Prefixes: z.Prefixes,
		}})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

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
		h.rejectShippingForm(w, r, errs, &shippingDrafts{prefixes: admin.ZonePrefixesDraft{
			ZoneID: zoneID, Prefixes: prefixes,
		}})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

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

type shippingDrafts struct {
	method   admin.MethodDraft
	zone     admin.ZoneDraft
	prefixes admin.ZonePrefixesDraft
}

func (h *Handler) rejectShippingForm(
	w http.ResponseWriter, r *http.Request, errs map[string]string, drafts *shippingDrafts,
) {
	view, err := h.shippingView(r.Context())
	if err != nil {
		h.serverError(w, r)
		return
	}
	view.Errors = errs
	view.MethodDraft, view.ZoneDraft, view.PrefixDraft = drafts.method, drafts.zone, drafts.prefixes
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Shipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

// dollars reads a non-negative whole-dollar amount. Optional blanks mean zero;
// malformed, negative and overflowing input remain distinguishable from zero.
func dollars(v string, blankOK bool) (int64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, blankOK
	}
	n, err := strconv.ParseInt(v, 10, 64)
	return n, err == nil && n >= 0
}

// shippingView is the configuration with each pickup-point method marked as not
// offered where checkout hides it: a method listed here with a disable button
// reads as live, and the same condition as the checkout is what keeps it true.
func (h *Handler) shippingView(ctx context.Context) (admin.ShippingView, error) {
	view, err := h.store.Shipping(ctx)
	if err != nil {
		return view, err
	}
	for i := range view.Methods {
		m := &view.Methods[i]
		m.PickupUnavailable = m.Destination == destination.PickupPoint && !h.storeMap.Enabled()
	}
	return view, nil
}

func (h *Handler) Shipping(w http.ResponseWriter, r *http.Request) {
	view, err := h.shippingView(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping configuration", "error", err)
		h.serverError(w, r)
		return
	}
	view.Notice = noticeFor(r)
	web.Render(w, r, h.log, http.StatusOK, admin.Shipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

func (h *Handler) PublishShippingVersion(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	fee, feeErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("fee")), 10, 64)
	if feeErr != nil {
		http.Redirect(w, r, "/admin/shipping?shippingneeds=1", http.StatusSeeOther)
		return
	}
	// An empty threshold is "no free shipping" and not zero, and ParseInt
	// refuses "" rather than answering 0.
	var freeOver int64
	if raw := strings.TrimSpace(r.PostFormValue("free_over")); raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			http.Redirect(w, r, "/admin/shipping?shippingneeds=1", http.StatusSeeOther)
			return
		}
		freeOver = parsed
	}

	err := h.store.PublishShippingVersion(r.Context(), ShippingVersion{
		MethodID:        r.PostFormValue("method"),
		Name:            r.PostFormValue("name"),
		Carrier:         r.PostFormValue("carrier"),
		NameEn:          r.PostFormValue("name_en"),
		CarrierEn:       r.PostFormValue("carrier_en"),
		FeeDollars:      fee,
		FreeOverDollars: freeOver,
	})
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

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
			http.Redirect(w, r, "/admin/shipping?shippingneeds=1", http.StatusSeeOther)
			return
		}
		amount = parsed
	}

	err := h.store.SetZoneSurcharge(r.Context(), r.PostFormValue("version"),
		r.PostFormValue("zone"), amount)
	h.redirectShipping(w, r, err, "/admin/shipping?ok=1")
}

func (h *Handler) redirectShipping(w http.ResponseWriter, r *http.Request, err error, ok string) {
	switch {
	case err == nil:
		http.Redirect(w, r, ok, http.StatusSeeOther)
	case errors.Is(err, ErrInvalid):
		http.Redirect(w, r, "/admin/shipping?shippingneeds=1", http.StatusSeeOther)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipping change refused", "error", err)
		http.Redirect(w, r, "/admin/shipping?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change shipping", "error", err)
		h.serverError(w, r)
	}
}
