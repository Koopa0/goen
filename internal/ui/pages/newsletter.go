package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
)

type EmailLinkRecovery uint8

const (
	EmailLinkNoRecovery EmailLinkRecovery = iota
	EmailLinkSubscribe
	EmailLinkContact
	EmailLinkVerify
	EmailLinkReset
)

func (r EmailLinkRecovery) URL() string {
	switch r {
	case EmailLinkContact:
		return "mailto:" + layouts.ContactEmail
	case EmailLinkVerify:
		return "/account#email-heading"
	case EmailLinkReset:
		return "/forgot"
	default:
		return ""
	}
}

func (r EmailLinkRecovery) Label(ctx context.Context) string {
	switch r {
	case EmailLinkContact:
		return layouts.ContactEmail
	case EmailLinkVerify:
		return i18n.T(ctx, i18n.KeyEmailResend)
	case EmailLinkReset:
		return i18n.T(ctx, i18n.KeyResetAgain)
	default:
		return ""
	}
}

type NewsletterActionView struct {
	Heading  string
	Body     string
	Action   string
	Submit   string
	Token    string
	Recovery EmailLinkRecovery
}

func NewsletterMeta(title string) layouts.Page {
	return layouts.Page{Title: title}
}
