package layouts

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
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
		return "mailto:" + ContactEmail
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
		return ContactEmail
	case EmailLinkVerify:
		return i18n.T(ctx, i18n.KeyEmailSection)
	case EmailLinkReset:
		return i18n.T(ctx, i18n.KeyResetAgain)
	default:
		return ""
	}
}

type EmailLinkView struct {
	Heading  string
	Body     string
	Action   string
	Submit   string
	Token    string
	Recovery EmailLinkRecovery
}

func EmailLinkMeta(title string) Page {
	return Page{Title: title}
}
