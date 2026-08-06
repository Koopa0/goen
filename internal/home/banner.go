package home

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/koopa0/goen/internal/i18n"
	"github.com/koopa0/goen/internal/ui/layouts"
	"github.com/koopa0/goen/internal/web"
)

// DismissCookie remembers which banner somebody closed.
//
// It holds a DIGEST of the banner's id, not the id: the cookie travels on every
// request and a raw uuid there is a value somebody can correlate across
// sessions for no benefit. A digest answers the only question the server asks —
// "is this the one they closed" — and answers nothing else.
const DismissCookie = "__Host-goen_promo"

// insecureDismissCookie is the development name. A __Host- cookie is never sent
// back over plain http://, so a dev machine would appear to forget the
// dismissal on every request.
const insecureDismissCookie = "goen_promo"

// DismissMaxAge is a year.
//
// Long, because the cookie is keyed to ONE banner: the promotion ending is what
// brings the strip back, not the cookie expiring. A short expiry would
// re-show a promotion somebody has already dismissed and read.
const DismissMaxAge = 365 * 24 * 60 * 60

// Banner reads the promotion to show this visitor, or nothing.
//
// dismissed is the digest the request carried. A banner the visitor has closed
// returns the zero value, which renders nothing at all.
func (s *Store) Banner(ctx context.Context, dismissed string) (layouts.Banner, error) {
	row, err := s.q.CurrentPromoBanner(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No promotion running. Not an error, and not a fallback either:
			// unlike the hero, a shop with nothing to announce should announce
			// nothing. An always-on banner is one people stop seeing.
			return layouts.Banner{}, nil
		}
		return layouts.Banner{}, fmt.Errorf("read promo banner: %w", err)
	}

	id := row.ID.String()
	if dismissed != "" && dismissed == DismissDigest(id) {
		return layouts.Banner{}, nil
	}

	// The CTA is typed by a person and rendered into an href. web.SitePath is
	// the one owner of that rule — an absolute URL here would send every
	// visitor off-site from the top of every page.
	href, ok := web.SitePath(row.CtaHref)
	if !ok {
		href = ""
	}
	label := row.CtaLabel
	if href == "" {
		// Both or neither: a button with no destination is worse than no
		// button, and promo_banners_cta_complete says the same in the schema.
		label = ""
	}

	return layouts.Banner{
		ID: id, Message: row.Message, MessageShort: row.MessageShort,
		Code: row.Code, CTALabel: label, CTAHref: href,
	}, nil
}

// DismissDigest is what the cookie stores for a banner id.
func DismissDigest(id string) string {
	sum := sha256.Sum256([]byte("goen-promo:" + id))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// ReadDismissal is the digest this request carried, or "".
func ReadDismissal(r *http.Request, secure bool) string {
	name := insecureDismissCookie
	if secure {
		name = DismissCookie
	}
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// WriteDismissal remembers that this visitor closed this banner.
func WriteDismissal(w http.ResponseWriter, id string, secure bool) {
	name := insecureDismissCookie
	if secure {
		name = DismissCookie
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: dev-only opt-out, secure by default
		Name:     name,
		Value:    DismissDigest(id),
		Path:     "/",
		MaxAge:   DismissMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Dismiss serves POST /promo/dismiss.
//
// A FORM, not a script. Closing the banner writes a cookie, the write-face rule
// covers every mutation, and this is the whole of it: submit, set cookie, 303
// back. There is nothing for JavaScript to add.
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	// The id is echoed back by the form so the cookie names the banner that was
	// actually on screen — dismissing whatever is current at the moment the
	// POST lands would close a promotion the visitor never saw.
	if id := r.PostFormValue("banner"); id != "" {
		WriteDismissal(w, id, h.secure)
	}
	// Back to the page they were reading. Validated as a same-site path by the
	// one owner of that rule; the taint analyser cannot see through it, and
	// internal/web's own tests are what keep the claim true.
	//nolint:gosec // G710: web.SitePathOr accepts only a same-site path
	http.Redirect(w, r, web.SitePathOr(r.PostFormValue("return"), "/"), http.StatusSeeOther)
}

// Nav is the header's category row, in the reader's language.
//
// It lives beside Banner because both are site CHROME read by middleware on every
// request rather than by a page's own handler — the lesson layouts.Page.CartCount
// taught: a field each handler must remember to fill is a field that goes
// unfilled.
func (s *Store) Nav(ctx context.Context) ([]layouts.NavItem, error) {
	rows, err := s.q.NavCategories(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		return nil, fmt.Errorf("read nav categories: %w", err)
	}
	items := make([]layouts.NavItem, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		items = append(items, layouts.NavItem{
			Slug: r.Slug, Name: r.Name, Href: "/c/" + r.Slug,
		})
	}
	return items, nil
}
