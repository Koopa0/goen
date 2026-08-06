package pages

import (
	"context"

	"github.com/koopa0/goen/internal/i18n"
)

// ForgotView is the "email me a link" page.
type ForgotView struct {
	// Sent is true after a submission — for ANY address, whether or not it
	// belongs to somebody. The page cannot say more without becoming a way to
	// find out which addresses are registered.
	Sent bool
}

// ResetView is the "set a new password" page.
type ResetView struct {
	Token string
	// Error is why the new password was refused, and Expired is a link that is
	// spent, unknown or too old. They are separate because the customer's next
	// step differs: one retypes, the other asks for a new link.
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

// Refusal is why the form is not being offered. The two readings are kept
// apart because the customer's next step differs: a spent or expired link needs
// a new one, and a URL with no token at all means they arrived some other way.
func (v ResetView) Refusal(ctx context.Context) string {
	if v.Expired {
		return i18n.T(ctx, i18n.KeyResetDead)
	}
	return i18n.T(ctx, i18n.KeyResetNoToken)
}
