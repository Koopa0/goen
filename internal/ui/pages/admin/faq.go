package admin

import (
	"strconv"

	"github.com/koopa0/goen/internal/ui/components"
)

type FAQEntry struct {
	ID         string
	Category   string
	Question   string
	Answer     string
	CategoryEn string
	QuestionEn string
	AnswerEn   string
	UpdatedAt  string
}

func (e FAQEntry) Translated() bool { return e.AnswerEn != "" }

type FAQView struct {
	Rows   []FAQEntry
	Notice components.Result
	Errors map[string]string
	Draft  FAQEntry
	// Edit and EditErrors are a refused edit of one listed entry, carried apart
	// from Draft and Errors so the refusal lands on that entry and not on the
	// add form, where fixing it would publish a second copy.
	Edit       FAQEntry
	EditErrors map[string]string
}

// Listed is the entry as its own form shows it: the refused submission when
// this is the entry whose edit was refused, otherwise what is stored.
func (v *FAQView) Listed(e *FAQEntry) FAQEntry {
	if v.Edit.ID != e.ID {
		return *e
	}
	out := v.Edit
	out.UpdatedAt = e.UpdatedAt
	return out
}

func (v *FAQView) RowErr(e *FAQEntry, f string) bool {
	_, refused := v.EditErrors[f]
	return refused && v.Edit.ID == e.ID
}

func (v *FAQView) Empty() bool { return len(v.Rows) == 0 }

func (v *FAQView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

func (v *FAQView) Err(f string) string { return v.Errors[f] }

func (v *FAQView) Untranslated() int {
	n := 0
	for i := range v.Rows {
		if !v.Rows[i].Translated() {
			n++
		}
	}
	return n
}

func untranslatedText(v *FAQView) string { return strconv.Itoa(v.Untranslated()) }
