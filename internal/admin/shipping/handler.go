package shipping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/carrier"
	"github.com/koopa0/goen/internal/destination"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store         *Store
	pickupOffered bool
	log           *slog.Logger
}

// NewHandler takes whether checkout offers a convenience-store map; the page
// marks pickup-point methods as not offered when it does not.
func NewHandler(store *Store, pickupOffered bool, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("shipping: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, pickupOffered: pickupOffered, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/shipping", ac.RequireStaff(h.Page))
	mux.HandleFunc("POST /admin/shipping/version", ac.RequireStaff(h.PublishVersion))
	mux.HandleFunc("POST /admin/shipping/surcharge", ac.RequireStaff(h.SetZoneSurcharge))
	mux.HandleFunc("POST /admin/shipping/method", ac.RequireStaff(h.CreateMethod))
	mux.HandleFunc("POST /admin/shipping/method/{id}/active", ac.RequireStaff(h.SetMethodActive))
	mux.HandleFunc("POST /admin/shipping/zone", ac.RequireStaff(h.CreateZone))
	mux.HandleFunc("POST /admin/shipping/zone/{id}/prefixes", ac.RequireStaff(h.SetZonePrefixes))
	mux.HandleFunc("POST /admin/shipping/zone/{id}/delete", ac.RequireStaff(h.DeleteZone))
}

var notices = map[string]web.NoticeEntry{
	"ok":           web.Done(i18n.KeyAdminNoticeOK),
	"refused":      web.Refused(i18n.KeyAdminShipRefused),
	"zoneprefixes": web.Refused(i18n.KeyAdminShipZoneHasPrefixes),
	"zoneversions": web.Refused(i18n.KeyAdminShipZoneHasVersions),
}

func (h *Handler) CreateMethod(w http.ResponseWriter, r *http.Request) {
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
		access.ServerError(w, r, h.log)
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
		value, ok := web.ParseBounded(raw, max)
		if !ok {
			errs[field] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodParcelLimit), max)
		}
		return value
	}
	fee, feeOK := dollars(draft.Fee, false)
	if !feeOK {
		errs["fee"] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodFee), money.TWD(MaxFee))
	}
	freeOver, freeOverOK := dollars(draft.FreeOver, true)
	if !freeOverOK {
		errs["free_over"] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodFreeOver), money.TWD(money.MaxCents))
	}
	return &NewMethod{
		Code:               r.PostFormValue("code"),
		Destination:        r.PostFormValue("destination"),
		MaxParcelLongestMM: parse(draft.MaxLongest, carrier.MaxParcelLongestMM, "max_parcel_longest"),
		MaxParcelSumMM:     parse(draft.MaxSum, carrier.MaxParcelSumMM, "max_parcel_sum"),
		MaxParcelWeightG:   parse(draft.MaxWeight, carrier.MaxParcelWeightG, "max_parcel_weight"),
		Name:               r.PostFormValue("name"),
		NameEn:             r.PostFormValue("name_en"),
		Carrier:            r.PostFormValue("carrier"),
		CarrierEn:          r.PostFormValue("carrier_en"),
		FeeDollars:         fee,
		FreeOverDollars:    freeOver,
	}, draft, errs
}

// SetMethodActive switches a method OFF, never deletes it: past orders name their version.
func (h *Handler) SetMethodActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.SetMethodActive(r.Context(), r.PathValue("id"),
		r.PostFormValue("active") == "1")
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "toggle shipping method", "error", err)
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "toggle shipping method", "error", err)
		http.Redirect(w, r, "/admin/shipping?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "toggle shipping method", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) CreateZone(w http.ResponseWriter, r *http.Request) {
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
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, &shippingDrafts{zone: admin.ZoneDraft{
			Code: r.PostFormValue("code"), Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en"), Prefixes: r.PostFormValue("prefixes"),
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
		access.NotFound(w, r, h.log)
	case err != nil:
		h.log.ErrorContext(r.Context(), "set zone prefixes", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		h.rejectShippingForm(w, r, errs, &shippingDrafts{prefixes: admin.ZonePrefixesDraft{
			ZoneID: zoneID, Prefixes: prefixes,
		}})
	default:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	}
}

func (h *Handler) DeleteZone(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	err := h.store.DeleteZone(r.Context(), r.PathValue("id"))
	if inUse, ok := errors.AsType[*ZoneInUseError](err); ok {
		switch inUse.Use {
		case ZoneUsedByPrefixes:
			http.Redirect(w, r, "/admin/shipping?zoneprefixes=1", http.StatusSeeOther)
		case ZoneUsedByVersions:
			http.Redirect(w, r, "/admin/shipping?zoneversions=1", http.StatusSeeOther)
		default:
			panic("shipping: unknown zone use: " + string(inUse.Use))
		}
		return
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.log.WarnContext(r.Context(), "delete shipping zone", "error", err)
		access.NotFound(w, r, h.log)
	case errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "delete shipping zone", "error", err)
		http.Redirect(w, r, "/admin/shipping?refused=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "delete shipping zone", "error", err)
		access.ServerError(w, r, h.log)
	}
}

type shippingDrafts struct {
	method   admin.MethodDraft
	zone     admin.ZoneDraft
	prefixes admin.ZonePrefixesDraft
	version  *admin.VersionDraft
}

func (h *Handler) rejectShippingForm(
	w http.ResponseWriter, r *http.Request, errs map[string]string, drafts *shippingDrafts,
) {
	view, err := h.shippingView(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	if drafts.version != nil {
		found := false
		for i := range view.Methods {
			if view.Methods[i].MethodID == drafts.version.MethodID {
				found = true
				break
			}
		}
		if !found {
			access.NotFound(w, r, h.log)
			return
		}
	}
	view.Errors = errs
	view.MethodDraft, view.ZoneDraft, view.PrefixDraft = drafts.method, drafts.zone, drafts.prefixes
	if drafts.version != nil {
		view.VersionDraft = *drafts.version
	}
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
	view, err := h.store.Configuration(ctx)
	if err != nil {
		return view, err
	}
	for i := range view.Methods {
		m := &view.Methods[i]
		m.PickupUnavailable = m.Destination == destination.PickupPoint && !h.pickupOffered
	}
	return view, nil
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.shippingView(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping configuration", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Shipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}

func (h *Handler) PublishVersion(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	v, draft, errs := versionFormOf(r)
	if len(errs) > 0 {
		h.rejectShippingForm(w, r, errs, &shippingDrafts{version: &draft})
		return
	}
	err := h.store.PublishShippingVersion(r.Context(), v)
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipping version refused", "error", err)
		h.rejectShippingForm(w, r, map[string]string{"version_form": i18n.T(r.Context(), i18n.KeyAdminShipRefused)}, &shippingDrafts{version: &draft})
	default:
		h.log.ErrorContext(r.Context(), "publish shipping version", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func versionFormOf(r *http.Request) (ShippingVersion, admin.VersionDraft, map[string]string) {
	draft := admin.VersionDraft{
		MethodID: r.PostFormValue("method"), Name: r.PostFormValue("name"), NameEn: r.PostFormValue("name_en"),
		Carrier: r.PostFormValue("carrier"), CarrierEn: r.PostFormValue("carrier_en"),
		Fee: r.PostFormValue("fee"), FreeOver: r.PostFormValue("free_over"),
	}
	errs := map[string]string{}
	fee, feeOK := dollars(draft.Fee, false)
	if !feeOK || fee > MaxFee/100 {
		errs["version_fee"] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodFee), money.TWD(MaxFee))
	}
	freeOver, freeOverOK := dollars(draft.FreeOver, true)
	if !freeOverOK || freeOver > money.MaxCents/100 {
		errs["version_free_over"] = fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormMethodFreeOver), money.TWD(money.MaxCents))
	}
	if name := strings.TrimSpace(draft.Name); name == "" || utf8.RuneCountInString(name) > MaxNameRunes {
		errs["version_name"] = i18n.T(r.Context(), i18n.KeyFormNameRequired)
	}
	return ShippingVersion{MethodID: draft.MethodID, Name: draft.Name, NameEn: draft.NameEn,
		Carrier: draft.Carrier, CarrierEn: draft.CarrierEn, FeeDollars: fee, FreeOverDollars: freeOver}, draft, errs
}

func (h *Handler) SetZoneSurcharge(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	// Blank clears the surcharge; it must stay distinct from an unreadable amount.
	amount, valid := dollars(r.PostFormValue("amount"), true)
	if !valid || amount > MaxFee/100 {
		h.rejectSurcharge(w, r, fmt.Sprintf(i18n.T(r.Context(), i18n.KeyFormShippingSurcharge), money.TWD(MaxFee)))
		return
	}
	err := h.store.SetZoneSurcharge(r.Context(), r.PostFormValue("version"), r.PostFormValue("zone"), amount)
	if changed, _ := errors.AsType[*VersionChangedError](err); changed != nil {
		h.rejectSurcharge(w, r, i18n.T(r.Context(), i18n.KeyAdminShipVersionChanged))
		return
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/shipping?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrRefused):
		h.log.WarnContext(r.Context(), "shipping surcharge refused", "error", err)
		h.rejectSurcharge(w, r, i18n.T(r.Context(), i18n.KeyAdminShipRefused))
	default:
		h.log.ErrorContext(r.Context(), "set shipping surcharge", "error", err)
		access.ServerError(w, r, h.log)
	}
}

func (h *Handler) rejectSurcharge(w http.ResponseWriter, r *http.Request, message string) {
	view, err := h.shippingView(r.Context())
	if err != nil {
		access.ServerError(w, r, h.log)
		return
	}
	versionID, zoneID := r.PostFormValue("version"), r.PostFormValue("zone")
	methodID, err := h.store.VersionMethod(r.Context(), versionID)
	if errors.Is(err, ErrNotFound) {
		access.NotFound(w, r, h.log)
		return
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "read surcharge version method", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	for i := range view.Methods {
		if view.Methods[i].MethodID == methodID && view.Methods[i].VersionID != versionID {
			message = i18n.T(r.Context(), i18n.KeyAdminShipVersionChanged)
			break
		}
	}
	zoneFound := false
	for i := range view.Zones {
		if view.Zones[i].ID == zoneID {
			zoneFound = true
			break
		}
	}
	if !zoneFound {
		access.NotFound(w, r, h.log)
		return
	}
	view.SurchargeDraft = admin.SurchargeDraft{MethodID: methodID, ZoneID: zoneID, Amount: r.PostFormValue("amount")}
	view.Errors = map[string]string{"surcharge": message}
	web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Shipping(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageShipping)}, view))
}
