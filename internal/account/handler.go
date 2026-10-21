package account

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// Handler serves the customer's own pages, each to the signed-in customer only:
// a visitor without a session is sent to sign in, whether or not the route is
// also behind auth's RequireUser.
type Handler struct {
	store *Store
	log   *slog.Logger
	// secure is the deployment's cookie flag: the password-setup receipt auth
	// writes is cleared here with the attributes it was written with.
	secure bool
	// takesPayment is the cart's TakesPayment, which decides whether an unpaid
	// order offers 付款.
	takesPayment bool
}

func NewHandler(store *Store, log *slog.Logger, secure, takesPayment bool) *Handler {
	if store == nil || log == nil {
		panic("account: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log, secure: secure, takesPayment: takesPayment}
}

// Routes registers the account's overview, orders, profile and addresses.
// requireUser sends a signed-out visitor to sign in and back; it is the
// caller's, because signing in is not this package's.
func (h *Handler) Routes(mux *http.ServeMux, requireUser func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /account", requireUser(h.Overview))
	mux.HandleFunc("GET /account/orders/{number}", requireUser(h.OrderPage))
	mux.HandleFunc("POST /account/profile", requireUser(h.UpdateProfile))
	mux.HandleFunc("POST /account/addresses", requireUser(h.AddAddress))
	mux.HandleFunc("GET /account/addresses/{address}/edit", requireUser(h.EditAddress))
	// The handler redirects an expired session without returning sign-in to a POST-only URL.
	mux.HandleFunc("POST /account/addresses/update", h.UpdateAddress)
	mux.HandleFunc("POST /account/addresses/default", requireUser(h.MakeDefaultAddress))
	mux.HandleFunc("POST /account/addresses/delete", requireUser(h.DeleteAddress))
}

// Refuse answers with the account page, its notice saying why, and 422: a
// request made from the page is refused on the page. auth's credential changes
// answer this way through [auth.Handler.RefuseOnAccountPage].
func (h *Handler) Refuse(w http.ResponseWriter, r *http.Request, notice string) {
	h.overview(w, r, http.StatusUnprocessableEntity, func(v *View) {
		v.Notice = notice
	})
}

func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	h.overview(w, r, http.StatusOK, nil)
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request, status int, refused func(*View)) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	view, err := h.store.Overview(r.Context(), u, r.URL.Query().Get("after"))
	if err != nil {
		h.log.ErrorContext(r.Context(), "read account", "error", err)
		h.serverError(w, r)
		return
	}
	// A failure here loses the badge and not the page, which is the safe direction.
	if state, vErr := h.store.EmailVerification(r.Context(), u.ID); vErr != nil {
		h.log.WarnContext(r.Context(), "read verification state", "error", vErr)
	} else {
		view.EmailVerified, view.PendingEmail = state.Verified, state.PendingEmail
	}
	view.Notice = accountNotice(r)
	view.EmailConfirmationRequested = r.URL.Query().Get("email") == "sent"
	view.EmailReentry = r.URL.Query().Get("email") == "reenter"
	if r.URL.Query().Get("welcome") == "1" {
		view.ReturnAfterWelcome, _ = siteurl.Path(r.URL.Query().Get("next"))
		view.CartAdjusted = r.URL.Query().Get("adjusted") == "1"
	}
	view.PaymentsEnabled = h.takesPayment
	if r.Method == http.MethodGet {
		completed := auth.ConsumePasswordSetupNotice(w, r, u, h.secure)
		view.PasswordSetupSent = completed && r.URL.Query().Get("password_setup") == "sent"
	}
	if refused != nil {
		refused(&view)
	}
	web.Render(w, r, h.log, status, Account(Meta(r.Context()), &view))
}

func accountNotice(r *http.Request) string {
	ctx := r.Context()
	q := r.URL.Query()
	switch {
	case q.Get("welcome") == "1":
		return i18n.T(ctx, i18n.KeyAccountWelcome)
	case q.Get("saved") == "1":
		return i18n.T(ctx, i18n.KeyProfileSaved)
	case q.Get("profile") == "invalid":
		return i18n.T(ctx, i18n.KeyProfileInvalid)
	}
	return auth.Notice(r)
}

func (h *Handler) OrderPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := user.FromContext(r.Context()); !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	number := r.PathValue("number")
	http.Redirect(w, r, "/orders/"+url.PathEscape(number), http.StatusSeeOther)
}

func (h *Handler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	if err := h.store.UpdateProfile(r.Context(), u.ID,
		r.PostFormValue("name"), r.PostFormValue("phone")); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			http.Redirect(w, r, "/account?profile=invalid", http.StatusSeeOther)
			return
		}
		h.log.ErrorContext(r.Context(), "update profile", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
		i18n.T(r.Context(), i18n.KeyBusyTitle),
		i18n.T(r.Context(), i18n.KeyBusyBody)))
}

func (h *Handler) AddAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	a := &Address{
		Label: r.PostFormValue("label"), Name: r.PostFormValue("name"),
		Phone: r.PostFormValue("phone"), PostalCode: r.PostFormValue("postal_code"),
		City: r.PostFormValue("city"), District: r.PostFormValue("district"),
		Street: r.PostFormValue("street"), Default: r.PostFormValue("default") == "1",
	}
	a.Trim()
	if errs := a.Validate(); len(errs) > 0 {
		h.refuseAddress(w, r, a, web.FieldMessages(r.Context(), errs), "")
		return
	}
	if err := h.store.AddAddress(r.Context(), u.ID, a); err != nil {
		if errors.Is(err, ErrInvalidInput) {
			h.refuseAddress(w, r, a, nil, i18n.T(r.Context(), i18n.KeyAddressIncomplete))
			return
		}
		h.log.ErrorContext(r.Context(), "add address", "error", err)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

func (h *Handler) refuseAddress(w http.ResponseWriter, r *http.Request, a *Address, errs map[string]string, notice string) {
	h.overview(w, r, http.StatusUnprocessableEntity, func(v *View) {
		v.AddressDraft = &AddressDraft{
			Label: a.Label, Name: a.Name, Phone: a.Phone, PostalCode: a.PostalCode,
			City: a.City, District: a.District, Street: a.Street, Default: a.Default,
		}
		v.AddressErrors = errs
		if notice != "" {
			v.Notice = notice
		}
	})
}

func (h *Handler) EditAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	a, err := h.store.addressForUser(r.Context(), u.ID, r.PathValue("address"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.addressNotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read address for edit", "error", err)
		h.serverError(w, r)
		return
	}
	h.overview(w, r, http.StatusOK, func(v *View) {
		v.AddressDraft = &AddressDraft{
			ID: a.ID, Label: a.Label, Name: a.Name, Phone: a.Phone,
			PostalCode: a.PostalCode, City: a.City, District: a.District, Street: a.Street, Default: a.Default,
		}
	})
}

func (h *Handler) UpdateAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	saved, err := h.store.addressForUser(r.Context(), u.ID, r.PostFormValue("address"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			h.addressNotFound(w, r)
			return
		}
		h.log.ErrorContext(r.Context(), "read address before edit", "error", err)
		h.serverError(w, r)
		return
	}
	a := &Address{
		Label: r.PostFormValue("label"), Name: r.PostFormValue("name"),
		Phone: r.PostFormValue("phone"), PostalCode: r.PostFormValue("postal_code"),
		City: r.PostFormValue("city"), District: r.PostFormValue("district"),
		Street: r.PostFormValue("street"), Default: saved.Default,
	}
	bounded := *a
	bounded.Trim()
	if errs := bounded.Validate(); len(errs) > 0 {
		h.refuseAddressEdit(w, r, saved.ID, a, web.FieldMessages(r.Context(), errs))
		return
	}
	switch err := h.store.updateAddress(r.Context(), u.ID, saved.ID, &bounded); {
	case err == nil:
		http.Redirect(w, r, "/account?saved=1#addresses-heading", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		h.addressNotFound(w, r)
	case errors.Is(err, ErrInvalidInput):
		h.refuseAddressEdit(w, r, saved.ID, a, web.FieldMessages(r.Context(), bounded.Validate()))
	default:
		h.log.ErrorContext(r.Context(), "update address", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) refuseAddressEdit(w http.ResponseWriter, r *http.Request, id string, a *Address, errs map[string]string) {
	h.overview(w, r, http.StatusUnprocessableEntity, func(v *View) {
		v.AddressDraft = &AddressDraft{
			ID: id, Label: a.Label, Name: a.Name, Phone: a.Phone, PostalCode: a.PostalCode,
			City: a.City, District: a.District, Street: a.Street, Default: a.Default,
		}
		v.AddressErrors = errs
	})
}

func (h *Handler) addressNotFound(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusNotFound, layouts.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyPageNotFoundTitle)}, "404",
		i18n.T(r.Context(), i18n.KeyPageNotFound), i18n.T(r.Context(), i18n.KeyPageNotFoundBody),
		layouts.NoticeActions{Primary: layouts.NoticeLink{Href: "/account#addresses-heading", Label: i18n.KeyAddresses}}))
}

func (h *Handler) MakeDefaultAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	switch err := h.store.MakeDefaultAddress(r.Context(), u.ID, r.PostFormValue("address")); {
	case err == nil:
		http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotFound):
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "set default address", "error", err)
		h.serverError(w, r)
	}
}

func (h *Handler) DeleteAddress(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin", http.StatusSeeOther)
		return
	}
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteAddress(r.Context(), u.ID, r.PostFormValue("address")); err != nil {
		if !errors.Is(err, ErrNotFound) {
			h.log.ErrorContext(r.Context(), "delete address", "error", err)
			h.serverError(w, r)
			return
		}
	}
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}
