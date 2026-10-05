package coupons

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/coupon"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/money"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("coupons: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/coupons", ac.RequireStaff(h.Page))
	mux.HandleFunc("POST /admin/coupons", ac.RequireStaff(h.Create))
	mux.HandleFunc("POST /admin/coupons/{code}/active", ac.RequireStaff(h.SetActive))
}

var notices = map[string]web.Message{
	"ok":      web.Done(i18n.KeyAdminNoticeOK),
	"refused": web.Refused(i18n.KeyAdminNoticeRefused),
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Coupons(r.Context(), r.URL.Query().Get(web.KeysetParam))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read coupons", "error", err)
		access.ServerError(w, r, h.log)
		return
	}
	view.Notice = web.Notice(r, notices)
	web.Render(w, r, h.log, http.StatusOK, admin.Coupons(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCoupons)}, view))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	f := formOf(r)

	errs, err := h.store.CreateCoupon(r.Context(), f)
	switch {
	case err != nil:
		h.log.ErrorContext(r.Context(), "create coupon", "error", err)
		access.ServerError(w, r, h.log)
	case len(errs) > 0:
		view, readErr := h.store.Coupons(r.Context(), r.URL.Query().Get(web.KeysetParam))
		if readErr != nil {
			access.ServerError(w, r, h.log)
			return
		}
		view.Errors = errs
		view.Draft = admin.CouponDraft{
			Code: f.Code, Description: f.Description, Kind: f.Kind,
			Value:       r.PostFormValue("value"),
			Cap:         r.PostFormValue("cap"),
			MinSpend:    r.PostFormValue("min"),
			MaxRedeem:   r.PostFormValue("max"),
			PerCustomer: r.PostFormValue("percustomer"),
			Days:        r.PostFormValue("days"),
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Coupons(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageCoupons)}, view))
	default:
		http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
	}
}

func formOf(r *http.Request) *Form {
	f := &Form{
		Code:         r.PostFormValue("code"),
		Description:  r.PostFormValue("description"),
		Kind:         coupon.Kind(r.PostFormValue("kind")),
		parseInvalid: map[string]bool{},
	}
	wholeFields := []struct {
		name string
		dst  *int64
	}{{"value", &f.Value}, {"cap", &f.CapDollars}, {"min", &f.MinSpendDollars}}
	for _, field := range wholeFields {
		value, ok := whole(r.PostFormValue(field.name))
		*field.dst = value
		if !ok {
			f.parseInvalid[field.name] = true
		}
	}
	smallFields := []struct {
		name string
		dst  *int32
	}{{"max", &f.MaxRedemptions}, {"percustomer", &f.PerCustomer}, {"days", &f.Days}}
	for _, field := range smallFields {
		value, ok := web.ParseCount(r.PostFormValue(field.name))
		*field.dst = value
		if !ok {
			f.parseInvalid[field.name] = true
		}
	}
	// An unfilled per-customer box means the schema's own default, not zero.
	if strings.TrimSpace(r.PostFormValue("percustomer")) == "" {
		f.PerCustomer = 1
	}
	return f
}

func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	if err := h.store.SetCouponActive(r.Context(), r.PathValue("code"),
		r.PostFormValue("active") == "true"); err != nil {
		h.log.WarnContext(r.Context(), "set coupon active", "error", err)
		http.Redirect(w, r, "/admin/coupons?refused=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/coupons?ok=1", http.StatusSeeOther)
}

func whole(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && n <= money.MaxCents/100
}
