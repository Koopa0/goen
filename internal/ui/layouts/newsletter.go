package layouts

import (
	"context"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/fieldrule"
	"github.com/koopa0/goen/internal/i18n"
)

func (s NewsletterState) emailAttrs(ctx context.Context) templ.Attributes {
	attrs := fieldrule.Email.Attrs(ctx, templ.Attributes{
		"required": true,
		"autocomplete": "email",
		"maxlength": "254",
	}, fieldrule.Message(i18n.KeyEmailMalformed), fieldrule.ErrorID("newsletter-error"))
	if s.FieldRefusal == "" {
		attrs["aria-describedby"] = "newsletter-email-example"
	}
	return attrs
}
