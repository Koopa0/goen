package site

import (
	"net/http"
	"strings"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

// FAQ serves GET /faq.
//
// Read from faq_entries rather than written into the template, so the back
// office can answer a question that keeps arriving without a deploy — which is
// the difference between an FAQ that is maintained and one that is a snapshot
// of what somebody once assumed people would ask.
func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	rows, err := h.content.FAQEntries(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		h.serverError(w, r)
		return
	}

	view := pages.FAQView{}
	var current *pages.FAQGroup
	for i := range rows {
		e := &rows[i]
		if current == nil || current.Category != e.Category {
			view.Groups = append(view.Groups, pages.FAQGroup{Category: e.Category})
			current = &view.Groups[len(view.Groups)-1]
		}
		current.Items = append(current.Items, pages.FAQItem{
			Question: e.Question, Answer: e.Answer,
		})
	}
	web.Render(w, r, h.log, http.StatusOK, pages.FAQ(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyFAQTitle),
			Description: i18n.T(r.Context(), i18n.KeyFAQDescription),
		}, view))
}

// Shipping serves GET /shipping.
//
// The fees come from the database — the same rows checkout charges from — so
// the page cannot promise a figure the till does not honour. A policy page that
// restates a number is a policy page that eventually contradicts one.
func (h *Handler) Shipping(w http.ResponseWriter, r *http.Request) {
	methods, err := h.content.ShippingPolicy(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read shipping policy", "error", err)
		h.serverError(w, r)
		return
	}
	view := pages.ShippingView{Methods: methods}
	web.Render(w, r, h.log, http.StatusOK, pages.Shipping(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyShippingTitle),
			Description: i18n.T(r.Context(), i18n.KeyShippingDescription),
		}, view))
}

// Policy serves the remaining static policy pages.
//
// One handler over a table rather than six near-identical ones: they differ in
// their words and in nothing else, and six copies of the same render call is
// six places to fix a layout change.
func (h *Handler) Policy(w http.ResponseWriter, r *http.Request) {
	// Keyed on the request path rather than a wildcard segment: each policy has
	// its own literal route, so a URL that is not one of them never reaches
	// here — it falls through to the catch-all 404 like any other unknown path.
	doc, ok := policies[strings.TrimPrefix(r.URL.Path, "/")]
	if !ok {
		h.NotFound(w, r)
		return
	}
	// Resolved to the visitor's own language. These pages were Chinese for every
	// reader until /returns began stating 消保法 §19 — a right an English-reading
	// customer in Taiwan holds identically, on a page they could not read.
	doc = doc.For(i18n.FromContext(r.Context()))
	web.Render(w, r, h.log, http.StatusOK, pages.Policy(
		layouts.Page{Title: doc.Title, Description: doc.Summary}, doc))
}

// serverError renders the 500 page.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
}
