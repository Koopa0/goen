package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// ForgotView is the "email me a link" page.
type ForgotView struct {
	// Sent is true after a submission for any address, registered or not: saying
	// more would make the page a way to enumerate accounts.
	Sent bool
}

// ResetView is the "set a new password" page.
type ResetView struct {
	Token string
	// Error is why the password was refused; Expired is a spent, unknown or old
	// link. Separate because one is retyped and the other needs a new link.
	Error   string
	Expired bool
}

// Usable reports whether the form should be offered.
func (v ResetView) Usable() bool { return !v.Expired && v.Token != "" }

// HasError reports whether the password was refused.
func (v ResetView) HasError() bool { return v.Error != "" }

// Invalid is the aria-invalid value for the password field.
func (v ResetView) Invalid() string {
	if v.HasError() {
		return "true"
	}
	return "false"
}

// Refusal is why the form is not being offered.
func (v ResetView) Refusal(ctx context.Context) string {
	if v.Expired {
		return i18n.T(ctx, i18n.KeyResetDead)
	}
	return i18n.T(ctx, i18n.KeyResetNoToken)
}
