package pages

import "github.com/koopa0/goen/internal/ui/layouts"

// NewsletterActionView is what [NewsletterAction] renders. An empty Token means
// there is nothing left to do, and the form is left out rather than shown with
// a button that would fail.
type NewsletterActionView struct {
	Heading string
	Body    string
	// Action is the path the form posts to; one template serves both links.
	Action string
	Submit string
	Token  string
}

// NewsletterMeta is the document shell for both newsletter link pages.
func NewsletterMeta(title string) layouts.Page {
	return layouts.Page{Title: title}
}
