package site

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web/formtext"
	"github.com/koopa0/goen/internal/web/siteurl"
)

func (h *Handler) SetLocale(w http.ResponseWriter, r *http.Request) {
	if err := formtext.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	// An unknown value sets the default: a 400 on a language switch is a dead
	// end for somebody who cannot read the page they are on.
	i18n.SetCookie(w, i18n.Parse(r.PostFormValue("locale")), h.secure)
	http.Redirect(w, r, siteurl.PathOr(r.PostFormValue("return"), "/"), http.StatusSeeOther) //nolint:gosec // G710: bounded by siteurl.PathOr
}
