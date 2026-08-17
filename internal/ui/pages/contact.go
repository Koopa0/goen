package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

// ContactMeta is the chrome view model for the contact page.
func ContactMeta(ctx context.Context) layouts.Page {
	return layouts.Page{
		Title:       i18n.T(ctx, i18n.KeyContactTitle),
		Description: i18n.T(ctx, i18n.KeyContactDescription),
	}
}

// ContactSubject is one option in the subject control: what gets stored, and
// what the reader sees.
type ContactSubject struct {
	Value string
	Label string
}

// ContactForm is what the contact panel renders.
type ContactForm struct {
	Name     string
	Email    string
	Subject  string
	OrderRef string
	Message  string

	// Subjects is supplied by the handler, so the allowed set has one
	// definition. The stored value is Chinese by design; the label follows the
	// reader.
	Subjects []ContactSubject

	// Errors maps a field name to its message; the empty key is a form-level
	// error belonging to no single field.
	Errors map[string]string

	// Done replaces the form with an acknowledgement.
	Done bool
}

func (f ContactForm) err(field string) string { return f.Errors[field] }

func (f ContactForm) invalid(field string) string {
	if f.Errors[field] != "" {
		return "true"
	}
	return "false"
}

// describedBy names the element holding a field's error message, and "" when
// the field is valid so the attribute is omitted rather than dangling.
func (f ContactForm) describedBy(field string) string {
	if f.Errors[field] == "" {
		return ""
	}
	return "contact-" + field + "-error"
}

func errorID(field string) string { return "contact-" + field + "-error" }

func (f ContactForm) hasFieldErrors() bool {
	for field, msg := range f.Errors {
		if field != "" && msg != "" {
			return true
		}
	}
	return false
}
