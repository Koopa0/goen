package site

import (
	"net/http"
	"strings"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/ui/pages"
	"github.com/koopa0/goen/internal/web"
)

func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	rows, err := h.content.FAQEntries(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		h.serverError(w, r)
		return
	}

	web.Render(w, r, h.log, http.StatusOK, pages.FAQ(
		layouts.Page{
			Title:       i18n.T(r.Context(), i18n.KeyFAQTitle),
			Description: i18n.T(r.Context(), i18n.KeyFAQDescription),
		}, faqView(rows)))
}

func faqView(rows []db.FAQEntriesRow) pages.FAQView {
	view := pages.FAQView{}
	var current *pages.FAQGroup
	var category string
	for i := range rows {
		e := &rows[i]
		if current == nil || category != e.CanonicalCategory {
			view.Groups = append(view.Groups, pages.FAQGroup{Category: e.Category})
			current = &view.Groups[len(view.Groups)-1]
			category = e.CanonicalCategory
		}
		current.Items = append(current.Items, pages.FAQItem{
			Question: e.Question, Answer: e.Answer,
		})
	}
	return view
}

// Shipping reads the same rows checkout charges from.
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

func (h *Handler) Policy(w http.ResponseWriter, r *http.Request) {
	doc, ok := policies[strings.TrimPrefix(r.URL.Path, "/")]
	if !ok {
		h.NotFound(w, r)
		return
	}
	doc = doc.For(i18n.FromContext(r.Context()))
	web.Render(w, r, h.log, http.StatusOK, pages.Policy(
		layouts.Page{Title: doc.Title, Description: doc.Summary}, doc))
}

func (h *Handler) serverError(w http.ResponseWriter, r *http.Request) {
	web.Render(w, r, h.log, http.StatusInternalServerError, pages.Notice(
		layouts.Page{Title: i18n.T(r.Context(), i18n.KeyTryAgainTitle)}, "500",
		i18n.T(r.Context(), i18n.KeyTryAgainTitle),
		i18n.T(r.Context(), i18n.KeyLoggedTryAgain)))
}
