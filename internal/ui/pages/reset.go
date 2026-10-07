package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

type ForgotView struct {
	// Sent is true for any address, so accounts cannot be enumerated.
	Sent   bool
	Notice string
}

type ResetView struct {
	Token   string
	Error   string
	Expired bool
}

func (v ResetView) Usable() bool { return !v.Expired && v.Token != "" }

func (v ResetView) HasError() bool { return v.Error != "" }

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
