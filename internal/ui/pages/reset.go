package pages

import (
	"context"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
)

type ForgotView struct {
	// Sent is true for any address, so accounts cannot be enumerated.
	Sent   bool
	Notice string
}

type ResetView struct {
	Token      string
	Error      string
	ErrorField string
	Expired    bool
}

func (v ResetView) Usable() bool { return !v.Expired && v.Token != "" }

func (v ResetView) HasError() bool { return v.Error != "" }

func (v ResetView) Invalid(field string) bool {
	if !v.HasError() {
		return false
	}
	if v.ErrorField == "" {
		return field == "password"
	}
	return v.ErrorField == field
}

func (v ResetView) PasswordAttrs(attrs templ.Attributes) templ.Attributes {
	if !v.Invalid("password") {
		attrs["aria-describedby"] = "password-hint"
	}
	return attrs
}

func (v ResetView) RecoveryView(ctx context.Context) EmailLinkView {
	view := EmailLinkView{
		Heading:  i18n.T(ctx, i18n.KeyResetTitle),
		Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
		Recovery: EmailLinkReset,
	}
	if v.Expired {
		view.Heading = i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)
		view.Body = i18n.T(ctx, i18n.KeyResetDead)
	}
	return view
}
