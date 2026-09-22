package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// ForgotView is the "email me a link" page.
type ForgotView struct {
	// Sent is true for any address, so accounts cannot be enumerated.
	Sent bool
}

// ResetView is the "set a new password" page.
type ResetView struct {
	Token   string
	Error   string
	Expired bool
}

// Usable reports whether the form should be offered.
func (v ResetView) Usable() bool { return !v.Expired && v.Token != "" }

// HasError reports whether the password was refused.
func (v ResetView) HasError() bool { return v.Error != "" }

// Refusal is why the form is not being offered.
func (v ResetView) Refusal(ctx context.Context) string {
	if v.Expired {
		return i18n.T(ctx, i18n.KeyResetDead)
	}
	return i18n.T(ctx, i18n.KeyResetNoToken)
}
