package content

import (
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/components"
)

// FAQEntry is a listed entry, or the add form's draft. Category is the listed
// entry's category name; CategoryID is the category the draft chose.
type FAQEntry struct {
	ID         string
	CategoryID string
	Category   string
	Question   string
	Answer     string
	QuestionEn string
	AnswerEn   string
	UpdatedAt  string
}

// FAQCategory is a heading on /faq. InUse holds it: the database refuses to
// delete a category that still has entries, so the desk does not offer it.
type FAQCategory struct {
	ID     string
	Name   string
	NameEn string
	InUse  bool
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

	Categories []FAQCategory
	// CategoryDraft and CategoryErrors are a refused category form: the add
	// form when CategoryDraft.ID is empty, else the listed category it names.
	CategoryDraft  FAQCategory
	CategoryErrors map[string]string
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

// ListedCategory is the category as its own form shows it: the refused
// submission when this is the category whose rename was refused.
func (v *FAQView) ListedCategory(c *FAQCategory) FAQCategory {
	if v.CategoryDraft.ID != c.ID {
		return *c
	}
	out := v.CategoryDraft
	out.InUse = c.InUse
	return out
}

// CategoryErr is the refusal of field f on the category form id names ("" for
// the add form).
func (v *FAQView) CategoryErr(id, f string) (string, bool) {
	if v.CategoryDraft.ID != id {
		return "", false
	}
	msg, ok := v.CategoryErrors[f]
	return msg, ok
}

func (v *FAQView) newCategoryDraft() FAQCategory {
	if v.CategoryDraft.ID != "" {
		return FAQCategory{}
	}
	return v.CategoryDraft
}

func (v *FAQView) categoryDescribedBy(id, f, base string) string {
	if _, ok := v.CategoryErr(id, f); ok {
		return strings.TrimSpace(base + " " + categoryErrID(id, f))
	}
	return base
}

func categoryErrID(id, f string) string {
	if id == "" {
		return "faq-new-category-" + f + "-error"
	}
	return "faq-category-err-" + f + "-" + id
}

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

// categoryRefusalForm names the refused category form in the page's summary.
func (v *FAQView) categoryRefusalForm() i18n.Key {
	if v.CategoryDraft.ID == "" {
		return i18n.KeyAdminFaqpCategoryAdd
	}
	return i18n.KeyAdminFaqpCategories
}

// categoryFieldID is the input of field f on the refused category form.
func (v *FAQView) categoryFieldID(f string) string {
	id := v.CategoryDraft.ID
	switch {
	case id == "" && f == "name":
		return "faq-new-category-name"
	case id == "":
		return "faq-new-category-name-en"
	case f == "name":
		return "fc-name-" + id
	default:
		return "fc-name-en-" + id
	}
}

// exampleID is the id of a field's visible example, or "" when it has none.
func exampleID(input, example string) string {
	if example == "" {
		return ""
	}
	return input + "-example"
}

// categoryAttrs links the example to a field that was not refused; a refused
// one carries it in Describes, beside its error, so the attribute is not doubled.
func categoryAttrs(refused, required bool, example string) templ.Attributes {
	attrs := templ.Attributes{"maxlength": "40"}
	if required {
		attrs["required"] = true
	}
	if !refused && example != "" {
		attrs["aria-describedby"] = example
	}
	return attrs
}
