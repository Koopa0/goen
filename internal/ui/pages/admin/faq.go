package admin

import (
	"strconv"
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

// Translated reports whether this entry reads in English; the answer decides.
func (e FAQEntry) Translated() bool { return e.AnswerEn != "" }

// FAQView is the FAQ management page.
type FAQView struct {
	Rows   []FAQEntry
	Notice string
	Errors map[string]string
	// Draft is the add form's text.
	Draft FAQEntry
	// Edit and EditErrors are a refused edit of one listed entry, carried apart
	// from Draft and Errors so the refusal lands on that entry and not on the
	// add form, where fixing it would publish a second copy.
	Edit       FAQEntry
	EditErrors map[string]string
}

// faqEditFields are the fields an entry's own form can be refused on.
var faqEditFields = []string{"category", "question", "answer", "category_en", "question_en", "answer_en"}

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

// RowErr reports whether this entry's own form had the field refused.
func (v *FAQView) RowErr(e *FAQEntry, f string) bool {
	_, refused := v.EditErrors[f]
	return refused && v.Edit.ID == e.ID
}

// RowErrFields are the fields of this entry's form that were refused, in form order.
func (v *FAQView) RowErrFields(e *FAQEntry) []string {
	var out []string
	for _, f := range faqEditFields {
		if v.RowErr(e, f) {
			out = append(out, f)
		}
	}
	return out
}

// Empty reports whether the shop has published no FAQ at all.
func (v *FAQView) Empty() bool { return len(v.Rows) == 0 }

// HasNotice reports whether to show the banner.
func (v *FAQView) HasNotice() bool { return v.Notice != "" }

// HasErr reports whether a field was refused.
func (v *FAQView) HasErr(f string) bool { _, ok := v.Errors[f]; return ok }

// Err is why.
func (v *FAQView) Err(f string) string { return v.Errors[f] }

// Untranslated is how many entries an English visitor reads in Chinese.
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
