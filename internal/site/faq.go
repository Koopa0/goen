package site

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/koopa0/goen/internal/db"
	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/web/respond"
)

func (h *Handler) FAQ(w http.ResponseWriter, r *http.Request) {
	rows, err := h.content.FAQEntries(r.Context())
	if err != nil {
		h.log.ErrorContext(r.Context(), "read faq", "error", err)
		h.serverError(w, r)
		return
	}

	respond.Component(w, r, h.log, http.StatusOK, FAQ(
		frame.Page{
			Title:       i18n.T(r.Context(), i18n.KeyFAQTitle),
			Description: i18n.T(r.Context(), i18n.KeyFAQDescription),
		}, faqView(rows)))
}

// faqView groups the rows by category row, never by the name it shows: two
// headings may read the same in one language and are still two headings.
func faqView(rows []db.FAQEntriesRow) FAQView {
	view := FAQView{}
	var current *FAQGroup
	var category uuid.UUID
	for i := range rows {
		e := &rows[i]
		if current == nil || category != e.CategoryID {
			view.Groups = append(view.Groups, FAQGroup{Category: e.Category})
			current = &view.Groups[len(view.Groups)-1]
			category = e.CategoryID
		}
		current.Items = append(current.Items, FAQItem{
			Question: e.Question, Answer: e.Answer,
		})
	}
	return view
}

type FAQItem struct {
	Question string
	Answer   string
}

// faqGroupID names a category by position, as the policy pages name their
// sections, since a heading on the page has no identifier of its own; the
// contents list uses the same numbers.
func faqGroupID(i int) string { return "faq-group-" + strconv.Itoa(i+1) }

func faqGroupHref(i int) string { return "#" + faqGroupID(i) }

type FAQGroup struct {
	Category string
	Items    []FAQItem
}

type FAQView struct {
	Groups []FAQGroup
}

func (v FAQView) Empty() bool { return len(v.Groups) == 0 }
