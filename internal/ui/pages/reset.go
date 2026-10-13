package pages

import (
	"context"
	"fmt"

	"github.com/koopa0/goen/internal/i18n"
)

type ForgotView struct {
	// Sent is true for any address, so accounts cannot be enumerated.
	Sent    bool
	Address string
	Notice  string
}

func (v ForgotView) SentMessage(ctx context.Context) string {
	if v.Address != "" {
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyForgotSentTo), v.Address)
	}
	return i18n.T(ctx, i18n.KeyForgotSent)
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
