package auth

import (
	"context"
	"fmt"

	"github.com/a-h/templ"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/frame"
	"github.com/koopa0/goen/internal/ui/widget"
)

type ForgotView struct {
	// Sent is true for any address, so accounts cannot be enumerated.
	Sent    bool
	Address string
	Notice  widget.Result
}

func (v ForgotView) SentMessage(ctx context.Context) string {
	if v.Address != "" {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyForgotSentTo), v.Address)
	}
	return i18n.T(ctx, i18n.KeyForgotSent)
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

func (v ResetView) RecoveryView(ctx context.Context) frame.EmailLinkView {
	view := frame.EmailLinkView{
		Heading:  i18n.T(ctx, i18n.KeyResetTitle),
		Body:     i18n.T(ctx, i18n.KeyEmailLinkIncomplete),
		Recovery: frame.EmailLinkReset,
	}
	if v.Expired {
		view.Heading = i18n.T(ctx, i18n.KeyEmailLinkDeadTitle)
		view.Body = i18n.T(ctx, i18n.KeyResetDead)
	}
	return view
}
