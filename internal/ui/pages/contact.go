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

// ContactForm is what the contact panel renders. Handlers own validation and
// fill Errors; the template only displays what it is given.
type ContactForm struct {
	Name     string
	Email    string
	Subject  string
	OrderRef string
	Message  string

	// Subjects lists the choices the subject control offers. The handler supplies
	// it so the allowed set has exactly one definition, in the contact
	// feature, rather than a second copy in this template.
	// Each is the value that will be STORED plus the label to show, because the
	// stored value is Chinese by design and the label follows the reader.
	Subjects []ContactSubject

	// Errors maps a field name to its message. An entry under the empty key
	// is a form-level error that belongs to no single field.
	Errors map[string]string

	// Done reports that the message was stored, which replaces the form with
	// an acknowledgement.
	Done bool
}

// err returns the message recorded for a field, or "".
func (f ContactForm) err(field string) string { return f.Errors[field] }

// invalid renders aria-invalid for a field.
func (f ContactForm) invalid(field string) string {
	if f.Errors[field] != "" {
		return "true"
	}
	return "false"
}

// describedBy names the element holding a field's error message. It returns ""
// when the field is valid, so the attribute is omitted rather than pointing at
// an element that does not exist.
func (f ContactForm) describedBy(field string) string {
	if f.Errors[field] == "" {
		return ""
	}
	return "contact-" + field + "-error"
}

// errorID is the id the message element carries.
func errorID(field string) string { return "contact-" + field + "-error" }

// hasFieldErrors reports whether any field-level error is present.
func (f ContactForm) hasFieldErrors() bool {
	for field, msg := range f.Errors {
		if field != "" && msg != "" {
			return true
		}
	}
	return false
}
