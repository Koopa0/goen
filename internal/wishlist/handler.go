package wishlist

import (
	"log/slog"
	"net/http"

	"github.com/koopa0/goen/internal/auth"
	"github.com/koopa0/goen/internal/cart/cartitem"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/user"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/respond"
	"github.com/koopa0/goen/internal/web/siteurl"
)

// Handler serves the wishlist to the signed-in customer only: a visitor
// without a session is sent to sign in, whether or not the route is also
// behind auth's RequireUser.
type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("wishlist: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Routes registers the wishlist page and its save and remove. requireUser
// sends a signed-out visitor to sign in and back; it is the caller's, because
// signing in is not this package's.
func (h *Handler) Routes(mux *http.ServeMux, requireUser func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /account/wishlist", requireUser(h.Page))
	mux.HandleFunc("POST /account/wishlist", requireUser(h.Update))
}

func (h *Handler) Page(w http.ResponseWriter, r *http.Request) {
	u, ok := user.FromContext(r.Context())
	if !ok {
		http.Redirect(w, r, "/signin?next=/account/wishlist", http.StatusSeeOther)
		return
	}
	tiles, err := h.store.Items(r.Context(), u.ID)
	if err != nil {
		h.log.ErrorContext(r.Context(), "read wishlist", "error", err)
		h.serverError(w, r)
		return
	}
	respond.Component(w, r, h.log, http.StatusOK, Page(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyWishlistTitle)}, View{Products: tiles, Added: cartitem.AddOutcome(r.URL.Query().Get("added"))}))
}

// Update saves the posted product, or removes it when action is "remove", and
// goes back to the page named in return.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400 "+i18n.T(r.Context(), i18n.KeyFormUnreadable), http.StatusBadRequest)
		return
	}
	// siteurl.PathOr refuses anything but a same-site path, which makes a
	// redirect target read off a form safe.
	back := siteurl.PathOr(r.PostFormValue("return"), "/account/wishlist")

	u, ok := user.FromContext(r.Context())
	if !ok {
		// Authentication does not replay writes; this redirect only preserves
		// the page for callers outside the authenticated route wrapper.
		//nolint:gosec // G710: the fixed /signin path; back, bounded by siteurl.PathOr above, is only its escaped query
		http.Redirect(w, r, auth.SignInPath(back), http.StatusSeeOther)
		return
	}

	slug := r.PostFormValue("slug")
	var err error
	if r.PostFormValue("action") == "remove" {
		err = h.store.Remove(r.Context(), u.ID, slug)
	} else {
		err = h.store.Save(r.Context(), u.ID, slug)
	}
	if err != nil {
		h.log.ErrorContext(r.Context(), "update wishlist", "error", err, "slug", slug)
		h.serverError(w, r)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyBusyTitle)}, "",
		i18n.T(r.Context(), i18n.KeyBusyTitle),
		i18n.T(r.Context(), i18n.KeyBusyBody)))
}
