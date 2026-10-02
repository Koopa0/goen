package staff

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/admin/access"
	"github.com/koopa0/goen/internal/admin/audit"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages/admin"
	"github.com/koopa0/goen/internal/web"
)

type Handler struct {
	store *Store
	log   *slog.Logger
	// factorsEnabled is whether the deployment has the key two-factor needs; the
	// page says so when it does not.
	factorsEnabled bool
}

func NewHandler(store *Store, log *slog.Logger, factorsEnabled bool) *Handler {
	if store == nil || log == nil {
		panic("staff: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log, factorsEnabled: factorsEnabled}
}

// Routes register under RequireAdmin and not RequireStaff, which accepts `staff`
// as well: these four promote, revoke, and strip an admin's second factor, and
// the listing names who has none yet.
func (h *Handler) Routes(mux *http.ServeMux, ac *access.Control) {
	mux.HandleFunc("GET /admin/staff", ac.RequireAdmin(h.Page))
	mux.HandleFunc("POST /admin/staff", ac.RequireAdmin(h.Add))
	mux.HandleFunc("POST /admin/staff/revoke", ac.RequireAdmin(h.Revoke))
	mux.HandleFunc("POST /admin/staff/factor", ac.RequireAdmin(h.RemoveFactor))
}

var notices = map[string]i18n.Key{
	"ok":          i18n.KeyAdminNoticeOK,
	"cleared":     i18n.KeyStaffCredentialCleared,
	"self":        i18n.KeyStaffSelf,
	"last":        i18n.KeyStaffLastAdmin,
	"needs":       i18n.KeyStaffNeeds,
	"notenrolled": i18n.KeyStaffInvalid,
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	view, err := h.store.Staff(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read staff 2FA status", "error", err)
		access.Fault(w, r, h.log)
		return
	}
	if !h.factorsEnabled {
		view.Notice = i18n.T(r.Context(), i18n.KeyTOTPNoKeyNotice)
	}
	view.Actor = actorID(r)
	// Only when there IS one: an unconditional assignment overwrites the
	// no-key warning set above with an empty string on an ordinary visit.
	if n := web.Notice(r, notices); n != "" {
		view.Notice = n
	}
	web.Render(w, r, h.log, http.StatusOK, admin.Staff(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageStaff)}, view))
}

func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	cleared, err := h.store.AddStaff(r.Context(),
		r.PostFormValue("email"), r.PostFormValue("name"), r.PostFormValue("role"))
	if errors.Is(err, ErrAlreadyStaff) {
		view, readErr := h.store.Staff(r.Context())
		if readErr != nil {
			h.log.ErrorContext(r.Context(), "read staff after refused add", "error", readErr)
			access.Fault(w, r, h.log)
			return
		}
		view.Actor = actorID(r)
		view.AddEmail = r.PostFormValue("email")
		view.AddName = r.PostFormValue("name")
		view.AddRole = admin.StaffRole(r.PostFormValue("role"))
		view.AddError = i18n.T(r.Context(), i18n.KeyStaffAlreadyExists)
		if !h.factorsEnabled {
			view.Notice = i18n.T(r.Context(), i18n.KeyTOTPNoKeyNotice)
		}
		web.Render(w, r, h.log, http.StatusUnprocessableEntity, admin.Staff(
			layouts.Page{Title: i18n.T(r.Context(), i18n.KeyAdminPageStaff)}, view))
		return
	}
	if err == nil && cleared {
		// A success the admin has to relay: the new colleague cannot get in
		// until they set a password through /forgot.
		http.Redirect(w, r, "/admin/staff?cleared=1", http.StatusSeeOther)
		return
	}
	h.redirect(w, r, err)
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirect(w, r, h.store.RevokeStaff(r.Context(), r.PostFormValue("user")))
}

func (h *Handler) RemoveFactor(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, i18n.T(r.Context(), i18n.KeyAdminBadForm), http.StatusBadRequest)
		return
	}
	h.redirect(w, r, h.store.RemoveFactor(r.Context(), r.PostFormValue("user")))
}

func actorID(r *http.Request) string {
	if id, ok := audit.Actor(r.Context()); ok {
		return id.String()
	}
	return ""
}

func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/admin/staff?ok=1", http.StatusSeeOther)
	case errors.Is(err, ErrSelf):
		http.Redirect(w, r, "/admin/staff?self=1", http.StatusSeeOther)
	case errors.Is(err, ErrLastAdmin):
		http.Redirect(w, r, "/admin/staff?last=1", http.StatusSeeOther)
	case errors.Is(err, ErrInvalidStaff):
		http.Redirect(w, r, "/admin/staff?needs=1", http.StatusSeeOther)
	case errors.Is(err, ErrNotEnrolled):
		http.Redirect(w, r, "/admin/staff?notenrolled=1", http.StatusSeeOther)
	default:
		h.log.ErrorContext(r.Context(), "change staff", "error", err)
		access.Fault(w, r, h.log)
	}
}
