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

// DismissCookie remembers which banner somebody closed. It holds a digest of the
// banner's id, never the id, because it travels on every request.
const DismissCookie = "__Host-goen_promo"

// insecureDismissCookie is the development name: a __Host- cookie is never sent
// back over plain http://.
const insecureDismissCookie = "goen_promo"

// DismissMaxAge is a year. The cookie is keyed to one banner, so the promotion
// ending is what brings the strip back, not the cookie expiring.
const DismissMaxAge = 365 * 24 * 60 * 60

// Banner reads the promotion to show this visitor, or nothing. dismissed is the
// digest the request carried.
func (s *Store) Banner(ctx context.Context, dismissed string) (layouts.Banner, error) {
	row, err := s.q.CurrentPromoBanner(ctx, string(i18n.FromContext(ctx)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No promotion running, and no fallback: unlike the hero, a shop with
			// nothing to announce announces nothing.
			return layouts.Banner{}, nil
		}
		return layouts.Banner{}, fmt.Errorf("read promo banner: %w", err)
	}

	id := row.ID.String()
	if dismissed != "" && dismissed == DismissDigest(id) {
		return layouts.Banner{}, nil
	}

	// Typed by a person and rendered into an href at the top of every page.
	href, ok := web.SitePath(row.CtaHref)
	if !ok {
		href = ""
	}
	label := row.CtaLabel
	if href == "" {
		// Both or neither, which promo_banners_cta_complete also says.
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
func (h *Handler) Dismiss(w http.ResponseWriter, r *http.Request) {
	if err := web.ParseForm(w, r); err != nil {
		http.Error(w, "400", http.StatusBadRequest)
		return
	}
	// The form echoes the id, so the cookie names the banner that was on screen
	// rather than whatever is current when the POST lands.
	if id := r.PostFormValue("banner"); id != "" {
		WriteDismissal(w, id, h.secure)
	}
	//nolint:gosec // G710: web.SitePathOr accepts only a same-site path
	http.Redirect(w, r, web.SitePathOr(r.PostFormValue("return"), "/"), http.StatusSeeOther)
}

// Nav is the header's category row, in the reader's language. Like Banner it is
// read by middleware rather than by a page's own handler.
func (s *Store) Nav(ctx context.Context) ([]layouts.NavItem, error) {
	rows, err := s.q.RootCategories(ctx, string(i18n.FromContext(ctx)))
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
