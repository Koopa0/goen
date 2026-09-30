package pages

import "github.com/koopa0/goen/internal/ui/layouts"

// NewsletterActionView is what [NewsletterAction] renders.
type NewsletterActionView struct {
	Heading string
	Body    string
	Action  string
	Submit  string
	Token   string
	// Next is a same-site path the form carries back, or empty for none.
	Next string
}

// NewsletterMeta is the document shell for both newsletter link pages.
func NewsletterMeta(title string) layouts.Page {
	return layouts.Page{Title: title}
}
