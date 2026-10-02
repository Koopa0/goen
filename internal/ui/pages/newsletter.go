package pages

import "github.com/koopa0/goen/internal/ui/layouts"

type NewsletterActionView struct {
	Heading string
	Body    string
	Action  string
	Submit  string
	Token   string
}

func NewsletterMeta(title string) layouts.Page {
	return layouts.Page{Title: title}
}
