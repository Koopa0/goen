package pages

import "github.com/koopa0/goen/internal/ui/layouts"

// NewsletterActionView is what [NewsletterAction] renders.
//
// Token empty means there is nothing left to do — a broken link, or a write that
// has already happened — and the form is left out rather than shown with a
// button that would fail. A page whose only control cannot work is worse than a
// page with no control.
type NewsletterActionView struct {
	Heading string
	Body    string
	// Action is the path the form posts to. A field rather than a constant
	// because one template serves both links, and each form has to name its own
	// handler.
	Action string
	Submit string
	Token  string
}

// NewsletterMeta is the document shell for both newsletter link pages.
func NewsletterMeta(title string) layouts.Page {
	return layouts.Page{Title: title}
}
