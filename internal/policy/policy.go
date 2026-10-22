// Package policy is what the shop states publicly about how it works; it holds
// the policy documents, the shipping page, the stated windows and thresholds
// (Rules) with the stat line the home, department and product pages draw them
// in, and the retention texts; it does not hold the rules that decide a return
// (returns), the FAQ (site) or the CSP (cmd/goen).
//
// It states and does not decide: each number it shows is read from where it is
// enforced, a database function or the stored delivery methods, or restates a
// constant a sweep enforces in another package, where a test binds the two.
package policy

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

type Handler struct {
	store *Store
	log   *slog.Logger
}

func NewHandler(store *Store, log *slog.Logger) *Handler {
	if store == nil || log == nil {
		panic("policy: NewHandler requires a store and a logger")
	}
	return &Handler{store: store, log: log}
}

// Routes registers the shipping page and the policy documents, each document
// at its key in policies.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /shipping", h.Shipping)
	mux.HandleFunc("GET /returns", h.DocPage)
	mux.HandleFunc("GET /payment", h.DocPage)
	mux.HandleFunc("GET /warranty", h.DocPage)
	mux.HandleFunc("GET /privacy", h.DocPage)
	mux.HandleFunc("GET /terms", h.DocPage)
}

// Shipping reads the same rows checkout charges from.
func (h *Handler) Shipping(w http.ResponseWriter, r *http.Request) {
	methods, err := h.store.ShippingMethods(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping policy", "error", err)
		h.serverError(w, r)
		return
	}
	windows, err := h.store.CheckoutWindows(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read checkout windows", "error", err)
		h.serverError(w, r)
		return
	}
	view := ShippingView{Methods: methods, Windows: windows}
	respond.Component(w, r, h.log, http.StatusOK, Shipping(
		frame.Page{
			Title:       i18n.T(r.Context(), i18n.KeyShippingTitle),
			Description: i18n.T(r.Context(), i18n.KeyShippingDescription),
		}, view))
}

func (h *Handler) DocPage(w http.ResponseWriter, r *http.Request) {
	doc, ok := policies[strings.TrimPrefix(r.URL.Path, "/")]
	if !ok {
		h.notFound(w, r)
		return
	}
	var windows CheckoutWindows
	if doc.statesWindows() {
		var err error
		if windows, err = h.store.CheckoutWindows(r.Context()); err != nil {
			h.log.ErrorContext(r.Context(), "read checkout windows", "error", err)
			h.serverError(w, r)
			return
		}
	}
	doc = doc.For(i18n.FromContext(r.Context()), windows)
	respond.Component(w, r, h.log, http.StatusOK, DocPage(
		frame.Page{Title: doc.Title, Description: doc.Summary}, doc))
}

// notFound is the shop's 404 page, as site.Handler.NotFound renders it. Only a
// path routed to DocPage without a document reaches it, which
// TestEveryPolicyRouteHasADocument refuses.
func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusNotFound, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyPageNotFoundTitle)},
		"404",
		i18n.T(r.Context(), i18n.KeyPageNotFound),
		i18n.T(r.Context(), i18n.KeyPageNotFoundBody),
	))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	respond.Component(w, r, h.log, http.StatusInternalServerError, frame.Notice(
		frame.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "500",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
}
