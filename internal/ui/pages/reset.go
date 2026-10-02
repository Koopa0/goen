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

func (v ResetView) Refusal(ctx context.Context) string {
	if v.Expired {
		return i18n.T(ctx, i18n.KeyResetDead)
	}
	return i18n.T(ctx, i18n.KeyResetNoToken)
}
