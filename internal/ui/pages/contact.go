package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

func ContactMeta(ctx context.Context) layouts.Page {
	return layouts.Page{
		Title:       i18n.T(ctx, i18n.KeyContactTitle),
		Description: i18n.T(ctx, i18n.KeyContactDescription),
	}
}

type ContactSubject struct {
	Value string
	Label string
}

type ContactForm struct {
	Name     string
	Email    string
	Subject  string
	OrderRef string
	Message  string

	Subjects []ContactSubject

	Errors map[string]string

	Done bool
	// SignedIn offers a finished message's sender their own orders.
	SignedIn bool
}

func (f ContactForm) err(field string) string { return f.Errors[field] }

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
