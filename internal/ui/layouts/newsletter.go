package layouts

import "github.com/a-h/templ"

func (s NewsletterState) emailAttrs(attrs templ.Attributes) templ.Attributes {
	if s.FieldRefusal == "" {
		attrs["aria-describedby"] = "newsletter-email-example"
	}
	return attrs
}
