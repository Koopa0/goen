package account

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/koopa0/goen/internal/email"
	"github.com/koopa0/goen/internal/user"
)

const passwordSetupNoticeCookie = "goen_password_setup"
const passwordSetupNoticeTTL = 10 * time.Minute

func (h *Handler) writePasswordSetupNotice(w http.ResponseWriter, u user.User) {
	value := base64.RawURLEncoding.EncodeToString([]byte(u.ID + "|" + email.Clean(u.Email)))
	//nolint:gosec // G124: Secure follows the deployment flag, as the session cookie does.
	http.SetCookie(w, &http.Cookie{
		Name: passwordSetupNoticeCookie, Value: value, Path: "/account",
		MaxAge: int(passwordSetupNoticeTTL.Seconds()), HttpOnly: true,
		Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) consumePasswordSetupNotice(w http.ResponseWriter, r *http.Request, u user.User) bool {
	cookie, err := r.Cookie(passwordSetupNoticeCookie)
	if err != nil {
		return false
	}
	// This is a display receipt only; it never authorizes a credential change.
	//nolint:gosec // G124: clear with the attributes that created the cookie.
	http.SetCookie(w, &http.Cookie{
		Name: passwordSetupNoticeCookie, Path: "/account", MaxAge: -1,
		HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode,
	})
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return false
	}
	id, address, ok := strings.Cut(string(raw), "|")
	return ok && id != "" && id == u.ID && address == email.Clean(u.Email)
}
