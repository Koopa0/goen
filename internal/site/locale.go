package site

import (
	"net/http"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/web"
)

// SetLocale serves POST /locale.
func (h *Handler) SetLocale(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	// An unknown value sets the default: a 400 on a language switch is a dead
	// end for somebody who cannot read the page they are on.
	i18n.SetCookie(w, i18n.Parse(r.PostFormValue("locale")), h.secure)
	//nolint:gosec // G710: web.SitePathOr accepts only a same-site path; the
	// taint analyser cannot see through it, and internal/web's own tests are
	// what keep that true.
	http.Redirect(w, r, web.SitePathOr(r.PostFormValue("return"), "/"), http.StatusSeeOther)
}
